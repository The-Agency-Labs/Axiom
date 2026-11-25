package hardcover

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Cleaning of Hardcover series lists.
//
// Hardcover lists every translation, split edition ("Part 1"), audio
// dramatisation, box set and placeholder as a book of the series, so a
// seven-book saga has dozens of entries. None of the signals is reliable on
// its own (flags are often missing, titles are free text), so they are
// combined, from the most certain to the most statistical:
//
//  1. structural: translations (canonical_id), box sets (details such as
//     "1-4", "Boxed Set" titles), placeholders. book_series.featured is NOT
//     used: it only says whether the series is the book's main one, and
//     real books are often unfeatured;
//  2. language and script: editions in another language or script than the
//     series' own, when a local edition competes;
//  3. split editions: part markers in titles and details, is_partial_book,
//     a fractional position whose title repeats the whole book's title,
//     several unmarked fractions under one floor;
//  4. compilation-flagged entries that a comparably read single book sits
//     beside (the flag is unreliable: real collected-novella books carry it);
//  5. one book per position, the most read;
//  6. readership: a book read by a tiny fraction of the series' typical book
//     is a stray edition, not part of the series (unreleased and
//     just-released books are exempt, nobody has read them yet).
//
// Nothing here knows a series name or id; every rule is a general pattern.

const (
	// minReadership is the share of the series' typical (median) book's
	// readers below which a book is dropped. Readers are measured against the
	// median, not the most-read book: one blockbuster (the first volume of a
	// long saga, with 50x the readers of its prequels) would otherwise sweep
	// away real, lesser-read sequels. Real novellas and prequels measured
	// 5-100% of the median in live data; stray editions, magazine listings and
	// fan entries were under 1.5%. 2% sits in that gap.
	minReadership = 0.02
	// readershipFloor is the median readership below which the rule is not
	// applied: with a handful of readers the ratio means nothing (new or niche
	// series). minBooksForReadership: nor with fewer books than this.
	readershipFloor       = 100
	minBooksForReadership = 3
	// recentYears is how long a book counts as "just released": its readers
	// have not accumulated yet, so the relative readership rule skips it.
	recentYears = 1
)

// nowFunc is the clock of the cleaning (overridden by tests).
var nowFunc = time.Now

var (
	// Part markers in titles, in the languages Hardcover editions come in:
	// "Part 1", "Part.1", "Partie 2", "Parte I", "Teil 2", "Tome 1", "Deel 3",
	// "Część 2", "Часть 1", "Том 2". The word must be followed by a number
	// (digits, a roman numeral or a spelled-out number) so "Parts Unknown"
	// or "Tomorrow" never match.
	partWordRe = regexp.MustCompile(`(?i)(?:^|[\s,;:(\[–—-])(?:part|pt|partie|parte|teil|tome|deel|część|czesc|часть|том)\.?\s*(?:\d{1,2}|[ivx]{1,4}|one|two|three|four|five|six|un|une|deux|trois|eins|zwei|drei|uno|due|tre|dos|ein|een|twee|drie|jeden|dwa|trzy)(?:$|[\s,;:)\].–—-])`)
	// "(1 of 3)", "Part 2 of 5", "Volume 1 of 2", "1/3": a fraction names a
	// piece of a bigger work.
	ofRe = regexp.MustCompile(`(?i)(?:^|[\s(\[,;:])(?:(?:part|pt|volume|vol|book|livre|libro|teil|band)\.?\s*)?\d{1,2}\s*(?:of|/|von|de|di|van|z)\s*\d{1,2}(?:$|[\s)\],;:.])`)
	// book_series.details of a piece: "1, Part 1", "Part 2 of 2", "pt. 1/2".
	detailsPartRe = regexp.MustCompile(`(?i)(?:\b(?:part|pt)\.?\s*\d|\d\s*(?:of|/)\s*\d)`)
	// details / titles of a box set: "1-4", "Books 1-3", "5 Books Collection".
	detailsRangeRe = regexp.MustCompile(`^\s*\d+(?:\.\d+)?\s*[-–]\s*\d+`)
	titleBoxedRe   = regexp.MustCompile(`(?i)\b(?:boxed? ?set|omnibus|books?\s*\d+\s*[-–]\s*\d+|\d+\s+books?\s+(?:collection|set))\b`)
	// Placeholders for books that have no title yet.
	placeholderRe = regexp.MustCompile(`(?i)(?:\buntitled\b|^\s*(?:tba|tbd|tbc|unknown|title (?:tba|tbd|unknown)|to be (?:announced|determined|titled))\b|^\s*(?:book|volume|vol\.?|part|#)\s*\d+\s*$)`)
	nonAlnumRe    = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

type langEdition struct {
	Language *struct {
		Language string `json:"language"`
	} `json:"language"`
}

// seriesEntry is one book_series row as Hardcover sends it.
type seriesEntry struct {
	Position    *float64 `json:"position"`
	Featured    *bool    `json:"featured"`
	Compilation bool     `json:"compilation"`
	Details     string   `json:"details"`
	Book        *struct {
		ID              int          `json:"id"`
		Title           string       `json:"title"`
		CachedImage     string       `json:"cached_image"`
		ReleaseYear     *int         `json:"release_year"`
		ReleaseDate     string       `json:"release_date"`
		CanonicalID     *int         `json:"canonical_id"`
		UsersCount      int          `json:"users_count"`
		IsPartialBook   bool         `json:"is_partial_book"`
		DefaultPhysical *langEdition `json:"default_physical_edition"`
		DefaultEbook    *langEdition `json:"default_ebook_edition"`
		DefaultCover    *langEdition `json:"default_cover_edition"`
	} `json:"book"`
}

type candidate struct {
	pos     float64
	id      int
	title   string
	image   string
	users   int
	year    int
	date    string
	langs   map[string]bool
	suspect bool // looks like a piece of a split edition
	// compilation is Hardcover's book_series.compilation flag. It is
	// unreliable (it also marks real books of collected novellas), so it only
	// loses to a comparably read non-compilation book at its position.
	compilation bool
}

func isInteger(p float64) bool { return p == math.Floor(p) }

func normTitle(s string) string {
	return strings.TrimSpace(nonAlnumRe.ReplaceAllString(strings.ToLower(s), " "))
}

// titleScript is "latin" or "other" by the majority of the title's letters.
func titleScript(s string) string {
	latin, other := 0, 0
	for _, r := range s {
		switch {
		case !unicode.IsLetter(r):
		case unicode.Is(unicode.Latin, r):
			latin++
		default:
			other++
		}
	}
	if other > latin {
		return "other"
	}
	return "latin"
}

func editionLanguages(eds ...*langEdition) map[string]bool {
	out := map[string]bool{}
	for _, e := range eds {
		if e != nil && e.Language != nil && e.Language.Language != "" {
			out[strings.ToLower(e.Language.Language)] = true
		}
	}
	return out
}

// isPartTitle reports a split-edition marker in a title or in the entry's
// details ("Part 1", "(2 of 3)", "pt. 1/2").
func isPartTitle(title, details string) bool {
	return partWordRe.MatchString(title) || ofRe.MatchString(title) || detailsPartRe.MatchString(details)
}

// recent reports a book released in the last recentYears or not yet.
func recent(c *candidate, now time.Time) bool {
	if c.year >= now.Year()-recentYears {
		return true
	}
	if t, err := time.Parse("2006-01-02", c.date); err == nil && t.After(now) {
		return true
	}
	return false
}

// cleanSeriesBooks reduces Hardcover's entries of a series to its real books
// in reading order (see the package comment above).
func cleanSeriesBooks(entries []seriesEntry, now time.Time) []providers.SeriesBook {
	// 1. Structural filters.
	var cands []*candidate
	for i := range entries {
		e := &entries[i]
		if e.Book == nil || e.Position == nil {
			continue
		}
		b := e.Book
		switch {
		case b.CanonicalID != nil: // a translation or re-listing of another book
			continue
		case detailsRangeRe.MatchString(e.Details), titleBoxedRe.MatchString(b.Title):
			continue // box set, omnibus
		case placeholderRe.MatchString(b.Title):
			continue
		}
		c := &candidate{
			pos: *e.Position, id: b.ID, title: b.Title, image: b.CachedImage, users: b.UsersCount, date: b.ReleaseDate,
			langs:       editionLanguages(b.DefaultPhysical, b.DefaultEbook, b.DefaultCover),
			compilation: e.Compilation,
			suspect:     b.IsPartialBook || isPartTitle(b.Title, e.Details),
		}
		if b.ReleaseYear != nil {
			c.year = *b.ReleaseYear
		}
		cands = append(cands, c)
	}

	cands = dropForeign(cands)
	cands = dropSplits(cands)
	cands = dropCompilations(cands)

	// 4. One book per position: the most read (first listed on a tie).
	best := map[float64]*candidate{}
	var order []float64
	for _, c := range cands {
		b, ok := best[c.pos]
		if !ok {
			order = append(order, c.pos)
		}
		if !ok || c.users > b.users {
			best[c.pos] = c
		}
	}
	sort.Float64s(order)

	// 5. Readership relative to the series' typical book (the median).
	users := make([]int, 0, len(order))
	for _, p := range order {
		users = append(users, best[p].users)
	}
	sort.Ints(users)
	typical := 0
	if len(users) >= minBooksForReadership {
		typical = users[len(users)/2]
	}
	out := make([]providers.SeriesBook, 0, len(order))
	for _, p := range order {
		c := best[p]
		if typical >= readershipFloor && float64(c.users) < minReadership*float64(typical) && !recent(c, now) {
			continue
		}
		out = append(out, providers.SeriesBook{
			Position: c.pos, ExternalID: strconv.Itoa(c.id), Title: c.title, Image: c.image, Year: c.year,
		})
	}
	return out
}

// dropForeign removes books that are editions in another language than the
// series' own (a German 2-in-1, a Russian edition with no canonical_id).
// The series' language is the one with the most readers among the entries
// that name one, its script likewise.
//
//   - a title in another script than the series' is dropped;
//   - an entry that names languages, none of them the series', yields to an
//     entry in the series' language (or naming none) under the same floor
//     position that has at least a tenth of its readers. The edition
//     languages Hardcover reports are noisy (the real, most-read edition may
//     only list a Dutch and a German default edition), so a foreign-looking
//     entry nobody local competes with is kept.
func dropForeign(cands []*candidate) []*candidate {
	byLang := map[string]int{}
	byScript := map[string]int{}
	for _, c := range cands {
		for l := range c.langs {
			byLang[l] += c.users + 1
		}
		byScript[titleScript(c.title)] += c.users + 1
	}
	dominant := ""
	for l, n := range byLang {
		if dominant == "" || n > byLang[dominant] || (n == byLang[dominant] && l < dominant) {
			dominant = l
		}
	}
	script := "latin"
	if byScript["other"] > byScript["latin"] {
		script = "other"
	}
	foreign := func(c *candidate) bool { return dominant != "" && len(c.langs) > 0 && !c.langs[dominant] }
	var out []*candidate
	for _, c := range cands {
		if titleScript(c.title) != script {
			continue
		}
		if foreign(c) && hasLocalRival(cands, c, foreign) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func hasLocalRival(cands []*candidate, c *candidate, foreign func(*candidate) bool) bool {
	for _, d := range cands {
		if d != c && math.Floor(d.pos) == math.Floor(c.pos) && !foreign(d) && d.users*10 >= c.users {
			return true
		}
	}
	return false
}

// dropSplits removes the pieces of split editions: "The Way of Kings, Part 1"
// at 1.1, "The Final Empire (1 of 3)" at 1.3 or even at 1.
//
//   - a piece (part marker, "N of M", details "Part N", is_partial_book) is
//     dropped when the book it belongs to is listed: at a fractional position
//     the integer-position book of the same floor, at an integer position
//     another, unmarked book at that very position. Marked books alone at
//     their integer position stay (serialised books such as "Part One: ...");
//   - an unmarked fractional entry, not a novella (x.5), whose title contains
//     the title of the whole book at its floor is a split edition too
//     ("Dune: Parte I" at 1.2);
//   - two or more unmarked fractional entries (not x.5) under one floor are
//     the parts of a split book ("Les noces pourpres" 3.3, "La loi du
//     régicide" 3.4), novellas come alone.
func dropSplits(cands []*candidate) []*candidate {
	whole := map[float64]*candidate{} // integer position -> most-read unmarked book
	for _, c := range cands {
		if isInteger(c.pos) && !c.suspect {
			if w, ok := whole[c.pos]; !ok || c.users > w.users {
				whole[c.pos] = c
			}
		}
	}
	var kept []*candidate
	for _, c := range cands {
		if isInteger(c.pos) {
			if c.suspect && whole[c.pos] != nil {
				continue
			}
			kept = append(kept, c)
			continue
		}
		w := whole[math.Floor(c.pos)]
		if w != nil {
			if c.suspect {
				continue
			}
			isNovella := c.pos-math.Floor(c.pos) == 0.5
			if wt := normTitle(w.title); !isNovella && len(wt) >= 4 && c.id != w.id &&
				strings.Contains(" "+normTitle(c.title)+" ", " "+wt+" ") {
				continue
			}
		}
		kept = append(kept, c)
	}
	siblings := map[float64]int{}
	for _, c := range kept {
		if !isInteger(c.pos) && c.pos-math.Floor(c.pos) != 0.5 && whole[math.Floor(c.pos)] != nil {
			siblings[math.Floor(c.pos)]++
		}
	}
	out := kept[:0]
	for _, c := range kept {
		if !isInteger(c.pos) && c.pos-math.Floor(c.pos) != 0.5 && siblings[math.Floor(c.pos)] >= 2 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// dropCompilations removes entries flagged as compilations when a
// non-compilation entry with at least a tenth of their readers sits at the
// same position (the real book next to its box set). A flagged entry nothing
// like that competes with stays: it is probably a real book.
func dropCompilations(cands []*candidate) []*candidate {
	var out []*candidate
	for _, c := range cands {
		if c.compilation {
			rival := false
			for _, d := range cands {
				if !d.compilation && d.pos == c.pos && d.users*10 >= c.users {
					rival = true
					break
				}
			}
			if rival {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

type rankedSeries struct {
	providers.BookSeries
	featured bool
}

// rankBookSeries puts the series a book is in into primary-first order.
// Hardcover already lists the featured ones first; on top of that,
// reading-order and umbrella lists ("Foo Universe (Chronological)") sort
// after every regular series, so one is only primary when it is the only
// series the book is in. Order is otherwise kept.
func rankBookSeries(in []rankedSeries) []providers.BookSeries {
	rank := func(s rankedSeries) int {
		switch {
		case providers.IsMetaSeries(s.Series.Name):
			return 2
		case !s.featured:
			return 1
		}
		return 0
	}
	sort.SliceStable(in, func(i, j int) bool { return rank(in[i]) < rank(in[j]) })
	out := make([]providers.BookSeries, len(in))
	for i, s := range in {
		out[i] = s.BookSeries
	}
	return out
}

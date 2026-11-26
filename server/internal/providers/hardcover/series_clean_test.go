package hardcover

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/providers"
)

// row is a compact synthetic book_series entry. Fixtures use public book
// titles for readability; the numbers (readers, years, languages) are made up
// to mimic the failure classes seen in live Hardcover data.
type row struct {
	pos     float64
	id      int
	title   string
	users   int
	year    int
	date    string
	langs   string // comma-separated edition languages, "" = unknown
	details string
	canon   int // canonical_id, 0 = none
	partial bool
	comp    bool
}

var cleanNow = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func entries(t *testing.T, rows ...row) []seriesEntry {
	t.Helper()
	var list []map[string]any
	for _, r := range rows {
		b := map[string]any{
			"id": r.id, "title": r.title, "users_count": r.users, "is_partial_book": r.partial,
			"release_year": r.year, "release_date": r.date,
		}
		if r.year == 0 {
			b["release_year"] = nil
		}
		if r.canon != 0 {
			b["canonical_id"] = r.canon
		}
		if r.langs != "" {
			for i, l := range strings.Split(r.langs, ",") {
				key := []string{"default_physical_edition", "default_ebook_edition", "default_cover_edition"}[i%3]
				b[key] = map[string]any{"language": map[string]any{"language": l}}
			}
		}
		list = append(list, map[string]any{
			"position": r.pos, "featured": true, "compilation": r.comp, "details": r.details, "book": b,
		})
	}
	raw, _ := json.Marshal(list)
	var out []seriesEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func titles(books []providers.SeriesBook) string {
	var s []string
	for _, b := range books {
		s = append(s, fmt.Sprintf("%v:%s", b.Position, b.Title))
	}
	return strings.Join(s, " | ")
}

func clean(t *testing.T, rows ...row) string {
	t.Helper()
	return titles(cleanSeriesBooks(entries(t, rows...), cleanNow))
}

func want(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("books =\n  %s\nwant\n  %s", got, want)
	}
}

// Split editions at fractional positions, with or without a flag or marker.
func TestCleanDropsSplitEditions(t *testing.T) {
	want(t, clean(t,
		row{pos: 1, id: 1, title: "The Way of Kings", users: 10000, year: 2010},
		row{pos: 1.1, id: 2, title: "The Way of Kings, Part 1", users: 494, year: 2010, partial: true},
		row{pos: 1.2, id: 3, title: "The Way of Kings, Part 2", users: 234, year: 2010},
		row{pos: 2, id: 4, title: "Words of Radiance", users: 6500, year: 2014},
		row{pos: 2.1, id: 5, title: "Words of Radiance, Part 1", users: 183, year: 2014},
		row{pos: 2.5, id: 6, title: "Edgedancer", users: 3600, year: 2016},
	), "1:The Way of Kings | 2:Words of Radiance | 2.5:Edgedancer")

	// Marker spellings: "Part.1", "Parte I", "(1 of 3)", other languages.
	for _, title := range []string{
		"A Game of Thrones, Part.1 : The Lords of Winterfell", "Dune: Parte I", "The Final Empire (1 of 3)",
		"Das Rad der Zeit, Teil 2", "La roue du temps, Tome 3", "De Wereld, Deel 1", "Gra o tron, Część 2",
		"Book, Volume 1 of 2", "Book - Part Two", "Book, Partie 2",
	} {
		want(t, clean(t,
			row{pos: 1, id: 1, title: "Whole Book", users: 5000, year: 2000},
			row{pos: 1.3, id: 2, title: title, users: 40, year: 2000},
		), "1:Whole Book")
	}
	// Parts that are not markers stay: no number after the word.
	want(t, clean(t,
		row{pos: 1, id: 1, title: "Whole Book", users: 5000, year: 2000},
		row{pos: 1.5, id: 2, title: "Parts Unknown and Tomorrow", users: 400, year: 2001},
	), "1:Whole Book | 1.5:Parts Unknown and Tomorrow")
}

// A marked duplicate at the integer position of the real book must not beat it,
// even with more readers; marked books alone at their position (serials) stay.
func TestCleanMarkedDuplicateAtIntegerPosition(t *testing.T) {
	want(t, clean(t,
		row{pos: 1, id: 1, title: "The Final Empire", users: 800, year: 2006},
		row{pos: 1, id: 2, title: "The Final Empire (3 of 3)", users: 900, year: 2006},
		row{pos: 1.3, id: 3, title: "The Final Empire (1 of 3)", users: 70, year: 2006},
		row{pos: 2, id: 4, title: "The Well of Ascension", users: 700, year: 2007},
		row{pos: 2.1, id: 5, title: "The Well of Ascension, Part 1", users: 60, year: 2007},
	), "1:The Final Empire | 2:The Well of Ascension")

	want(t, clean(t,
		row{pos: 1, id: 1, title: "The Green Mile, Part 1: The Two Dead Girls", users: 500, year: 1996, details: "1, Part 1"},
		row{pos: 2, id: 2, title: "The Green Mile, Part 2: The Mouse on the Mile", users: 450, year: 1996, details: "2, Part 2"},
		row{pos: 3, id: 3, title: "The Green Mile, Part 3: Coffey's Hands", users: 440, year: 1996, partial: true},
	), "1:The Green Mile, Part 1: The Two Dead Girls | 2:The Green Mile, Part 2: The Mouse on the Mile | 3:The Green Mile, Part 3: Coffey's Hands")
}

// Unflagged, unmarked splits: a fractional title repeating the whole book's
// title, and several fractional entries under one floor.
func TestCleanDropsUnmarkedSplits(t *testing.T) {
	want(t, clean(t,
		row{pos: 1, id: 1, title: "Dune", users: 9000, year: 1965},
		row{pos: 1.2, id: 2, title: "Dune: Special Collector Edition", users: 300, year: 1965},
		row{pos: 3, id: 3, title: "A Storm of Swords", users: 5000, year: 2000},
		row{pos: 3.3, id: 4, title: "Les noces pourpres", users: 300, year: 2000},
		row{pos: 3.4, id: 5, title: "La loi du régicide", users: 280, year: 2000},
		row{pos: 4, id: 6, title: "A Feast for Crows", users: 4000, year: 2005},
		row{pos: 4.5, id: 7, title: "A Lone Novella", users: 1200, year: 2006},
	), "1:Dune | 3:A Storm of Swords | 4:A Feast for Crows | 4.5:A Lone Novella")

	// With no integer book at the floor there is nothing to be a split of.
	want(t, clean(t,
		row{pos: 1.1, id: 1, title: "Foo, Part 1", users: 300, year: 2000},
		row{pos: 1.2, id: 2, title: "Foo, Part 2", users: 280, year: 2000},
	), "1.1:Foo, Part 1 | 1.2:Foo, Part 2")
}

func TestCleanDropsPlaceholdersTranslationsAndBoxSets(t *testing.T) {
	want(t, clean(t,
		row{pos: 5, id: 1, title: "Wind and Truth", users: 3900, year: 2024},
		row{pos: 6, id: 2, title: "Untitled Stormlight Archive #6", users: 265, year: 2031},
		row{pos: 7, id: 3, title: "TBA", users: 200, year: 2032},
		row{pos: 8, id: 4, title: "Book 8", users: 200, year: 2033},
		row{pos: 5, id: 5, title: "Vento e verità", users: 10, year: 2024, canon: 1},
		row{pos: 1, id: 6, title: "Series, Books 1-4", users: 40, year: 2017, details: "1-4"},
		row{pos: 1, id: 7, title: "Series Boxed Set", users: 40, year: 2017, comp: true},
		row{pos: 1, id: 8, title: "Series Omnibus", users: 40, year: 2017},
	), "5:Wind and Truth")
}

// Foreign editions with no canonical_id: another script, a language other
// than the series' with a local edition competing. A foreign-looking entry
// that nobody local competes with stays: edition languages are noisy.
func TestCleanDropsForeignEditions(t *testing.T) {
	want(t, clean(t,
		row{pos: 3, id: 1, title: "Mort", users: 2500, year: 1987, langs: "English,English,English"},
		row{pos: 3, id: 2, title: "Gevatter Tod / Das Erbe des Zauberers", users: 1, year: 1987, langs: "German,German"},
		row{pos: 3, id: 3, title: "Das Erbe des Zauberers", users: 1, year: 1987, langs: "German"},
		row{pos: 8, id: 4, title: "Guards! Guards!", users: 3100, year: 1989, langs: "English"},
		row{pos: 8, id: 5, title: "Стража! Стража! К оружию!", users: 2, year: 1989},
	), "3:Mort | 8:Guards! Guards!")

	// The real, most-read edition lists only Dutch and German default
	// editions; the only English-looking rival is an empty stub.
	want(t, clean(t,
		row{pos: 1, id: 1, title: "Imperium", users: 411, year: 2006, langs: "Dutch,German,Dutch"},
		row{pos: 1, id: 2, title: "Cicero Series 1", users: 0, langs: "English"},
		row{pos: 2, id: 3, title: "Conspirata", users: 167, year: 2009, langs: "English"},
		row{pos: 3, id: 4, title: "Dictator", users: 142, year: 2015, langs: "English,Portuguese,English"},
	), "1:Imperium | 2:Conspirata | 3:Dictator")
}

// The compilation flag also marks real books; it only loses to a comparably
// read single book at the same position.
func TestCleanCompilationFlagIsSoft(t *testing.T) {
	want(t, clean(t,
		row{pos: 1, id: 1, title: "Book One", users: 6000, year: 1990},
		row{pos: 1, id: 2, title: "The Complete Collection", users: 90, year: 2016, comp: true},
		row{pos: 6, id: 3, title: "The End of All Things", users: 886, year: 2015, comp: true},
		row{pos: 6, id: 4, title: "The End of All Things: Book 6", users: 20, year: 2015},
	), "1:Book One | 6:The End of All Things")
}

func TestCleanChoosesMostReadPerPosition(t *testing.T) {
	want(t, clean(t,
		row{pos: 1, id: 1, title: "Anniversary Edition", users: 3, year: 2010},
		row{pos: 1, id: 2, title: "Book One", users: 900, year: 2010},
		row{pos: 1, id: 3, title: "Illustrated Book One", users: 40, year: 2012},
		row{pos: 2, id: 4, title: "Book Two", users: 500, year: 2011},
	), "1:Book One | 2:Book Two")
}

// Readership relative to the series' typical book; recent and unreleased
// books are exempt; a series too small or too niche for the ratio is left alone.
func TestCleanReadershipRule(t *testing.T) {
	rows := []row{
		{pos: 4.5, id: 20, title: "Prequel Novella", users: 1500, year: 1993},
		{pos: 5.5, id: 21, title: "Magazine Issue 94", users: 30, year: 1994},
		{pos: 11, id: 22, title: "Just Released Sequel", users: 40, year: 2026},
		{pos: 12, id: 23, title: "Last Year's Sequel", users: 40, year: 2025},
		{pos: 13, id: 24, title: "Announced Sequel", users: 0, year: 2029},
		{pos: 14, id: 25, title: "Announced By Date", users: 0, year: 2026, date: "2026-12-01"},
		{pos: 15, id: 26, title: "Old Obscurity", users: 40, year: 2019},
	}
	for i := 1; i <= 8; i++ {
		rows = append(rows, row{pos: float64(i), id: i, title: fmt.Sprintf("Main %d", i), users: 100000 / i, year: 1990 + i})
	}
	want(t, clean(t, rows...), "1:Main 1 | 2:Main 2 | 3:Main 3 | 4:Main 4 | 4.5:Prequel Novella | 5:Main 5 | 6:Main 6 | 7:Main 7 | 8:Main 8 | "+
		"11:Just Released Sequel | 12:Last Year's Sequel | 13:Announced Sequel | 14:Announced By Date")

	// One blockbuster does not sweep away real, lesser-read sequels.
	want(t, clean(t,
		row{pos: 1, id: 1, title: "Alpha", users: 700000, year: 1965},
		row{pos: 2, id: 2, title: "Beta", users: 20000, year: 1969},
		row{pos: 3, id: 3, title: "Gamma", users: 12000, year: 1999},
		row{pos: 4, id: 4, title: "Delta", users: 8000, year: 2000},
	), "1:Alpha | 2:Beta | 3:Gamma | 4:Delta")

	// Niche series: a handful of readers means nothing.
	want(t, clean(t,
		row{pos: 1, id: 1, title: "A", users: 50, year: 2000},
		row{pos: 2, id: 2, title: "B", users: 1, year: 2001},
		row{pos: 3, id: 3, title: "C", users: 20, year: 2002},
	), "1:A | 2:B | 3:C")
}

func TestRankBookSeriesPutsMetaListsLast(t *testing.T) {
	mk := func(name string, featured bool) rankedSeries {
		return rankedSeries{BookSeries: providers.BookSeries{Series: providers.SeriesInfo{Name: name}}, featured: featured}
	}
	got := rankBookSeries([]rankedSeries{
		mk("Foo Universe (Publication Order)", true),
		mk("Foo Universe (Chronological)", true),
		mk("The Cosmere-like Umbrella", false),
		mk("Foo", true),
		mk("Foo: Era One", false),
	})
	var names []string
	for _, s := range got {
		names = append(names, s.Series.Name)
	}
	want(t, strings.Join(names, " | "), "Foo | The Cosmere-like Umbrella | Foo: Era One | Foo Universe (Publication Order) | Foo Universe (Chronological)")
	for name, meta := range map[string]bool{
		"Dune Universe (Publication Order)": true, "Foo (Chronological Order)": true, "Foo Collection": true,
		"Foo Extended Universe": true, "Discworld - Ankh-Morpork City Watch": false, "Red Rising": false, "Foo: Era One": false,
	} {
		if providers.IsMetaSeries(name) != meta {
			t.Errorf("IsMetaSeries(%q) = %v", name, !meta)
		}
	}
}

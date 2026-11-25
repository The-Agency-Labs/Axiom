package reading

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Parsed is what the linker could read out of a document's title and authors.
type Parsed struct {
	// Query is the cleaned title to search with.
	Query string
	// Titles are the normalized title candidates (see NormalizeTitle), most
	// likely first. A provider title that equals one of them is a title match.
	Titles []string
	// Authors are the document's authors, normalized to "first last".
	Authors []string
	// Guess reads the first filename segment as the author when the
	// document names none; nil otherwise.
	Guess *Parsed
}

var (
	extRe      = regexp.MustCompile(`(?i)\.(epub3?|kepub|mobi|azw3?|kfx|pdf|fb2|djvu|cbz|cbr|txt|docx?)$`)
	groupRe    = regexp.MustCompile(`\([^()]*\)|\[[^\[\]]*\]|\{[^{}]*\}`)
	editionRe  = regexp.MustCompile(`(?i)\b(?:(?:kindle|ebook|e-book|paperback|hardcover|audio|digital|mass market) edition|(?:\d+(?:st|nd|rd|th)|first|second|third|fourth|fifth|revised|updated|expanded|anniversary|deluxe|illustrated|annotated|special|collector'?s|international|new|definitive|complete|original) (?:and \w+ )?edition|unabridged|z-library|z-lib\.org|libgen(?:\.\w+)?|1lib\.\w+|anna'?s archive)\b`)
	seriesNumR = regexp.MustCompile(`(?i)^(.*?\S)\s+(?:#|book\s+|vol\.?\s*|volume\s+)?0*\d{1,3}(?:\.\d)?$`)
	// "Dune 03: Dune Messiah", "Series 2 - Title"
	seriesTitleRe = regexp.MustCompile(`^(.*?\S)\s+0*\d{1,3}\s*[:\-]\s+(\S.*)$`)
	spaceRe       = regexp.MustCompile(`\s+`)
	leadingArtRe  = regexp.MustCompile(`^(?:the|a|an) `)
	segSplitRe    = regexp.MustCompile(`\s+[-\x{2013}\x{2014}]\s+|\s*_\s*-\s*_\s*`)
	authorSplitRe = regexp.MustCompile(`\s*(?:\n|;|\||&|\band\b|\bwith\b)\s*`)
)

// junkWords mark derivative works (study guides, notebooks, summaries) that
// share a title with a real book. A result containing one of these is only
// suggested, never auto-linked, unless the document's title has it too.
var junkWords = []string{
	"study guide", "summary", "summaries", "notebook", "journal", "workbook", "analysis",
	"sparknotes", "cliffsnotes", "companion", "trivia", "quiz", "lined", "diary",
	"boxed set", "box set", "collection", "omnibus", "saga", "trilogy", "book guide", "planner",
}

// foldTable maps Latin letters outside ASCII to their base letters; enough
// for book titles without pulling in x/text.
var foldTable = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'æ': "ae", 'ç': "c", 'ć': "c", 'č': "c", 'ď': "d", 'đ': "d", 'è': "e", 'é': "e", 'ê': "e",
	'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ī': "i", 'ı': "i", 'ł': "l", 'ñ': "n", 'ń': "n", 'ň': "n", 'ò': "o", 'ó': "o", 'ô': "o",
	'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o", 'œ': "oe", 'ř': "r", 'ś': "s", 'š': "s",
	'ş': "s", 'ß': "ss", 'ť': "t", 'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u",
	'ű': "u", 'ý': "y", 'ÿ': "y", 'ź': "z", 'ż': "z", 'ž': "z",
}

// fold lowercases s, strips diacritics and punctuation and collapses space.
// Apostrophes vanish ("hitchhiker's" -> "hitchhikers"); other punctuation
// becomes a space.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’' || r == '`' || r == '.':
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if f, ok := foldTable[r]; ok {
				b.WriteString(f)
			} else {
				b.WriteRune(r)
			}
		default:
			b.WriteByte(' ')
		}
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(b.String(), " "))
}

// stripNoise removes file extensions, bracketed groups and edition wording.
func stripNoise(s string) string {
	s = extRe.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.ReplaceAll(s, "_", " ")
	s = groupRe.ReplaceAllString(s, " ")
	s = editionRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Trim(spaceRe.ReplaceAllString(s, " "), " -:,"))
}

// NormalizeTitle reduces a title to what equality is decided on: noise and
// brackets removed, case, punctuation and diacritics ignored, and a leading
// article dropped.
func NormalizeTitle(s string) string {
	return leadingArtRe.ReplaceAllString(fold(stripNoise(s)), "")
}

// NormalizeAuthor turns "Herbert, Frank" or "Frank Herbert" into "frank herbert".
func NormalizeAuthor(s string) string {
	s = strings.TrimSpace(s)
	if last, first, ok := strings.Cut(s, ","); ok && !strings.Contains(first, ",") {
		if f := strings.TrimSpace(first); f != "" && !isSuffix(f) {
			s = f + " " + strings.TrimSpace(last)
		} else {
			s = strings.TrimSpace(last)
		}
	}
	return fold(strings.ReplaceAll(groupRe.ReplaceAllString(s, " "), ".", " "))
}

func isSuffix(s string) bool {
	switch fold(s) {
	case "jr", "sr", "ii", "iii", "iv", "phd", "md":
		return true
	}
	return false
}

// SplitAuthors splits an authors field ("A\nB", "A & B", "A; B") into normalized names.
func SplitAuthors(s string) []string {
	var out []string
	for _, part := range authorSplitRe.Split(s, -1) {
		if n := NormalizeAuthor(part); n != "" && !unknownAuthors[n] {
			out = append(out, n)
		}
	}
	return out
}

// unknownAuthors are placeholders devices put in the authors field.
var unknownAuthors = map[string]bool{"n a": true, "na": true, "unknown": true, "none": true, "various": true, "anonymous": true}

// stockDocuments are files every device ships with; they are never books
// to link and are ignored on sight.
var stockDocuments = map[string]bool{"koreader quickstart guide": true}

// IsStockDocument reports whether a title is a file that ships with the device.
func IsStockDocument(title string) bool { return stockDocuments[fold(stripNoise(title))] }

// looksLikeAuthor reports whether a filename segment is a "Last, First" name
// or one of the known authors.
func looksLikeAuthor(seg string, known []string) bool {
	n := NormalizeAuthor(seg)
	for _, k := range known {
		if sameAuthor(n, k) {
			return true
		}
	}
	if last, first, ok := strings.Cut(seg, ","); ok && !strings.Contains(first, ",") {
		return wordCount(last) <= 3 && wordCount(first) <= 3 && wordCount(first) > 0
	}
	return false
}

func wordCount(s string) int { return len(strings.Fields(s)) }

// ParseDocument reads a KOReader document's title (often a filename such as
// "Herbert, Frank - Dune 01 - Dune" or "Dune 03: Dune Messiah") and authors.
func ParseDocument(title, authors string) Parsed {
	p := Parsed{Authors: SplitAuthors(authors)}
	clean := stripNoise(title)
	if clean == "" {
		clean = strings.TrimSpace(title)
	}

	segs := segSplitRe.Split(clean, -1)
	for i := range segs {
		segs[i] = strings.TrimSpace(segs[i])
	}
	// Leading "Author - ..." or trailing "... - Author".
	if len(segs) > 1 && looksLikeAuthor(segs[0], p.Authors) {
		if n := NormalizeAuthor(segs[0]); n != "" && !containsStr(p.Authors, n) {
			p.Authors = append(p.Authors, n)
		}
		segs = segs[1:]
	} else if len(segs) > 1 && len(p.Authors) > 0 && looksLikeAuthor(segs[len(segs)-1], p.Authors) {
		segs = segs[:len(segs)-1]
	}
	// Drop "Series NN" segments in front of the title.
	if len(segs) > 1 {
		kept := segs[:0:0]
		for i, s := range segs {
			if i < len(segs)-1 && seriesNumR.MatchString(s) {
				continue
			}
			kept = append(kept, s)
		}
		segs = kept
	}

	p.Query, p.Titles = titleCandidates(segs)
	// Without a known author, "A - B" may still be "Author - Title": offer
	// that reading, to be accepted only if a provider confirms the author.
	if len(p.Authors) == 0 && len(segs) > 1 {
		g := &Parsed{Authors: []string{NormalizeAuthor(segs[0])}}
		g.Query, g.Titles = titleCandidates(segs[1:])
		if g.Authors[0] != "" && len(g.Titles) > 0 {
			p.Guess = g
		}
	}
	return p
}

// titleCandidates returns the search text and normalized title candidates
// of filename segments: the last one (after a "Series NN:" prefix), then
// all of them joined.
func titleCandidates(segs []string) (query string, cands []string) {
	add := func(s string) {
		if n := NormalizeTitle(s); n != "" && !containsStr(cands, n) {
			cands = append(cands, n)
			if query == "" {
				query = strings.TrimSpace(s)
			}
		}
	}
	last := segs[len(segs)-1]
	if m := seriesTitleRe.FindStringSubmatch(last); m != nil {
		add(m[2])
	}
	add(last)
	if len(segs) > 1 {
		add(strings.Join(segs, " "))
	}
	return query, cands
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sameAuthor compares two normalized "first last" names: the last names
// must be equal and the first names equal or one an initial of the other
// ("g r r martin" matches "george r r martin").
func sameAuthor(a, b string) bool {
	ta, tb := strings.Fields(a), strings.Fields(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if a == b {
		return true
	}
	if len(ta) < 2 || len(tb) < 2 {
		return false
	}
	if ta[len(ta)-1] != tb[len(tb)-1] {
		return false
	}
	fa, fb := ta[0], tb[0]
	if fa == fb {
		return true
	}
	if len(fa) == 1 && strings.HasPrefix(fb, fa) {
		return true
	}
	return len(fb) == 1 && strings.HasPrefix(fa, fb)
}

// AuthorsMatch reports whether any document author equals any provider author.
func AuthorsMatch(doc, provider []string) bool {
	for _, d := range doc {
		for _, p := range provider {
			if sameAuthor(d, p) {
				return true
			}
		}
	}
	return false
}

// isJunk reports whether a result title is a derivative work (see junkWords)
// that the document's own title doesn't ask for.
func isJunk(resultTitle string, docTitles []string) bool {
	rt := fold(resultTitle)
	for _, w := range junkWords {
		if containsPhrase(rt, w) {
			asked := false
			for _, dt := range docTitles {
				if containsPhrase(dt, w) {
					asked = true
				}
			}
			if !asked {
				return true
			}
		}
	}
	return false
}

func containsPhrase(s, phrase string) bool {
	return strings.Contains(" "+s+" ", " "+phrase+" ")
}

// TitleScore rates a provider title against the document's candidates: 100
// for an exact match, up to 50 for a close one (word overlap), 0 for none.
// Derivative works (study guides, notebooks) score 0.
func TitleScore(resultTitle string, p Parsed) float64 {
	if isJunk(resultTitle, p.Titles) {
		return 0
	}
	rt := NormalizeTitle(resultTitle)
	best := 0.0
	rw := strings.Fields(rt)
	for _, c := range p.Titles {
		if c == rt {
			return 100
		}
		cw := strings.Fields(c)
		common := 0
		set := map[string]bool{}
		for _, w := range rw {
			set[w] = true
		}
		for _, w := range cw {
			if set[w] {
				common++
			}
		}
		union := len(rw) + len(cw) - common
		if union == 0 {
			continue
		}
		if j := float64(common) / float64(union); j >= 0.4 && 50*j > best {
			best = 50 * j
		}
	}
	return best
}

// Rank orders search results (maps with a "title") by TitleScore, best first,
// keeping the provider's order among equals. Results that score 0 follow the
// others unchanged unless dropZero is set.
func Rank(results []any, p Parsed, dropZero bool) []any {
	type scored struct {
		v any
		s float64
	}
	var all []scored
	for _, r := range results {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		title, _ := m["title"].(string)
		s := TitleScore(title, p)
		if dropZero && s == 0 {
			continue
		}
		all = append(all, scored{r, s})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].s > all[j].s })
	out := make([]any, 0, len(all))
	for _, a := range all {
		out = append(out, a.v)
	}
	return out
}

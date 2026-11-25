package reading

import (
	"regexp"
	"strconv"
	"strings"
)

// Display is how a document is shown: the series (when the title names
// one) and the title, without the author, file extension or edition noise.
type Display struct {
	Title        string
	Series       string
	SeriesNumber float64 // 0 when unknown
}

// String renders "Series N: Title", or just the title.
func (d Display) String() string {
	if d.Series == "" || strings.EqualFold(d.Series, d.Title) && d.SeriesNumber == 0 {
		return d.Title
	}
	n := ""
	if d.SeriesNumber > 0 {
		n = " " + strconv.FormatFloat(d.SeriesNumber, 'f', -1, 64)
	}
	return d.Series + n + ": " + d.Title
}

var (
	// "(The Red Rising Trilogy, Book 2)", "(Dune #3)", "[Mistborn, Vol. 1]"
	seriesGroupRe = regexp.MustCompile(`(?i)[(\[]\s*([^()\[\]]*?\S)\s*(?:,|:)?\s*(?:#|book\s+|vol\.?\s*|volume\s+|no\.?\s*)(\d{1,3}(?:\.\d)?)\s*[)\]]`)
	// "Dune 01", "Red Rising 04", "Mistborn #2", "Expanse Book 3"
	seriesSegRe = regexp.MustCompile(`(?i)^(.*?\S)\s+(?:#|book\s+|vol\.?\s*|volume\s+)?(\d{1,3}(?:\.\d)?)$`)
	// "The Red Rising Trilogy" → "Red Rising"; "The Wheel of Time" is kept.
	seriesKindRe = regexp.MustCompile(`(?i)^(?:the\s+)?(.*?\S)\s+(?:trilogy|series|saga|duology|quartet|quintet|cycle|sequence|chronicles|books|novels)$`)
)

// seriesName drops packaging words, so every book of a series shows the
// same name: "The Red Rising Trilogy" and "Red Rising" are both "Red Rising".
func seriesName(s string) string {
	s = strings.TrimSpace(s)
	if m := seriesKindRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return s
}

// DisplayName reads the series and title out of a KOReader document title
// such as "Herbert, Frank - Dune 02 - Dune Messiah" (→ Dune 2: Dune
// Messiah), "Dune 03: Dune Messiah", or "Golden Son (The Red Rising
// Trilogy, Book 2)". Titles without a series come back cleaned.
func DisplayName(title, authors string) Display {
	var d Display
	if m := seriesGroupRe.FindStringSubmatch(title); m != nil {
		d.Series = strings.TrimSpace(m[1])
		d.SeriesNumber, _ = strconv.ParseFloat(m[2], 64)
	}
	clean := stripNoise(title)
	if clean == "" {
		clean = strings.TrimSpace(title)
	}
	segs := segSplitRe.Split(clean, -1)
	for i := range segs {
		segs[i] = strings.TrimSpace(segs[i])
	}
	known := SplitAuthors(authors)
	if len(segs) > 1 && looksLikeAuthor(segs[0], known) {
		segs = segs[1:]
	} else if len(segs) > 1 && len(known) > 0 && looksLikeAuthor(segs[len(segs)-1], known) {
		segs = segs[:len(segs)-1]
	}
	if len(segs) > 1 {
		kept := segs[:0:0]
		for i, s := range segs {
			if m := seriesSegRe.FindStringSubmatch(s); i < len(segs)-1 && m != nil {
				if d.Series == "" {
					d.Series = m[1]
					d.SeriesNumber, _ = strconv.ParseFloat(m[2], 64)
				}
				continue
			}
			kept = append(kept, s)
		}
		segs = kept
	}
	t := strings.Join(segs, " - ")
	if m := seriesTitleRe.FindStringSubmatch(t); m != nil {
		if d.Series == "" {
			d.Series = strings.TrimSpace(m[1])
			if n := regexp.MustCompile(`(\d{1,3})\s*[:\-]\s+\S`).FindStringSubmatch(t); n != nil {
				d.SeriesNumber, _ = strconv.ParseFloat(n[1], 64)
			}
		}
		t = m[2]
	}
	d.Series = seriesName(d.Series)
	d.Title = strings.TrimSpace(t)
	if d.Title == "" {
		d.Title = strings.TrimSpace(title)
	}
	return d
}

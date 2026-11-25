package reading_test

import (
	"context"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/reading"
	"github.com/the-agency-labs/encompass/server/internal/reading/readingtest"
)

func TestParseDocument(t *testing.T) {
	cases := []struct {
		title, authors string
		wantTitle      string // first candidate
		wantAuthor     string // first author
		notTitle       string // must not be a candidate
	}{
		{"Herbert, Frank - Dune 01 - Dune", "Herbert, Frank", "dune", "frank herbert", "dune 01"},
		{"Herbert, Frank - Dune 02 - Dune Messiah", "Herbert, Frank", "dune messiah", "frank herbert", "dune"},
		{"Dune 03: Dune Messiah", "Frank Herbert", "dune messiah", "frank herbert", "dune"},
		{"Circe", "Madeline Miller", "circe", "madeline miller", ""},
		{"The Hitchhiker's Guide to the Galaxy", "Douglas Adams", "hitchhikers guide to the galaxy", "douglas adams", ""},
		{"Gabriel García Márquez - One Hundred Years of Solitude (Kindle Edition).epub", "", "one hundred years of solitude", "", ""},
		{"Dune (Kindle Edition) [Z-Library]", "Frank Herbert", "dune", "frank herbert", ""},
		{"Foundation, 2nd Edition", "Isaac Asimov", "foundation", "isaac asimov", ""},
		{"Tolkien, J. R. R. - The Hobbit", "", "hobbit", "j r r tolkien", ""},
		{"Hobbit - Tolkien, J.R.R.", "J.R.R. Tolkien", "hobbit", "j r r tolkien", "j r r tolkien"},
	}
	for _, c := range cases {
		p := reading.ParseDocument(c.title, c.authors)
		if len(p.Titles) == 0 || p.Titles[0] != c.wantTitle {
			t.Errorf("%q: titles = %v, want first %q", c.title, p.Titles, c.wantTitle)
		}
		if c.notTitle != "" {
			for _, x := range p.Titles {
				if x == c.notTitle {
					t.Errorf("%q: unexpected candidate %q in %v", c.title, x, p.Titles)
				}
			}
		}
		if c.wantAuthor != "" && (len(p.Authors) == 0 || p.Authors[0] != c.wantAuthor) {
			t.Errorf("%q: authors = %v, want %q", c.title, p.Authors, c.wantAuthor)
		}
	}
}

func TestPlaceholderAuthorsAreIgnored(t *testing.T) {
	for _, a := range []string{"N/A", "Unknown", ""} {
		if got := reading.ParseDocument("Dune", a).Authors; len(got) != 0 {
			t.Errorf("authors %q parsed as %v", a, got)
		}
	}
	if !reading.IsStockDocument("KOReader Quickstart Guide.epub") || reading.IsStockDocument("Dune") {
		t.Error("stock document detection")
	}
}

func TestNormalizeTitle(t *testing.T) {
	same := [][2]string{
		{"The Hitchhiker's Guide to the Galaxy", "Hitchhikers Guide To The Galaxy"},
		{"Les Misérables", "les miserables"},
		{"Dune (Dune Chronicles, #1)", "DUNE"},
		{"Neuromancer: Deluxe Edition", "Neuromancer"},
	}
	for _, c := range same {
		if reading.NormalizeTitle(c[0]) != reading.NormalizeTitle(c[1]) {
			t.Errorf("%q != %q (%q vs %q)", c[0], c[1], reading.NormalizeTitle(c[0]), reading.NormalizeTitle(c[1]))
		}
	}
	if reading.NormalizeTitle("Dune") == reading.NormalizeTitle("Dune Messiah") {
		t.Error("Dune == Dune Messiah")
	}
}

// linkCase runs FindMatch for a document against books a fake Hardcover knows.
func findMatch(t *testing.T, title, authors string, books ...readingtest.Book) (reading.Match, bool) {
	t.Helper()
	env := readingtest.NewEnv(t, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	for _, b := range books {
		env.HC.Add(b)
	}
	doc := db.ReadingDocument{ID: 1, UserID: env.User, Title: title, Authors: authors}
	m, ok, err := env.Reading.FindMatch(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	return m, ok
}

func TestAutoLinkAcceptsHighConfidenceMatches(t *testing.T) {
	m, ok := findMatch(t, "Herbert, Frank - Dune 01 - Dune", "Herbert, Frank",
		readingtest.Book{ID: "3", Title: "God Emperor of Dune", Author: "Frank Herbert"},
		readingtest.Book{ID: "1", Title: "Dune", Author: "Frank Herbert", Pages: 600},
	)
	if !ok || m.MediaID != "1" {
		t.Errorf("Dune: %+v %v", m, ok)
	}
	m, ok = findMatch(t, "Dune 03: Dune Messiah", "Frank Herbert",
		readingtest.Book{ID: "4", Title: "The Battle of Corrin", Author: "Brian Herbert"},
		readingtest.Book{ID: "2", Title: "Dune Messiah", Author: "Frank Herbert"},
	)
	if !ok || m.MediaID != "2" {
		t.Errorf("Dune Messiah: %+v %v", m, ok)
	}
	m, ok = findMatch(t, "Gabriel Garcia Marquez - One Hundred Years of Solitude (Kindle Edition).epub", "",
		readingtest.Book{ID: "9", Title: "One Hundred Years of Solitude", Author: "Gabriel García Márquez"},
	)
	if !ok || m.MediaID != "9" {
		t.Errorf("diacritics: %+v %v", m, ok)
	}
	m, ok = findMatch(t, "The Lord of the Rings", "J. R. R. Tolkien",
		readingtest.Book{ID: "7", Title: "The Lord of the Rings", Author: "John Ronald Reuel Tolkien"},
	)
	if !ok || m.MediaID != "7" {
		t.Errorf("initials: %+v %v", m, ok)
	}
}

func TestAutoLinkRejectsKnownBadMatches(t *testing.T) {
	bad := []struct {
		name, title, authors string
		books                []readingtest.Book
	}{
		{"Dune 01 vs God Emperor", "Herbert, Frank - Dune 01 - Dune", "Herbert, Frank", []readingtest.Book{
			{ID: "3", Title: "God Emperor of Dune", Author: "Frank Herbert"},
			{ID: "5", Title: "Dune Saga Collection", Author: "Frank Herbert"},
			{ID: "6", Title: "Dreamer of Dune", Author: "Brian Herbert"},
		}},
		{"Dune Messiah vs Battle of Corrin", "Dune 03: Dune Messiah", "Frank Herbert", []readingtest.Book{
			{ID: "4", Title: "The Battle of Corrin", Author: "Brian Herbert"},
			{ID: "3", Title: "God Emperor of Dune", Author: "Frank Herbert"},
		}},
		{"Dune by someone else", "Dune", "Frank Herbert", []readingtest.Book{
			{ID: "8", Title: "Dune", Author: "Somebody Else"},
		}},
		{"Circe vs notebook", "Circe", "Madeline Miller", []readingtest.Book{
			{ID: "1", Title: "Circe Notebook", Author: "Madeline Miller"},
			{ID: "2", Title: "Circe", Author: "Notebooks and Journals Press"},
			{ID: "3", Title: "Circe: Lined Journal", Author: "Madeline Miller"},
		}},
		{"I Am Legend vs study guide", "I Am Legend", "Richard Matheson", []readingtest.Book{
			{ID: "1", Title: "I Am Legend Study Guide", Author: "Richard Matheson"},
			{ID: "2", Title: "Summary of I Am Legend", Author: "Richard Matheson"},
			{ID: "3", Title: "I Am Legend", Author: "SuperSummary"},
		}},
		{"Hitchhiker's vs And Another Thing", "The Hitchhiker's Guide to the Galaxy", "Douglas Adams", []readingtest.Book{
			{ID: "1", Title: "And Another Thing... (Hitchhiker's Guide to the Galaxy, #6)", Author: "Eoin Colfer"},
			{ID: "2", Title: "The Hitchhiker's Guide to the Galaxy: The Illustrated Edition Study Guide", Author: "Douglas Adams"},
		}},
		{"no author known", "Dune", "", []readingtest.Book{
			{ID: "1", Title: "Dune", Author: "Frank Herbert"},
		}},
	}
	for _, c := range bad {
		if m, ok := findMatch(t, c.title, c.authors, c.books...); ok {
			t.Errorf("%s: auto-linked to %+v", c.name, m)
		}
	}
}

func TestSuggestionsRankExactTitleFirstAndDemoteStudyGuides(t *testing.T) {
	env := readingtest.NewEnv(t, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	env.HC.Add(readingtest.Book{ID: "3", Title: "God Emperor of Dune", Author: "Frank Herbert"}).
		Add(readingtest.Book{ID: "9", Title: "Dune Study Guide", Author: "SuperSummary"}).
		Add(readingtest.Book{ID: "1", Title: "Dune", Author: "Frank Herbert"})
	doc := db.ReadingDocument{ID: 1, UserID: env.User, Title: "Herbert, Frank - Dune 01 - Dune", Authors: "Herbert, Frank"}
	got, err := env.Reading.Suggestions(context.Background(), doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("suggestions = %v", got)
	}
	first := got[0].(map[string]any)
	last := got[2].(map[string]any)
	if first["title"] != "Dune" || last["title"] != "Dune Study Guide" {
		t.Errorf("order = %v", got)
	}
	top, err := env.Reading.TopSuggestions(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range top {
		if r.(map[string]any)["title"] == "Dune Study Guide" {
			t.Errorf("study guide in top suggestions: %v", top)
		}
	}
}

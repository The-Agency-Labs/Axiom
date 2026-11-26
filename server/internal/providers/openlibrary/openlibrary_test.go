package openlibrary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newClient(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Axiom/1.0") {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		file, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(fixture(t, file))
	}))
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{})
	c.Root = srv.URL
	return c
}

func norm(t *testing.T, v any) any {
	t.Helper()
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestSearch(t *testing.T) {
	c := newClient(t, map[string]string{"/search.json": "search.json"})
	page, err := c.Search(context.Background(), "dune", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"page": float64(1), "total_results": float64(50), "total_pages": float64(50/24 + 1),
		"results": []any{
			map[string]any{"media_id": "OL7500941M", "source": "openlibrary", "media_type": "book", "title": "Dune", "image": "https://covers.openlibrary.org/b/id/123-L.jpg"},
			map[string]any{"media_id": "OL2M", "source": "openlibrary", "media_type": "book", "title": "Messiah de Dune: Dune Messiah", "image": ImgNone},
		},
	}
	if got := norm(t, page); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestBook(t *testing.T) {
	c := newClient(t, map[string]string{
		"/books/OL1M.json":          "edition.json",
		"/works/OL9W.json":          "work.json",
		"/authors/OL1A.json":        "author1.json",
		"/works/OL9W/editions.json": "editions.json",
		"/works/OL9W/ratings.json":  "ratings.json",
	})
	m, err := c.Book(context.Background(), "OL1M")
	if err != nil {
		t.Fatal(err)
	}
	d := m["details"].(map[string]any)
	checks := map[string]any{
		"title": "Test Book", "synopsis": "A bold story&more. Next", "image": "https://covers.openlibrary.org/b/id/555-L.jpg",
		"max_progress": float64(216), "score": 8.2, "score_count": float64(10),
		"source_url": "https://openlibrary.org/books/OL1M",
	}
	got := norm(t, m).(map[string]any)
	for k, v := range checks {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if !reflect.DeepEqual(m["genres"], []any{"a", "b", "c", "d", "e"}) {
		t.Errorf("genres = %v", m["genres"])
	}
	if d["physical_format"] != "Mass Market Paperback" || d["publish_date"] != "2001-03-18" {
		t.Errorf("details = %v", d)
	}
	if !reflect.DeepEqual(d["publishers"], []any{"A", "B", "C", "D", "E"}) || !reflect.DeepEqual(d["isbn"], []any{"978", "123"}) {
		t.Errorf("details = %v", d)
	}
	// OL2A 404s: skipped, the rest kept.
	if !reflect.DeepEqual(d["author"], []any{"Jane Author"}) {
		t.Errorf("author = %v", d["author"])
	}
	eds := norm(t, m["related"].(map[string]any)["other_editions"])
	wantEds := []any{map[string]any{"source": "openlibrary", "source_url": "https://openlibrary.org/books/OL2M", "media_id": "OL2M", "media_type": "book", "title": "Other", "image": "https://covers.openlibrary.org/b/id/7-L.jpg"}}
	if !reflect.DeepEqual(eds, wantEds) {
		t.Errorf("editions = %v", eds)
	}
	wantTOC := []any{
		map[string]any{"title": "Part One", "level": float64(0)},
		map[string]any{"title": "Chapter 1", "level": float64(1)},
		map[string]any{"title": "2", "level": float64(1)},
		map[string]any{"title": "Epilogue", "level": float64(0)},
	}
	if !reflect.DeepEqual(got["table_of_contents"], wantTOC) {
		t.Errorf("toc = %v", got["table_of_contents"])
	}
	toc := TOCFromMetadata(m)
	if len(toc) != 4 || toc[1] != (TOCEntry{"Chapter 1", 1}) {
		t.Errorf("TOCFromMetadata = %v", toc)
	}
}

func TestBookWithoutWorkOrTOC(t *testing.T) {
	c := newClient(t, map[string]string{"/books/OL5M.json": "bare_edition.json", "/works/OL5M/editions.json": "editions.json"})
	m, err := c.Book(context.Background(), "OL5M")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["table_of_contents"]; ok {
		t.Error("unexpected table_of_contents")
	}
	if m["synopsis"] != "No synopsis available." || m["score"] != nil || m["score_count"] != nil || m["genres"] != nil || m["image"] != ImgNone {
		t.Errorf("defaults wrong: %v", m)
	}
}

func TestBookNotFound(t *testing.T) {
	c := newClient(t, nil)
	_, err := c.Book(context.Background(), "OL404M")
	if !providers.NotFound(err) || !strings.Contains(err.Error(), "Open Library API (HTTP 404)") {
		t.Fatalf("err = %v", err)
	}
}

func TestHelpers(t *testing.T) {
	if ExtractID("/works/OL123W/") != "OL123W" || ExtractID("") != "" {
		t.Error("ExtractID")
	}
	if pyTitle("it's a 3d book") != "It'S A 3D Book" {
		t.Errorf("pyTitle = %q", pyTitle("it's a 3d book"))
	}
	for in, want := range map[string]any{
		"Oct 01, 2017": "2017-10-01", "18 March 2025": "2025-03-18", "1999": "1999", "cop. 2001": "2001",
	} {
		if got := publishDate(map[string]any{"publish_date": in}); got != want {
			t.Errorf("publishDate(%q) = %v, want %v", in, got, want)
		}
	}
	if d := Description(map[string]any{"description": "From edition"}, map[string]any{"description": "work"}); d != "From edition" {
		t.Errorf("Description = %q", d)
	}
}

package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func server(t *testing.T, status int, body []byte, seen *map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if seen != nil {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, seen)
		}
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{HardcoverKey: "tok"})
	c.URL = srv.URL
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
	var sent map[string]any
	c := server(t, 200, fixture(t, "search.json"), &sent)
	page, err := c.Search(context.Background(), "Dune", 2)
	if err != nil {
		t.Fatal(err)
	}
	vars := sent["variables"].(map[string]any)
	if vars["query"] != "Dune" || vars["per_page"] != float64(24) || vars["page"] != float64(2) {
		t.Errorf("variables = %v", vars)
	}
	want := map[string]any{
		"page": float64(2), "total_results": float64(937), "total_pages": float64(937/24 + 1),
		"results": []any{
			map[string]any{"media_id": "312460", "source": "hardcover", "media_type": "book", "title": "Dune", "image": "https://assets.example/dune.jpg"},
			map[string]any{"media_id": "2836392", "source": "hardcover", "media_type": "book", "title": "No Cover", "image": ImgNone},
		},
	}
	if got := norm(t, page); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestBook(t *testing.T) {
	c := server(t, 200, fixture(t, "book.json"), nil)
	m, err := c.Book(context.Background(), "312460")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"media_id": float64(312460), "source": "hardcover", "source_url": "https://hardcover.app/books/dune",
		"media_type": "book", "title": "Dune", "max_progress": float64(617),
		"image": "https://assets.example/dune.jpg", "synopsis": "Set on the desert planet Arrakis.",
		"genres": []any{"Science fiction", "Fantasy"}, "score": 8.5, "score_count": float64(12345),
		"details": map[string]any{
			"format": "Unknown", "number_of_pages": float64(617), "publish_date": "1965-08-01",
			"author": "Frank Herbert", "publisher": "Ace", "isbn": []any{"0441172717", "9780441172719"},
		},
	}
	if got := norm(t, m); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if _, ok := m["table_of_contents"]; ok {
		t.Error("hardcover has no table of contents")
	}
}

func TestBookSparse(t *testing.T) {
	c := server(t, 200, fixture(t, "bare.json"), nil)
	m, err := c.Book(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	if m["synopsis"] != "No synopsis available." || m["image"] != ImgNone || m["genres"] != nil || m["score"] != nil || m["score_count"] != nil {
		t.Errorf("sparse defaults wrong: %v", m)
	}
	d := m["details"].(map[string]any)
	if d["publish_date"] != "2001" || d["format"] != nil || d["isbn"] != nil || d["publisher"] != nil {
		t.Errorf("details = %v", d)
	}
}

func TestBookNotFound(t *testing.T) {
	c := server(t, 200, []byte(`{"data":{"books_by_pk":null}}`), nil)
	_, err := c.Book(context.Background(), "9")
	want := "There was an error contacting the Hardcover API (HTTP 404): No result found with ID 9. Check the logs for more details."
	if err == nil || err.Error() != want || !providers.NotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestGraphQLErrorAndUnauthorized(t *testing.T) {
	c := server(t, 200, []byte(`{"errors":[{"message":"bad field"}],"data":null}`), nil)
	_, err := c.Book(context.Background(), "9")
	if err == nil || !strings.Contains(err.Error(), "(HTTP 200): bad field.") {
		t.Fatalf("err = %v", err)
	}
	c = server(t, 401, fixture(t, "unauthorized.json"), nil)
	_, err = c.Search(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "(HTTP 401): Unable to verify token.") {
		t.Fatalf("err = %v", err)
	}
}

func TestCapSearchQuery(t *testing.T) {
	long := strings.Repeat("abcd ", 20)
	cases := map[string]string{
		"short":                   "short",
		strings.Repeat("a", 60):   strings.Repeat("a", 50),
		long:                      strings.TrimSpace(long[:50]),
		strings.Repeat("ab ", 20): strings.TrimSpace(strings.Repeat("ab ", 20)[:50]),
	}
	for in, want := range cases {
		if got := CapSearchQuery(in); got != want {
			t.Errorf("CapSearchQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRating(t *testing.T) {
	if Rating(0) != nil || Rating(4.27) != 8.5 || Rating(3.333) != 6.7 {
		t.Errorf("Rating wrong: %v %v %v", Rating(0), Rating(4.27), Rating(3.333))
	}
}

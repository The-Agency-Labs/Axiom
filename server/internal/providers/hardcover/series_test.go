package hardcover

import (
	"context"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/providers"
)

func TestBookSeries(t *testing.T) {
	var sent map[string]any
	c := server(t, 200, fixture(t, "book_series.json"), &sent)
	got, err := c.BookSeries(context.Background(), "102")
	if err != nil {
		t.Fatal(err)
	}
	if vars := sent["variables"].(map[string]any); vars["book_id"].(float64) != 102 {
		t.Fatalf("variables = %v", vars)
	}
	if len(got) != 1 || got[0].Position != 2 {
		t.Fatalf("book series = %+v", got)
	}
	s := got[0].Series
	if s.ExternalID != "55" || s.Name != "The Red Rising Trilogy" || s.BooksCount != 4 {
		t.Fatalf("series = %+v", s)
	}
	want := []providers.SeriesBook{
		{Position: 1, ExternalID: "101", Title: "Red Rising", Image: "https://assets.example/rr.jpg", Year: 2014},
		{Position: 2, ExternalID: "102", Title: "Golden Son"},
	}
	if len(s.Books) != 2 || s.Books[0] != want[0] || s.Books[1] != want[1] {
		t.Fatalf("books (unpositioned dropped) = %+v", s.Books)
	}
}

func TestBookSeriesMissingBook(t *testing.T) {
	c := server(t, 200, []byte(`{"data":{"books_by_pk":null}}`), nil)
	if _, err := c.BookSeries(context.Background(), "1"); !providers.NotFound(err) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.BookSeries(context.Background(), "abc"); !providers.NotFound(err) {
		t.Fatalf("non-numeric id: %v", err)
	}
}

func TestSeries(t *testing.T) {
	body := []byte(`{"data":{"series_by_pk":{"id":55,"name":"Red Rising","slug":"rr","books_count":1,"book_series":[
		{"position":1.5,"book":{"id":5,"title":"Novella","cached_image":"u","release_year":2020}}]}}}`)
	c := server(t, 200, body, nil)
	s, err := c.Series(context.Background(), "55")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Red Rising" || len(s.Books) != 1 || s.Books[0].Position != 1.5 || s.Books[0].Year != 2020 {
		t.Fatalf("series = %+v", s)
	}
	c = server(t, 200, []byte(`{"data":{"series_by_pk":null}}`), nil)
	if _, err := c.Series(context.Background(), "1"); !providers.NotFound(err) {
		t.Fatalf("missing series: %v", err)
	}
}

func TestSearchSeries(t *testing.T) {
	var sent map[string]any
	c := server(t, 200, fixture(t, "series_search.json"), &sent)
	hits, err := c.SearchSeries(context.Background(), "red")
	if err != nil {
		t.Fatal(err)
	}
	if sent["variables"].(map[string]any)["query"] != "red" {
		t.Fatalf("variables = %v", sent["variables"])
	}
	if len(hits) != 2 || hits[0] != (providers.SeriesHit{ExternalID: "55", Name: "Red Rising", BooksCount: 6}) || hits[1].ExternalID != "77" {
		t.Fatalf("hits = %+v", hits)
	}
}

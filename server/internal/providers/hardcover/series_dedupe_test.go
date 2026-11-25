package hardcover

import (
	"encoding/json"
	"testing"
)

// Hardcover lists translations, split editions and compilations as books of
// a series; only one canonical book per position is kept (the most read).
func TestSeriesInfoKeepsOneCanonicalBookPerPosition(t *testing.T) {
	raw := `{"id": 1, "name": "Saga", "book_series": [
	  {"position": 1, "featured": true,  "book": {"id": 10, "title": "Translation", "canonical_id": 11, "users_count": 1}},
	  {"position": 1, "featured": true,  "book": {"id": 11, "title": "Book One", "users_count": 900}},
	  {"position": 1, "featured": true,  "book": {"id": 12, "title": "Anniversary Edition", "users_count": 3}},
	  {"position": 2, "featured": true,  "book": {"id": 20, "title": "Book Two", "users_count": 500}},
	  {"position": 2, "featured": false, "book": {"id": 21, "title": "Book Two - Part 2", "users_count": 11}},
	  {"position": 2.1, "featured": false, "book": {"id": 22, "title": "Book Two - Part 1", "users_count": 6}},
	  {"position": 3, "featured": true, "compilation": true, "book": {"id": 30, "title": "Box Set", "users_count": 50}},
	  {"position": 3, "book": {"id": 31, "title": "Book Three", "users_count": 40}}
	]}`
	var d seriesData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	got := d.info().Books
	want := []string{"Book One", "Book Two", "Book Three"}
	if len(got) != len(want) {
		t.Fatalf("books = %+v", got)
	}
	for i, w := range want {
		if got[i].Title != w {
			t.Errorf("book %d = %q, want %q", i, got[i].Title, w)
		}
	}
}

package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/reading"
)

// seriesEnv is a library env whose Hardcover knows the Red Rising trilogy
// (plus a fourth book nobody tracks) and a second, remote-only series.
func newSeriesEnv(t *testing.T) (*libraryEnv, *catalogtest.Hardcover) {
	t.Helper()
	le := newLibraryEnv(t)
	hc := catalogtest.NewHardcover()
	hc.AddBook("101", "Red Rising").AddBook("102", "Golden Son").AddBook("103", "Morning Star")
	hc.AddSeries("55", "The Red Rising Trilogy",
		catalogtest.SeriesBook(1, "101", "Red Rising"), catalogtest.SeriesBook(2, "102", "Golden Son"),
		catalogtest.SeriesBook(3, "103", "Morning Star"), catalogtest.SeriesBook(4, "104", "Iron Gold"))
	hc.AddSeries("77", "Red Sister", catalogtest.SeriesBook(1, "201", "Red Sister"))
	le.env.Catalog.Reg.P.Hardcover = hc
	return le, hc
}

func (le *libraryEnv) track(id, status string) map[string]any {
	le.t.Helper()
	code, body := le.post("/api/v1/library", map[string]any{"media_type": "book", "source": "hardcover", "media_id": id, "status": status})
	return le.expect(code, 200, body, "track "+id)
}

func TestSeriesAuth(t *testing.T) {
	le, _ := newSeriesEnv(t)
	for _, p := range []string{"/api/v1/series", "/api/v1/series/search?q=red", "/api/v1/series/1", "/api/v1/series/hardcover/55"} {
		if code, _ := le.call("", "GET", p, nil); code != 401 {
			t.Errorf("anonymous GET %s: %d", p, code)
		}
		if code, _ := le.call(le.read, "GET", p, nil); code == 401 || code == 403 {
			t.Errorf("read token GET %s: %d", p, code)
		}
	}
}

func TestSeriesFlow(t *testing.T) {
	le, _ := newSeriesEnv(t)
	le.track("101", "Completed")
	le.track("102", "In progress")

	// The viewer's series: counts and the next unread book.
	code, body := le.get("/api/v1/series")
	list := libraryList(le.expect(code, 200, body, "series")["results"])
	if len(list) != 1 {
		t.Fatalf("series list: %v", body)
	}
	row := libraryMap(list[0])
	if row["name"] != "Red Rising" || libraryNum(row["tracked"]) != 2 || libraryNum(row["completed"]) != 1 || libraryNum(row["total"]) != 4 {
		t.Fatalf("series row: %v", row)
	}
	next := libraryMap(row["next"])
	if next["title"] != "Golden Son" || libraryNum(next["position"]) != 2 || next["status"] != "In progress" || next["media_id"] != "102" {
		t.Fatalf("next unread: %v", next)
	}
	sid := int(row["id"].(float64))

	// Finishing book 2 moves "next" on to the untracked third book.
	le.track("102", "Completed")
	_, body = le.get("/api/v1/series")
	next = libraryMap(libraryMap(libraryList(libraryMap(body)["results"])[0])["next"])
	if next["title"] != "Morning Star" || next["status"] != nil || next["item_id"] != nil {
		t.Fatalf("next after completing: %v", next)
	}

	// The series page: every book in order, gaps included, with the viewer's entry.
	code, body = le.get("/api/v1/series/" + strconv.Itoa(sid))
	page := le.expect(code, 200, body, "series page")
	if page["name"] != "Red Rising" || libraryNum(page["books_count"]) != 4 {
		t.Fatalf("page: %v", page)
	}
	books := libraryList(page["books"])
	if len(books) != 4 {
		t.Fatalf("books: %v", books)
	}
	for i, want := range []struct {
		title, status string
		tracked       bool
	}{{"Red Rising", "Completed", true}, {"Golden Son", "Completed", true}, {"Morning Star", "", false}, {"Iron Gold", "", false}} {
		b := libraryMap(books[i])
		if b["title"] != want.title || libraryNum(b["position"]) != i+1 || b["media_type"] != "book" || b["source"] != "hardcover" {
			t.Fatalf("book %d: %v", i, b)
		}
		entry := libraryMap(b["entry"])
		if want.tracked {
			if entry == nil || entry["status"] != want.status || b["item_id"] == nil {
				t.Fatalf("book %d entry: %v", i, b)
			}
		} else if b["entry"] != nil || b["item_id"] != nil {
			t.Fatalf("untracked book %d has link/entry: %v", i, b)
		}
	}

	// Another user sees the same series with no entries, and has no series of their own.
	code, body = le.call(le.other, "GET", "/api/v1/series/"+strconv.Itoa(sid), nil)
	for _, b := range libraryList(le.expect(code, 200, body, "other page")["books"]) {
		if libraryMap(b)["entry"] != nil {
			t.Fatalf("entry leaked to another user: %v", b)
		}
	}
	_, body = le.call(le.other, "GET", "/api/v1/series", nil)
	if n := len(libraryList(libraryMap(body)["results"])); n != 0 {
		t.Fatalf("other user's series: %v", body)
	}

	if code, _ := le.get("/api/v1/series/9999"); code != 404 {
		t.Errorf("unknown series: %d", code)
	}
	if code, _ := le.get("/api/v1/series/abc"); code != 422 {
		t.Errorf("bad id: %d", code)
	}
}

func TestSeriesInLibraryJSON(t *testing.T) {
	le, _ := newSeriesEnv(t)
	le.env.TMDB.AddMovie("300", "Plain Movie", "2019-05-05")
	entry := le.track("102", "Planning")
	if s := libraryMap(entry["series"]); s["name"] != "Red Rising" || libraryNum(s["position"]) != 2 || s["id"] == nil {
		t.Fatalf("entry series: %v", entry["series"])
	}
	if s := libraryMap(libraryMap(entry["item"])["series"]); s["name"] != "Red Rising" {
		t.Fatalf("item series: %v", entry["item"])
	}
	_, body := le.get("/api/v1/library?media_type=book")
	first := libraryMap(libraryList(libraryMap(body)["results"])[0])
	if libraryMap(first["series"])["name"] != "Red Rising" {
		t.Fatalf("library list entry: %v", first)
	}
	// A standalone book has "series": null; other kinds have no such key.
	le.env.Catalog.Reg.P.Hardcover.(*catalogtest.Hardcover).AddBook("500", "Standalone")
	solo := le.track("500", "Planning")
	if v, ok := solo["series"]; !ok || v != nil {
		t.Fatalf("standalone series: %v (present %v)", v, ok)
	}
	code, body := le.post("/api/v1/library", map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300", "status": "Planning"})
	movie := le.expect(code, 200, body, "movie")
	if _, ok := movie["series"]; ok {
		t.Fatalf("movie entry has series: %v", movie)
	}
}

func TestSeriesSearch(t *testing.T) {
	le, hc := newSeriesEnv(t)
	le.track("101", "Completed")

	// Local (normalised) first, then Hardcover's; a series found by both is listed once.
	code, body := le.get("/api/v1/series/search?q=red")
	res := libraryList(le.expect(code, 200, body, "search")["results"])
	if len(res) != 2 {
		t.Fatalf("results: %v", body)
	}
	local, remote := libraryMap(res[0]), libraryMap(res[1])
	if local["name"] != "Red Rising" || local["id"] == nil || local["source"] != "hardcover" || local["external_id"] != "55" ||
		libraryNum(local["tracked"]) != 1 || libraryNum(local["books_count"]) != 4 {
		t.Fatalf("local result: %v", local)
	}
	if remote["name"] != "Red Sister" || remote["id"] != nil || remote["external_id"] != "77" || libraryNum(remote["tracked"]) != 0 {
		t.Fatalf("remote result: %v", remote)
	}

	// The query is normalised like names: "the red rising saga" finds "Red Rising".
	_, body = le.get("/api/v1/series/search?q=the+red+rising+saga")
	if res := libraryList(libraryMap(body)["results"]); len(res) < 1 || libraryMap(res[0])["name"] != "Red Rising" {
		t.Fatalf("normalised query: %v", body)
	}
	if _, body = le.get("/api/v1/series/search?q="); len(libraryList(libraryMap(body)["results"])) != 0 {
		t.Fatalf("empty query: %v", body)
	}

	// Fetching the remote series stores it; the search then knows its id.
	code, body = le.get("/api/v1/series/hardcover/77")
	page := le.expect(code, 200, body, "fetch remote")
	if page["name"] != "Red Sister" || len(libraryList(page["books"])) != 1 || page["id"] == nil {
		t.Fatalf("fetched series: %v", page)
	}
	_, body = le.get("/api/v1/series/search?q=sister")
	if r := libraryMap(libraryList(libraryMap(body)["results"])[0]); r["id"] != page["id"] {
		t.Fatalf("stored series id missing in search: %v", r)
	}
	if code, _ := le.get("/api/v1/series/hardcover/999"); code != 404 {
		t.Errorf("unknown remote series: %d", code)
	}
	if code, _ := le.get("/api/v1/series/goodreads/1"); code != 400 {
		t.Errorf("unsupported source: %d", code)
	}

	// A Hardcover failure leaves the local results.
	hc.SeriesErr = errors.New("down")
	_, body = le.get("/api/v1/series/search?q=red")
	if res := libraryList(libraryMap(body)["results"]); len(res) < 1 || libraryMap(res[0])["name"] != "Red Rising" {
		t.Fatalf("search with Hardcover down: %v", body)
	}
}

func TestReadingDisplayPrefersStoredSeries(t *testing.T) {
	le, _ := newSeriesEnv(t)
	entry := le.track("102", "Planning")
	itemID := int64(libraryMap(entry["item"])["id"].(float64))
	cache := newReadingItemCache(le.srv.Catalog)
	ctx := context.Background()
	item := sql.NullInt64{Int64: itemID, Valid: true}

	// Unlinked: the Kindle-parsed series, normalised.
	d := cache.documentDict(ctx, reading.Document{Title: "Morning Star (The Red Rising Trilogy, Book 3)"})
	if d["series"] != "Red Rising" || d["display_title"] != "Red Rising 3: Morning Star" {
		t.Fatalf("unlinked: %v", d)
	}
	// Linked: the stored series wins (name and position), whatever the Kindle title says.
	d = cache.documentDict(ctx, reading.Document{Title: "Golden Son", ItemID: item})
	if d["series"] != "Red Rising" || d["series_number"] != 2.0 || d["display_title"] != "Red Rising 2: Golden Son" {
		t.Fatalf("linked, no series in title: %v", d)
	}
	d = cache.documentDict(ctx, reading.Document{Title: "Golden Son (Some Other Name, Book 9)", ItemID: item})
	if d["series"] != "Red Rising" || d["series_number"] != 2.0 {
		t.Fatalf("linked, conflicting title: %v", d)
	}
}

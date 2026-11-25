package mal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

// jsonOf round-trips v so the assertions see what API clients see.
func jsonOf(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func newClient(t *testing.T, h http.HandlerFunc, cfg config.Config) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	if cfg.TimeZone == "" {
		cfg.TimeZone = "UTC"
	}
	c := New(providers.NewClient(nil), cfg)
	c.url = srv.URL
	return c
}

func TestAnimeMetadata(t *testing.T) {
	var gotKey, gotPath, gotFields string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath, gotFields = r.Header.Get("X-MAL-CLIENT-ID"), r.URL.Path, r.URL.Query().Get("fields")
		w.Write(fixture(t, "anime_52991.json"))
	}, config.Config{MALKey: "client-id", TimeZone: "America/New_York"})

	md, err := c.Anime(context.Background(), "52991")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "client-id" || gotPath != "/anime/52991" ||
		!strings.Contains(gotFields, "related_anime") || !strings.Contains(gotFields, "num_episodes") {
		t.Fatalf("key=%q path=%q fields=%q", gotKey, gotPath, gotFields)
	}
	got := jsonOf(t, md)
	details := got["details"].(map[string]any)
	// 2023-09-29 23:00 JST is 10:00 in New York (EDT)
	want := map[string]any{
		"format": "Anime", "start_date": "2023-09-29", "end_date": "2024-03-22", "status": "Finished",
		"episodes": 28.0, "runtime": "26 min", "studios": []any{"Madhouse"}, "season": "Fall 2023",
		"broadcast": "Friday 10:00", "source": "Manga",
	}
	if len(details) != len(want) {
		t.Fatalf("details keys: %v", details)
	}
	for k, v := range want {
		if !jsonEqual(details[k], v) {
			t.Errorf("details[%s] = %v, want %v", k, details[k], v)
		}
	}
	for k, v := range map[string]any{
		"media_id": "52991", "source": "mal", "source_url": "https://myanimelist.net/anime/52991",
		"media_type": "anime", "title": "Sousou no Frieren", "max_progress": 28.0,
		"image":    "https://cdn.myanimelist.net/images/anime/1015/138006l.jpg",
		"synopsis": "The demon king has been defeated.", "genres": []any{"Adventure", "Drama"},
		"score": 9.3, "score_count": 600000.0,
	} {
		if !jsonEqual(got[k], v) {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	related := got["related"].(map[string]any)
	ra := related["related_anime"].([]any)[0].(map[string]any)
	if ra["media_id"] != 59978.0 || ra["media_type"] != "anime" || ra["source"] != "mal" {
		t.Errorf("related_anime %v", ra)
	}
	rec := related["recommendations"].([]any)[0].(map[string]any)
	if rec["image"] != ImgNone {
		t.Errorf("a recommendation without picture must use the placeholder: %v", rec)
	}
}

func TestMangaWithUnknownValues(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manga/160219" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write(fixture(t, "manga_160219.json"))
	}, config.Config{})
	md, err := c.Manga(context.Background(), "160219")
	if err != nil {
		t.Fatal(err)
	}
	got := jsonOf(t, md)
	details := got["details"].(map[string]any)
	if got["synopsis"] != "No synopsis available." || got["max_progress"] != nil || got["genres"] != nil ||
		got["score"] != nil || got["score_count"] != 0.0 || got["image"] != ImgNone {
		t.Errorf("unknown values: %v", got)
	}
	if details["format"] != "One Shot" || details["status"] != "Publishing" ||
		details["number_of_chapters"] != nil || details["start_date"] != "2020-05" || details["end_date"] != nil {
		t.Errorf("details: %v", details)
	}
	related := got["related"].(map[string]any)
	if len(related["related_manga"].([]any)) != 0 {
		t.Errorf("related: %v", related)
	}
}

func TestSearch(t *testing.T) {
	var calls atomic.Int32
	var query map[string][]string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		query = r.URL.Query()
		w.Write(fixture(t, "search.json"))
	}, config.Config{MALNSFW: true})
	page, err := c.Search(context.Background(), "anime", "frieren", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if query["q"][0] != "frieren" || query["limit"][0] != "24" || query["offset"][0] != "24" ||
		query["nsfw"][0] != "true" || query["fields"][0] != "media_type,alternative_titles" {
		t.Fatalf("query %v", query)
	}
	got := jsonOf(t, page)
	if got["page"] != 2.0 || got["total_results"] != 72.0 || got["total_pages"] != 4.0 {
		t.Fatalf("paging %v", got)
	}
	results := got["results"].([]any)
	if len(results) != 2 || results[1].(map[string]any)["image"] != ImgNone ||
		results[0].(map[string]any)["title"] != "Sousou no Frieren" {
		t.Fatalf("results %v", results)
	}
	// adult titles off: no nsfw parameter
	if _, err := c.Search(context.Background(), "anime", "frieren", 2, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := query["nsfw"]; ok {
		t.Fatal("nsfw sent when not allowed")
	}
}

func TestSearchInvalidQueryIsEmpty(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"invalid q","error":"bad_request"}`))
	}, config.Config{})
	page, err := c.Search(context.Background(), "manga", "a", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	got := jsonOf(t, page)
	if len(got["results"].([]any)) != 0 || got["total_results"] != 72.0 {
		t.Fatalf("%v", got)
	}
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"forbidden", 403, `{"message":"forbidden"}`, "There was an error contacting the MyAnimeList API (HTTP 403): API key is missing. Check the logs for more details."},
		{"bad client", 400, `{"message":"Invalid client id"}`, "There was an error contacting the MyAnimeList API (HTTP 400): Invalid API key. Check the logs for more details."},
		{"not found", 404, `{"message":"not_found"}`, "There was an error contacting the MyAnimeList API (HTTP 404). Check the logs for more details."},
		{"html body", 403, `<html>nope</html>`, "There was an error contacting the MyAnimeList API (HTTP 403). Check the logs for more details."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}, config.Config{})
			_, err := c.Anime(context.Background(), "1")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v", err)
			}
			if tc.status == 404 && !providers.NotFound(err) {
				t.Fatal("404 must be NotFound")
			}
		})
	}
}

func TestMetadataDispatch(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "manga_160219.json"))
	}, config.Config{})
	if _, err := c.Metadata(context.Background(), "tv", "1"); err == nil {
		t.Fatal("tv is not a MAL type")
	}
	md, err := c.Metadata(context.Background(), "manga", "160219")
	if err != nil || md["media_type"] != "manga" {
		t.Fatalf("%v %v", md, err)
	}
}

func TestRoundingAndTitles(t *testing.T) {
	if got := round1(8.25); got != 8.2 { // Python: round(8.25, 1) == 8.2
		t.Errorf("round1(8.25) = %v", got)
	}
	if got := round1(7.449); got != 7.4 {
		t.Errorf("round1 = %v", got)
	}
	for in, want := range map[string]string{"light novel": "Light Novel", "4-koma": "4-Koma", "web_novel": "Web_Novel"} {
		if got := pyTitle(in); got != want {
			t.Errorf("pyTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestDisplayTitlesPreferEnglish(t *testing.T) {
	for _, tc := range []struct {
		main, en, title, native string
	}{
		{"Jeonjijeok Dokja Sijeom", "Omniscient Reader", "Omniscient Reader", "Jeonjijeok Dokja Sijeom"},
		{"Shuumatsu no Walküre", "Record of Ragnarok", "Record of Ragnarok", "Shuumatsu no Walküre"},
		{"Blue Lock", "Blue Lock", "Blue Lock", ""}, // same name: no subtitle
		{"Hikaru no Go", "", "Hikaru no Go", ""},    // no English title
	} {
		title, native := displayTitles(tc.main, &altTitles{En: tc.en})
		if title != tc.title || native != tc.native {
			t.Errorf("%q/%q -> %q/%q, want %q/%q", tc.main, tc.en, title, native, tc.title, tc.native)
		}
	}
}

package anilist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/cache"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/db"
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

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// newClient serves handler for every POST; handler sees the decoded request.
func newClient(t *testing.T, cfg config.Config, handler func(w http.ResponseWriter, req gqlRequest)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		handler(w, req)
	}))
	t.Cleanup(srv.Close)
	if cfg.TimeZone == "" {
		cfg.TimeZone = "UTC"
	}
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := New(providers.NewClient(cache.New(db.New(conn), 0)), cfg)
	c.url = srv.URL
	return c
}

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

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestAnimeMetadata(t *testing.T) {
	var vars map[string]any
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		vars = req.Variables
		w.Write(fixture(t, "anime_154587.json"))
	})
	md, err := c.Anime(context.Background(), "154587")
	if err != nil {
		t.Fatal(err)
	}
	if vars["id"] != 154587.0 || vars["type"] != "ANIME" {
		t.Fatalf("variables %v", vars)
	}
	got := jsonOf(t, md)
	details := got["details"].(map[string]any)
	want := map[string]any{
		"format": "TV", "start_date": "2023-09-29", "end_date": "2024-03", "status": "Airing",
		"episodes": 28.0, "runtime": "24 min", "studios": []any{"MADHOUSE"}, "season": "Fall 2023",
		"broadcast": "Tuesday 22:13", "source": "Manga",
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
		"media_id": "154587", "source": "anilist", "source_url": "https://anilist.co/anime/154587",
		"media_type": "anime", "title": "Sousou no Frieren", "max_progress": 28.0,
		"image":    "https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx154587.jpg",
		"synopsis": "The demon king has been defeated.\n\nThe hero party disbands. & so on.",
		"genres":   []any{"Adventure", "Drama"}, "score": 9.1, "score_count": 350.0,
		"external_links": map[string]any{"MyAnimeList": "https://myanimelist.net/anime/52991"},
	} {
		if !jsonEqual(got[k], v) {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	related := got["related"].(map[string]any)
	if n := len(related["related_anime"].([]any)); n != 1 {
		t.Errorf("related_anime has %d entries", n)
	}
	if n := len(related["related_manga"].([]any)); n != 1 {
		t.Errorf("related_manga has %d entries", n)
	}
	recs := related["recommendations"].([]any)
	if len(recs) != 1 || recs[0].(map[string]any)["media_id"] != 21827.0 {
		t.Errorf("recommendations %v (deleted media must be skipped)", recs)
	}
	if first := related["related_manga"].([]any)[0].(map[string]any); first["media_type"] != "manga" || first["source"] != "anilist" {
		t.Errorf("manga card %v", first)
	}
}

func TestMangaMetadataSparse(t *testing.T) {
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		if req.Variables["type"] != "MANGA" {
			t.Errorf("type %v", req.Variables["type"])
		}
		w.Write(fixture(t, "manga_sparse.json"))
	})
	md, err := c.Manga(context.Background(), "30002")
	if err != nil {
		t.Fatal(err)
	}
	got := jsonOf(t, md)
	details := got["details"].(map[string]any)
	if got["synopsis"] != "No synopsis available." || got["genres"] != nil || got["score"] != nil ||
		got["score_count"] != 0.0 || got["max_progress"] != nil || got["image"] != ImgNone {
		t.Errorf("sparse values: %v", got)
	}
	if !jsonEqual(got["external_links"], map[string]any{}) {
		t.Errorf("external_links = %v", got["external_links"])
	}
	if details["format"] != "One Shot" || details["status"] != "Upcoming" || details["start_date"] != "2026" ||
		details["end_date"] != nil || details["number_of_chapters"] != nil || len(details) != 5 {
		t.Errorf("details %v", details)
	}
	if _, ok := got["related"].(map[string]any)["related_manga"]; !ok {
		t.Errorf("related %v", got["related"])
	}
}

func TestNotFoundAndErrorDetails(t *testing.T) {
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errors":[{"message":"Not Found.","status":404}],"data":{"Media":null}}`))
	})
	_, err := c.Anime(context.Background(), "1")
	want := "There was an error contacting the AniList API (HTTP 404): Not Found.. Check the logs for more details."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	if !providers.NotFound(err) {
		t.Fatal("expected NotFound")
	}

	// 200 with a null Media is a not-found too
	c = newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		w.Write([]byte(`{"data":{"Media":null}}`))
	})
	if _, err := c.Manga(context.Background(), "7"); !providers.NotFound(err) {
		t.Fatalf("got %v", err)
	}
	if _, err := c.Manga(context.Background(), "x"); err == nil {
		t.Fatal("non-numeric ids must fail")
	}
}

func TestSearch(t *testing.T) {
	var vars map[string]any
	var calls atomic.Int32
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		calls.Add(1)
		vars = req.Variables
		w.Write(fixture(t, "search.json"))
	})
	page, err := c.Search(context.Background(), "anime", "frieren", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if vars["search"] != "frieren" || vars["type"] != "ANIME" || vars["page"] != 2.0 ||
		vars["perPage"] != 24.0 || vars["isAdult"] != false {
		t.Fatalf("variables %v", vars)
	}
	got := jsonOf(t, page)
	// 1 previous page + 2 results + one more page, like the Python
	if got["page"] != 2.0 || got["total_results"] != 50.0 || got["total_pages"] != 3.0 {
		t.Fatalf("paging %v", got)
	}
	results := got["results"].([]any)
	if len(results) != 2 || results[1].(map[string]any)["image"] != ImgNone ||
		results[0].(map[string]any)["media_type"] != "anime" {
		t.Fatalf("results %v", results)
	}
	// cached: the second identical search does not hit the API
	if _, err := c.Search(context.Background(), "anime", "frieren", 2, true); err != nil || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}

	// with ANILIST_NSFW and adult titles allowed, isAdult is left out
	c = newClient(t, config.Config{AniListNSFW: true}, func(w http.ResponseWriter, req gqlRequest) {
		vars = req.Variables
		w.Write(fixture(t, "search.json"))
	})
	if _, err := c.Search(context.Background(), "manga", "berserk", 1, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := vars["isAdult"]; ok || vars["type"] != "MANGA" {
		t.Fatalf("variables %v", vars)
	}
	if _, err := c.Search(context.Background(), "manga", "berserk", 1, false); err != nil {
		t.Fatal(err)
	}
	if vars["isAdult"] != false {
		t.Fatalf("variables %v", vars)
	}
}

func TestEpisodeMediaAndStreaming(t *testing.T) {
	var vars map[string]any
	var calls atomic.Int32
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		calls.Add(1)
		vars = req.Variables
		w.Write(fixture(t, "episodes.json"))
	})
	m := c.EpisodeMedia(context.Background(), "mal", "52991")
	if _, ok := vars["idMal"]; !ok || len(vars) != 1 {
		t.Fatalf("mal lookup must send idMal only: %v", vars)
	}
	if m.ID != 154587 || m.IDMal != 52991 || m.Episodes != 28 || len(m.StreamingEpisodes) != 5 {
		t.Fatalf("media %+v", m)
	}
	s := m.Streaming()
	if len(s) != 4 {
		t.Fatalf("streaming %v", s)
	}
	if s[1] != (EpisodeInfo{"The Journey's End", "https://img.example/1.jpg"}) {
		t.Errorf("episode 1 (first site wins): %+v", s[1])
	}
	if s[2].Title != "Next Steps" {
		t.Errorf("episode 2: %+v", s[2])
	}
	// no number in the title: position (4th entry) and the full title
	if s[4].Title != "A special without a number" || s[5].Title != "" {
		t.Errorf("episodes 4/5: %+v %+v", s[4], s[5])
	}
	c.EpisodeMedia(context.Background(), "mal", "52991")
	if calls.Load() != 1 {
		t.Errorf("episode media must be cached (calls=%d)", calls.Load())
	}
	c.EpisodeMedia(context.Background(), "anilist", "154587")
	if _, ok := vars["id"]; !ok || len(vars) != 1 {
		t.Errorf("anilist lookup must send id only: %v", vars)
	}
}

func TestEpisodeMediaSwallowsErrors(t *testing.T) {
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errors":[{"message":"Not Found."}]}`))
	})
	m := c.EpisodeMedia(context.Background(), "mal", "1")
	if m.ID != 0 || len(m.Streaming()) != 0 {
		t.Fatalf("%+v", m)
	}
}

func TestCredits(t *testing.T) {
	var pages []float64
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		pages = append(pages, req.Variables["page"].(float64))
		if req.Variables["page"] == 1.0 {
			w.Write(fixture(t, "cast_page1.json"))
			return
		}
		w.Write(fixture(t, "cast_page2.json"))
	})
	credits, err := c.Credits(context.Background(), "mal", "52991")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 { // MaxCharacters/25 pages even when AniList says there are more
		t.Fatalf("pages fetched: %v", pages)
	}
	if len(credits) != 3 {
		t.Fatalf("credits %+v", credits)
	}
	want := VoiceCredit{
		PersonID: 112215, PersonName: "Atsumi Tanezaki", PersonNative: "種﨑敦美",
		PersonImage: "https://img.example/atsumi.jpg", Character: "Frieren",
		CharacterImage: "https://img.example/frieren.png", CharacterRole: "MAIN", Language: "japanese", Order: 0,
	}
	if credits[0] != want {
		t.Errorf("first credit %+v", credits[0])
	}
	if credits[1].Language != "english" || credits[1].PersonNative != "" || credits[1].PersonImage != "" || credits[1].Order != 0 {
		t.Errorf("english actor %+v", credits[1])
	}
	// Fern has no actors but still takes position 1
	if credits[2].Character != "Stark" || credits[2].Order != 2 {
		t.Errorf("page 2 %+v", credits[2])
	}
}

func TestMetadataDispatch(t *testing.T) {
	c := newClient(t, config.Config{}, func(w http.ResponseWriter, req gqlRequest) {
		w.Write(fixture(t, "manga_sparse.json"))
	})
	if _, err := c.Metadata(context.Background(), "movie", "1"); err == nil {
		t.Fatal("movie is not an AniList type")
	}
	md, err := c.Metadata(context.Background(), "manga", "30002")
	if err != nil || md["media_type"] != "manga" || !strings.HasPrefix(md["source_url"].(string), "https://anilist.co/") {
		t.Fatalf("%v %v", md, err)
	}
}

func TestHelpers(t *testing.T) {
	if got := round1(9.05); got != 9.1 && got != 9.0 { // either is a correct rounding of the binary value
		t.Errorf("round1 = %v", got)
	}
	for in, want := range map[string]string{"NOT YET RELEASED": "Not Yet Released", "winter": "Winter", "3d movie": "3D Movie"} {
		if got := pyTitle(in); got != want {
			t.Errorf("pyTitle(%q) = %q, want %q", in, got, want)
		}
	}
	y, m, d := 2020, 3, 5
	for _, tc := range []struct {
		in   fuzzyDate
		want any
	}{
		{fuzzyDate{&y, &m, &d}, "2020-03-05"}, {fuzzyDate{&y, &m, nil}, "2020-03"},
		{fuzzyDate{&y, nil, nil}, "2020"}, {fuzzyDate{}, nil},
	} {
		if got := date(tc.in); got != tc.want {
			t.Errorf("date(%+v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for dur, want := range map[int]any{0: nil, 24: "24 min", 60: "1h 0m", 125: "2h 5m"} {
		d := dur
		if got := runtime(media{Duration: &d}); got != want {
			t.Errorf("runtime(%d) = %v, want %v", dur, got, want)
		}
	}
}

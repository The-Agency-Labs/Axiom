package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

type fake struct {
	mu       sync.Mutex
	requests []*url.URL
	routes   map[string]any // path -> JSON body
	status   map[string]int
}

func (f *fake) calls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, u := range f.requests {
		if u.Path == path {
			n++
		}
	}
	return n
}

func (f *fake) last() *url.URL {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func newClient(t *testing.T, f *fake, cfg config.Config) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL)
		f.mu.Unlock()
		if st, ok := f.status[r.URL.Path]; ok {
			w.WriteHeader(st)
		}
		body, ok := f.routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			body = map[string]any{"status_message": "The resource you requested could not be found."}
		}
		json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	cfg.TMDBKey = "k"
	cfg.TMDBLang = "en"
	c := New(providers.NewClient(nil), cfg)
	c.BaseURL = srv.URL
	return c
}

func get(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %v is not an object", path, cur)
		}
		cur = obj[p]
	}
	return cur
}

func TestTV(t *testing.T) {
	f := &fake{routes: map[string]any{"/tv/42410": fixture(t, "tv_42410.json")}}
	c := newClient(t, f, config.Config{})
	m, err := c.TV(context.Background(), "42410")
	if err != nil {
		t.Fatal(err)
	}
	q := f.last().Query()
	if q.Get("api_key") != "k" || q.Get("language") != "en" || q.Get("append_to_response") != "recommendations,external_ids,watch/providers" {
		t.Errorf("query = %v", q)
	}
	want := map[string]any{
		"source": "tmdb", "media_type": "tv", "title": "Durarara!!",
		"source_url": "https://www.themoviedb.org/tv/42410", "media_id": float64(42410),
		"tvdb_id": float64(133341),
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if m["image"] != "https://image.tmdb.org/t/p/w500/rOVSpNPFr62tFKeFuHgPdOXQx3E.jpg" {
		t.Errorf("image = %v", m["image"])
	}
	if get(t, m, "details", "format") != "TV" || get(t, m, "details", "runtime") != "24m" {
		t.Errorf("details = %v", m["details"])
	}
	links := m["external_links"].(map[string]any)
	if links["IMDb"] != "https://www.imdb.com/title/tt1584000/" || links["TVDB"] != "https://www.thetvdb.com/dereferrer/series/133341" {
		t.Errorf("links = %v", links)
	}
	if _, ok := links["Letterboxd"]; ok {
		t.Error("TV must not have a Letterboxd link")
	}
	seasons := get(t, m, "related", "seasons").([]any)
	s1 := seasons[0].(map[string]any)
	if s1["media_type"] != "season" || s1["media_id"] != float64(42410) || s1["title"] != "Durarara!!" || s1["season_title"] == nil {
		t.Errorf("season related = %v", s1)
	}
	if m["next_episode_season"] != nil {
		t.Errorf("next_episode_season = %v", m["next_episode_season"])
	}
	for _, k := range []string{"score", "score_count", "max_progress", "synopsis", "genres", "providers", "last_episode_season"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
}

func TestMovieCollectionAndRecommendations(t *testing.T) {
	movie := fixture(t, "movie_433422.json")
	// Make a recommendation that is also in the collection: it must be filtered out.
	recs := get(t, movie, "recommendations", "results").([]any)
	recs[0].(map[string]any)["id"] = float64(135531)
	f := &fake{routes: map[string]any{
		"/movie/433422":      movie,
		"/collection/524143": fixture(t, "collection.json"),
	}}
	c := newClient(t, f, config.Config{})
	m, err := c.Movie(context.Background(), "433422")
	if err != nil {
		t.Fatal(err)
	}
	if m["media_id"] != "433422" || m["max_progress"] != float64(1) || m["media_type"] != "movie" {
		t.Errorf("movie = %v", m)
	}
	if get(t, m, "details", "format") != "Movie" || get(t, m, "details", "runtime") != "1h 25m" {
		t.Errorf("details = %v", m["details"])
	}
	related := m["related"].(map[string]any)
	coll := related["Fairy Tail Collection"].([]any)
	if len(coll) != 2 {
		t.Fatalf("collection = %v", coll)
	}
	if recs := related["recommendations"].([]any); len(recs) != 2 {
		t.Errorf("recommendations after filter = %d, want 2", len(recs))
	}
	if m["total_cast_count"] != float64(3) || len(m["cast"].([]any)) != 3 {
		t.Errorf("cast = %v", m["cast"])
	}
	cast0 := m["cast"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "name", "character", "image"} {
		if _, ok := cast0[k]; !ok {
			t.Errorf("cast missing %s", k)
		}
	}
	if m["external_links"].(map[string]any)["Letterboxd"] != "https://www.letterboxd.com/tmdb/433422" {
		t.Errorf("links = %v", m["external_links"])
	}
}

func TestMovieCollectionFailureIsNotFatal(t *testing.T) {
	f := &fake{routes: map[string]any{"/movie/433422": fixture(t, "movie_433422.json")}}
	c := newClient(t, f, config.Config{})
	m, err := c.Movie(context.Background(), "433422")
	if err != nil {
		t.Fatal(err)
	}
	related := m["related"].(map[string]any)
	if items, ok := related["collection"].([]any); !ok || len(items) != 0 {
		t.Errorf("related = %v", related)
	}
	if len(related["recommendations"].([]any)) != 3 {
		t.Errorf("recommendations = %v", related["recommendations"])
	}
}

func TestTVWithSeasons(t *testing.T) {
	f := &fake{routes: map[string]any{"/tv/42410": fixture(t, "tv_42410.json")}}
	c := newClient(t, f, config.Config{})
	m, err := c.TVWithSeasons(context.Background(), "42410", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.last().Query().Get("append_to_response"); got != "recommendations,external_ids,watch/providers,season/1,season/1/watch/providers" {
		t.Errorf("append_to_response = %q", got)
	}
	if m["title"] != "Durarara!!" {
		t.Errorf("tv keys missing: %v", m["title"])
	}
	s := m["season/1"].(map[string]any)
	if s["media_type"] != "season" || s["season_number"] != float64(1) || s["media_id"] != "42410" {
		t.Errorf("season = %v", s)
	}
	if s["source_url"] != "https://www.themoviedb.org/tv/42410/season/1" || s["title"] != "Durarara!!" || s["tvdb_id"] != float64(133341) {
		t.Errorf("season enrichment = %v", s)
	}
	if s["max_progress"] != float64(3) {
		t.Errorf("max_progress = %v", s["max_progress"])
	}
	d := s["details"].(map[string]any)
	if d["episodes"] != float64(3) || d["first_air_date"] != "2010-01-08" {
		t.Errorf("details = %v", d)
	}
	// Episode 3 has null runtime and air_date: runtime averages the other two,
	// and the season's last_air_date is the (null) air date of the last episode.
	if d["last_air_date"] != nil {
		t.Errorf("last_air_date = %v, want nil", d["last_air_date"])
	}
	eps := s["episodes"].([]any)
	if eps[2].(map[string]any)["air_date"] != nil || eps[0].(map[string]any)["episode_number"] != float64(1) {
		t.Errorf("episodes = %v", eps)
	}
	rows := ProcessEpisodes(s, map[int][]any{2: {"seen"}})
	if len(rows) != 3 || rows[2]["air_date"] != nil || rows[2]["runtime"] != nil || rows[2]["media_type"] != "episode" {
		t.Errorf("rows = %v", rows)
	}
	if h := rows[1]["history"].([]any); len(h) != 1 {
		t.Errorf("history = %v", h)
	}
	if next, ok := FindNextEpisode(2, rows); !ok || next != 3 {
		t.Errorf("next = %d %v", next, ok)
	}
	if _, ok := FindNextEpisode(3, rows); ok {
		t.Error("last episode has no next")
	}

	// Season missing from the response is a 404 provider error.
	_, err = c.TVWithSeasons(context.Background(), "42410", []int{9})
	if !providers.NotFound(err) || !strings.Contains(err.Error(), "Season 9 not found for The Movie Database with ID 42410") {
		t.Errorf("err = %v", err)
	}

	ep, err := c.Episode(context.Background(), "42410", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ep["title"] != "Durarara!!" || ep["season_title"] != "Season 1" || ep["episode_title"] == nil {
		t.Errorf("episode = %v", ep)
	}
	_, err = c.Episode(context.Background(), "42410", 1, 99)
	if !providers.NotFound(err) || !strings.Contains(err.Error(), "Episode 99 not found in season 1 for The Movie Database with ID 42410") {
		t.Errorf("err = %v", err)
	}
}

func TestSearch(t *testing.T) {
	resp := map[string]any{
		"total_results": 41,
		"results": []any{
			map[string]any{"id": 1, "name": "Show", "poster_path": "/p.jpg", "first_air_date": "2010-01-08"},
			map[string]any{"id": 2, "name": "No Date", "poster_path": nil, "first_air_date": ""},
		},
	}
	f := &fake{routes: map[string]any{"/search/tv": resp}}
	c := newClient(t, f, config.Config{TMDBNSFW: true})
	page, err := c.Search(context.Background(), "season", "show", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	q := f.last().Query()
	if q.Get("query") != "show" || q.Get("page") != "2" || q.Get("include_adult") != "true" {
		t.Errorf("query = %v", q)
	}
	if providers.Int(page, "page") != 2 || providers.Int(page, "total_results") != 41 || providers.Int(page, "total_pages") != 3 {
		t.Errorf("page = %v", page)
	}
	results := providers.List(page, "results")
	if results[0]["release_year"] != float64(2010) || results[0]["title"] != "Show" || results[0]["media_type"] != "tv" ||
		results[0]["image"] != "https://image.tmdb.org/t/p/w500/p.jpg" {
		t.Errorf("result0 = %v", results[0])
	}
	if results[1]["release_year"] != nil || results[1]["image"] != imgNone {
		t.Errorf("result1 = %v", results[1])
	}

	if _, err := c.Search(context.Background(), "tv", "x", 1, false); err != nil {
		t.Fatal(err)
	}
	if f.last().Query().Has("include_adult") {
		t.Error("include_adult must be omitted when the user hides adult titles")
	}
	off := newClient(t, f, config.Config{})
	if _, err := off.Search(context.Background(), "tv", "x", 1, true); err != nil {
		t.Fatal(err)
	}
	if f.last().Query().Has("include_adult") {
		t.Error("include_adult must be omitted when TMDB_NSFW is off")
	}
	if _, err := c.Search(context.Background(), "game", "x", 1, true); err != ErrUnsupported {
		t.Errorf("err = %v", err)
	}
}

func TestFind(t *testing.T) {
	f := &fake{routes: map[string]any{"/find/tt0903747": fixture(t, "find_tv.json")}}
	c := newClient(t, f, config.Config{})
	m, err := c.Find(context.Background(), "tt0903747", "imdb_id")
	if err != nil {
		t.Fatal(err)
	}
	if f.last().Query().Get("external_source") != "imdb_id" {
		t.Errorf("query = %v", f.last().Query())
	}
	tv := m["tv_results"].([]any)
	if len(tv) != 1 || tv[0].(map[string]any)["id"] == nil {
		t.Errorf("tv_results = %v", tv)
	}
}

func TestErrors(t *testing.T) {
	f := &fake{
		routes: map[string]any{
			"/tv/1": map[string]any{"status_code": 7, "status_message": "Invalid API key: You must be granted a valid key."},
			"/tv/2": map[string]any{"status_message": "nope"},
		},
		status: map[string]int{"/tv/1": 401},
	}
	c := newClient(t, f, config.Config{})
	_, err := c.TV(context.Background(), "1")
	if err == nil || err.Error() != "There was an error contacting the The Movie Database API (HTTP 401): Invalid API key: You must be granted a valid key. Check the logs for more details." {
		t.Errorf("err = %v", err)
	}
	_, err = c.TV(context.Background(), "3")
	if !providers.NotFound(err) {
		t.Errorf("err = %v", err)
	}
	if err.Error() != "There was an error contacting the The Movie Database API (HTTP 404). Check the logs for more details." {
		t.Errorf("404 message = %q", err)
	}
}

func TestGetMediaMetadata(t *testing.T) {
	f := &fake{routes: map[string]any{"/tv/42410": fixture(t, "tv_42410.json")}}
	c := newClient(t, f, config.Config{})
	ctx := context.Background()
	if m, err := c.GetMediaMetadata(ctx, "season", "42410", []int{1}, 0); err != nil || m["media_type"] != "season" {
		t.Errorf("season: %v %v", m, err)
	}
	if m, err := c.GetMediaMetadata(ctx, "tv_with_seasons", "42410", []int{1}, 0); err != nil || m["season/1"] == nil {
		t.Errorf("tv_with_seasons: %v", err)
	}
	if m, err := c.GetMediaMetadata(ctx, "episode", "42410", []int{1}, 1); err != nil || m["episode_title"] == nil {
		t.Errorf("episode: %v %v", m, err)
	}
	if _, err := c.GetMediaMetadata(ctx, "season", "42410", nil, 0); err == nil {
		t.Error("season without a number must fail")
	}
	if _, err := c.GetMediaMetadata(ctx, "book", "1", nil, 0); err != ErrUnsupported {
		t.Errorf("err = %v", err)
	}
}

func TestHelpers(t *testing.T) {
	if GetImageURL(nil) != imgNone || GetImageURL("") != imgNone || GetImageURL("/a.jpg") != "https://image.tmdb.org/t/p/w500/a.jpg" {
		t.Error("GetImageURL")
	}
	for in, want := range map[float64]any{0: nil, 45: "45m", 60: "1h 0m", 85: "1h 25m", 59.9: "59m"} {
		if got := getReadableDuration(in); got != want {
			t.Errorf("duration(%v) = %v, want %v", in, got, want)
		}
	}
	if getReadableDuration(nil) != nil {
		t.Error("nil duration")
	}
	if getScore(7.25) != 7.2 || getScore(6.66) != 6.7 || getScore(0.0) != 0 {
		t.Errorf("score: %v %v", getScore(7.25), getScore(6.66))
	}
	if getSynopsis("") != "No synopsis available." || getSynopsis("x") != "x" {
		t.Error("synopsis")
	}
	if getStartDate("") != nil || getStartDate("2020-01-01") != "2020-01-01" || getStartDate(nil) != nil {
		t.Error("start date")
	}
	if getGenres([]any{}) != nil || getCountry([]any{}) != nil || getLanguages(nil) != nil || getCompanies(nil) != nil {
		t.Error("empty lists must be nil")
	}
	comps := getCompanies([]any{obj{"name": "a"}, obj{"name": "b"}, obj{"name": "c"}, obj{"name": "d"}}).([]any)
	if len(comps) != 3 {
		t.Errorf("companies = %v", comps)
	}
	if getRuntimeTV([]any{}) != nil || getRuntimeTV([]any{float64(24)}) != "24m" {
		t.Error("tv runtime")
	}
	links := getExternalLinks(obj{"imdb_id": "tt1", "tvdb_id": nil, "wikidata_id": ""}, "")
	if len(links) != 1 || links["IMDb"] != "https://www.imdb.com/title/tt1/" {
		t.Errorf("links = %v", links)
	}
}

func TestFilterProviders(t *testing.T) {
	all := map[string]any{"US": map[string]any{
		"flatrate": []any{
			map[string]any{"provider_id": float64(2), "display_priority": float64(5), "logo_path": "/b.png"},
			map[string]any{"provider_id": float64(1), "display_priority": float64(1), "logo_path": "/a.png"},
		},
		"free": []any{map[string]any{"provider_id": float64(1), "display_priority": float64(1), "logo_path": "/a.png"}},
	}}
	got := FilterProviders(all, "US")
	if len(got) != 2 || got[0]["provider_id"] != float64(1) || got[0]["image"] != "https://image.tmdb.org/t/p/w500/a.png" {
		t.Errorf("got = %v", got)
	}
	if FilterProviders(all, "") != nil {
		t.Error("blank region disables")
	}
	if got := FilterProviders(nil, "US"); got == nil || len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
	if got := FilterProviders(all, "FR"); len(got) != 0 {
		t.Errorf("FR = %v", got)
	}
}

func TestRegionsAndChanges(t *testing.T) {
	f := &fake{routes: map[string]any{
		"/watch/providers/regions": map[string]any{"results": []any{
			map[string]any{"iso_3166_1": "US", "english_name": "United States"},
			map[string]any{"iso_3166_1": "DE", "english_name": ""},
			map[string]any{"iso_3166_1": "", "english_name": "Nowhere"},
			map[string]any{"iso_3166_1": "CA", "english_name": "Canada"},
		}},
		"/tv/changes": map[string]any{"results": []any{map[string]any{"id": 5}, map[string]any{"id": 6}}, "total_pages": 1},
	}}
	c := newClient(t, f, config.Config{})
	regions, err := c.WatchProviderRegions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if regions[0].Code != "DE" || regions[1].Code != "CA" || regions[2].Code != "US" {
		t.Errorf("regions = %v", regions)
	}
	ids, err := c.TVChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids["5"]; !ok || len(ids) != 2 {
		t.Errorf("ids = %v", ids)
	}
	q := f.last().Query()
	if q.Get("start_date") == "" || q.Get("end_date") == "" || q.Get("page") != "1" {
		t.Errorf("query = %v", q)
	}
}

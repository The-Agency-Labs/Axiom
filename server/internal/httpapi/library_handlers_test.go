package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// libraryFakeMAL is a MyAnimeList client with one 12-episode anime (id "1").
type libraryFakeMAL struct{}

func (libraryFakeMAL) Anime(_ context.Context, id string) (providers.Metadata, error) {
	if id != "1" {
		return nil, providers.NotFoundError("MyAnimeList", "anime", id)
	}
	return providers.Metadata{
		"media_id": id, "source": "mal", "media_type": "anime", "title": "Count Anime", "max_progress": 12,
		"image": "https://img/mal.jpg", "synopsis": "Counted.", "details": map[string]any{"start_date": "2020-01-01"},
	}, nil
}
func (libraryFakeMAL) Manga(context.Context, string) (providers.Metadata, error) {
	return nil, providers.NotFoundError("MyAnimeList", "manga", "")
}
func (libraryFakeMAL) Search(context.Context, string, string, int, bool) (providers.SearchPage, error) {
	return providers.SearchPage{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}}, nil
}

type libraryEnv struct {
	t       *testing.T
	env     *catalogtest.Env
	ts      *httptest.Server
	srv     *Server
	userID  int64
	write   string // bearer key with write scope
	read    string // bearer key with read scope
	other   string // write token of another user
	otherID int64
}

func newLibraryEnv(t *testing.T) *libraryEnv {
	t.Helper()
	env := catalogtest.NewEnv(t, time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC))
	env.Catalog.Reg.P.MAL = libraryFakeMAL{}
	srv := &Server{Config: config.Config{}, DB: env.Conn, Queries: env.Q, Auth: auth.NewService(env.Q, 14),
		Web: fstest.MapFS{"200.html": {Data: []byte("<html></html>")}}, Catalog: env.Catalog}
	srv.Library = library.New(env.Conn, env.Q, env.Catalog)
	srv.Library.Now = env.Now
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	ctx := context.Background()
	u, err := srv.Auth.CreateUser(ctx, "alice", "password-1", true)
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Auth.CreateUser(ctx, "bob", "password-2", false)
	if err != nil {
		t.Fatal(err)
	}
	le := &libraryEnv{t: t, env: env, ts: ts, srv: srv, userID: u.ID, otherID: o.ID}
	for _, tk := range []struct {
		dst  *string
		user int64
		name string
		sc   string
	}{{&le.write, u.ID, "w", auth.ScopeWrite}, {&le.read, u.ID, "r", auth.ScopeRead}, {&le.other, o.ID, "o", auth.ScopeWrite}} {
		nt, err := srv.Auth.CreateToken(ctx, tk.user, tk.name, tk.sc)
		if err != nil {
			t.Fatal(err)
		}
		*tk.dst = nt.Key
	}
	return le
}

// call sends a request with a bearer key and decodes the JSON response.
func (le *libraryEnv) call(key, method, path string, body any) (int, any) {
	le.t.Helper()
	var rd bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&rd).Encode(body); err != nil {
			le.t.Fatal(err)
		}
	}
	req, _ := http.NewRequest(method, le.ts.URL+path, &rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		le.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (le *libraryEnv) get(path string) (int, any)         { return le.call(le.write, "GET", path, nil) }
func (le *libraryEnv) post(path string, b any) (int, any) { return le.call(le.write, "POST", path, b) }
func (le *libraryEnv) expect(code int, want int, body any, what string) map[string]any {
	le.t.Helper()
	if code != want {
		le.t.Fatalf("%s: status %d, want %d (%v)", what, code, want, body)
	}
	m, _ := body.(map[string]any)
	return m
}

func libraryMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func libraryList(v any) []any         { l, _ := v.([]any); return l }
func libraryNum(v any) int            { return int(v.(float64)) }

func (le *libraryEnv) addShows() {
	le.env.TMDB.AddShow("100", "Plain Show",
		catalogtest.Season{Number: 0, Episodes: catalogtest.Eps(1, 1, "2020-01-01")},
		catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: catalogtest.Eps(1, 3, "2020-01-01")},
		catalogtest.Season{Number: 2, AirDate: "2021-01-01", Episodes: catalogtest.Eps(1, 2, "2021-01-01")})
	le.env.TMDB.AddShow("200", "Anime Show",
		catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: catalogtest.Eps(1, 2, "2020-01-01")})
	le.env.Mappings.AddShow(200, 1, 0, 5000, 6000)
	le.env.TMDB.AddMovie("300", "Plain Movie", "2019-05-05")
	le.env.TMDB.AddMovie("301", "Anime Film", "2018-05-05")
	le.env.Mappings.AddMovie(301, 5001, 6001)
}

func TestLibraryAuth(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()
	for _, p := range []string{"/api/v1/library", "/api/v1/library/find?media_type=tv&source=tmdb&media_id=1",
		"/api/v1/library/tv/1", "/api/v1/search?media_type=tv&q=x", "/api/v1/metadata/tv/tmdb/100",
		"/api/v1/shows/tmdb/100", "/api/v1/shows/tmdb/100/seasons/1", "/api/v1/anime/mal/1/episodes"} {
		if code, _ := le.call("", "GET", p, nil); code != 401 {
			t.Errorf("anonymous GET %s: %d", p, code)
		}
	}
	// Read-only tokens can read but not write.
	if code, _ := le.call(le.read, "GET", "/api/v1/library", nil); code != 200 {
		t.Errorf("read token list: %d", code)
	}
	for _, w := range []struct{ method, path string }{
		{"POST", "/api/v1/library"}, {"DELETE", "/api/v1/library/tv/1"}, {"POST", "/api/v1/library/anime/1/step"},
		{"POST", "/api/v1/shows/tmdb/100/seasons/1/episodes"},
		{"POST", "/api/v1/anime/mal/1/episodes"},
	} {
		if code, _ := le.call(le.read, w.method, w.path, map[string]any{}); code != 403 {
			t.Errorf("read token %s %s: %d", w.method, w.path, code)
		}
		if code, _ := le.call("", w.method, w.path, map[string]any{}); code != 401 {
			t.Errorf("anonymous %s %s: %d", w.method, w.path, code)
		}
	}

	// A cookie session needs the CSRF header on writes.
	c := newClient(t, le.ts.URL)
	_, body, _ := c.do("GET", "/api/v1/auth/csrf", nil, nil)
	c.csrf = body["csrf"].(string)
	code, body, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "password-1"}, nil)
	if code != 200 {
		t.Fatalf("login: %d %v", code, body)
	}
	c.csrf = body["csrf"].(string)
	if code, _, _ := c.do("GET", "/api/v1/library", nil, nil); code != 200 {
		t.Errorf("session list: %d", code)
	}
	if code, _, _ := c.do("DELETE", "/api/v1/library/tv/999999", nil, map[string]string{"X-CSRFToken": "bad"}); code != 403 {
		t.Errorf("bad csrf: %d", code)
	}
	if code, _, _ := c.do("DELETE", "/api/v1/library/tv/999999", nil, nil); code != 404 {
		t.Errorf("with csrf (unknown entry): %d", code)
	}
}

func TestLibraryShowFlow(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()

	// The overview works before the show is tracked.
	code, body := le.get("/api/v1/shows/tmdb/100")
	show := le.expect(code, 200, body, "show")
	if show["entry"] != nil || show["title"] != "Plain Show" || show["media_id"] != "100" || show["source"] != "tmdb" {
		t.Fatalf("untracked show: %v", show)
	}
	seasons := libraryList(show["seasons"])
	if len(seasons) != 3 || libraryMap(seasons[0])["season_number"].(float64) != 1 || libraryMap(seasons[2])["is_special"] != true {
		t.Fatalf("seasons order: %v", seasons)
	}
	if c := libraryMap(show["completion"]); c["total"].(float64) != 5 || c["watched"].(float64) != 0 || c["seasons"].(float64) != 2 {
		t.Fatalf("completion: %v", c)
	}

	code, body = le.get("/api/v1/shows/tmdb/100/seasons/1")
	season := le.expect(code, 200, body, "season")
	if season["entry"] != nil || season["total"].(float64) != 3 || len(libraryList(season["episodes"])) != 3 || season["runtime_minutes"].(float64) != 72 {
		t.Fatalf("season: %v", season)
	}
	if ep := libraryMap(libraryList(season["episodes"])[0]); ep["aired"] != true || ep["watched"] != false || ep["title"] != "Episode 1" {
		t.Fatalf("episode: %v", ep)
	}

	// Watch episode 1: the season and show entries appear.
	code, body = le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"episodes": []int{1}})
	season = le.expect(code, 200, body, "watch 1")
	if season["watched"].(float64) != 1 || libraryMap(season["entry"])["status"] != "In progress" || libraryMap(season["entry"])["media_type"] != "season" {
		t.Fatalf("after watch: %v", season)
	}
	if ep := libraryMap(libraryList(season["episodes"])[0]); ep["watched"] != true || ep["watch_count"].(float64) != 1 || ep["watched_at"] == nil {
		t.Fatalf("episode 1: %v", ep)
	}
	// Watching it again without rewatch changes nothing; with rewatch it adds a play.
	code, body = le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"episodes": []int{1}})
	if season = le.expect(code, 200, body, "again"); libraryMap(libraryList(season["episodes"])[0])["watch_count"].(float64) != 1 {
		t.Fatalf("duplicate watch recorded: %v", season)
	}
	code, body = le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"episodes": []int{1}, "rewatch": true, "date": "2025-05-01T10:00:00Z"})
	if season = le.expect(code, 200, body, "rewatch"); libraryMap(libraryList(season["episodes"])[0])["watch_count"].(float64) != 2 {
		t.Fatalf("rewatch not recorded: %v", season)
	}

	// up_to completes the season.
	code, body = le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"up_to": 3})
	season = le.expect(code, 200, body, "up_to")
	if season["watched"].(float64) != 3 || libraryMap(season["entry"])["status"] != "Completed" {
		t.Fatalf("season not completed: %v", season)
	}

	// Manual unwatch is allowed (the app may undo; automated sources may not).
	code, body = le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"episodes": []int{3}, "watched": false})
	season = le.expect(code, 200, body, "unwatch")
	if season["watched"].(float64) != 2 || libraryMap(season["entry"])["status"] != "In progress" {
		t.Fatalf("unwatch did not reopen: %v", season)
	}
	if ep := libraryMap(libraryList(season["episodes"])[2]); ep["watched"] != false {
		t.Fatalf("episode 3 still watched: %v", ep)
	}

	// "all" marks the whole season and the show overview follows.
	le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"all": true})
	code, body = le.get("/api/v1/shows/tmdb/100")
	show = le.expect(code, 200, body, "show after")
	if show["entry"] == nil || libraryMap(show["completion"])["watched"].(float64) != 3 || libraryMap(show["completion"])["seasons_completed"].(float64) != 1 {
		t.Fatalf("show after: %v", show)
	}
	if e := libraryMap(libraryMap(libraryList(show["seasons"])[0])["entry"]); e["status"] != "Completed" {
		t.Fatalf("season entry: %v", e)
	}

	// Errors.
	for _, tc := range []struct {
		what, path string
		body       any
		want       int
	}{
		{"no matching episodes", "/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"episodes": []int{99}}, 400},
		{"empty body", "/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{}, 400},
		{"unknown source", "/api/v1/shows/nope/100/seasons/1/episodes", map[string]any{"all": true}, 400},
		{"bad date", "/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"all": true, "date": "yesterday"}, 422},
		{"bad season", "/api/v1/shows/tmdb/100/seasons/x/episodes", map[string]any{"all": true}, 422},
		{"non-tmdb show", "/api/v1/shows/mal/1/seasons/1/episodes", map[string]any{"all": true}, 400},
		{"unknown show", "/api/v1/shows/tmdb/9999/seasons/1/episodes", map[string]any{"all": true}, 502},
	} {
		if code, body := le.post(tc.path, tc.body); code != tc.want {
			t.Errorf("%s: %d, want %d (%v)", tc.what, code, tc.want, body)
		}
	}
	if code, _ := le.get("/api/v1/shows/nope/100"); code != 400 {
		t.Errorf("show unknown source: %d", code)
	}
	if code, _ := le.get("/api/v1/shows/tmdb/9999"); code != 502 {
		t.Errorf("show unknown id: %d", code)
	}

	// Another user sees none of it.
	code, body = le.call(le.other, "GET", "/api/v1/shows/tmdb/100", nil)
	if le.expect(code, 200, body, "other user")["entry"] != nil {
		t.Fatalf("entry leaked to another user: %v", body)
	}
}

func TestLibraryDetailAndEntries(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()
	le.post("/api/v1/shows/tmdb/100/seasons/1/episodes", map[string]any{"episodes": []int{1, 2}})

	code, body := le.get("/api/v1/library/find?media_type=tv&source=tmdb&media_id=100")
	id := libraryNum(le.expect(code, 200, body, "find")["id"])
	for _, q := range []string{"media_type=tv&source=tmdb&media_id=999", "media_type=movie&source=tmdb&media_id=100", "media_type=tv&source=tmdb&media_id=100&season_number=2"} {
		code, body = le.get("/api/v1/library/find?" + q)
		if le.expect(code, 200, body, "find "+q)["id"] != nil {
			t.Errorf("find %s should be null: %v", q, body)
		}
	}
	code, body = le.get("/api/v1/library/find?media_type=tv&source=tmdb&media_id=100&season_number=1")
	if le.expect(code, 200, body, "find season")["id"] == nil {
		t.Errorf("find season 1 should be the show entry")
	}
	if code, _ = le.get("/api/v1/library/find?media_type=bogus&source=tmdb&media_id=1"); code != 422 {
		t.Errorf("find bad type: %d", code)
	}
	if code, _ = le.get("/api/v1/library/find?media_type=tv"); code != 422 {
		t.Errorf("find missing params: %d", code)
	}

	code, body = le.get(fmt.Sprintf("/api/v1/library/tv/%d", id))
	d := le.expect(code, 200, body, "detail")
	for _, k := range []string{"id", "media_type", "item", "status", "score", "progress", "progressed_at", "start_date", "end_date", "notes", "created_at", "is_anime", "history", "seasons", "metadata"} {
		if _, ok := d[k]; !ok {
			t.Errorf("detail missing %q", k)
		}
	}
	if d["is_anime"] != false || d["media_type"] != "tv" || libraryMap(d["item"])["media_id"] != "100" || libraryMap(d["metadata"])["title"] != "Plain Show" {
		t.Fatalf("detail: %v", d)
	}
	ss := libraryList(d["seasons"])
	if len(ss) != 1 || len(libraryList(libraryMap(ss[0])["episodes"])) != 2 || libraryMap(ss[0])["season_number"].(float64) != 1 {
		t.Fatalf("detail seasons: %v", ss)
	}
	if h := libraryList(d["history"]); len(h) == 0 || libraryMap(h[0])["type"] != "created" || libraryMap(h[0])["progress"] != nil {
		t.Fatalf("history: %v", h)
	}
	// include_metadata=false drops the provider call.
	code, body = le.get(fmt.Sprintf("/api/v1/library/tv/%d?include_metadata=false", id))
	if _, ok := le.expect(code, 200, body, "no meta")["metadata"]; ok {
		t.Errorf("metadata present with include_metadata=false")
	}
	// A provider outage turns metadata into null instead of failing the page.
	le.env.TMDB.Err = &providers.Error{Provider: "The Movie Database", Status: 500}
	code, body = le.get(fmt.Sprintf("/api/v1/library/tv/%d", id))
	if m := le.expect(code, 200, body, "outage"); m["metadata"] != nil {
		t.Errorf("metadata on outage: %v", m["metadata"])
	}
	le.env.TMDB.Err = nil

	// Wrong media type, unknown id, other user's entry, bad inputs.
	for _, p := range []struct {
		path string
		want int
	}{
		{fmt.Sprintf("/api/v1/library/movie/%d", id), 404},
		{fmt.Sprintf("/api/v1/library/anime/%d", id), 404},
		{"/api/v1/library/tv/99999", 404},
		{"/api/v1/library/tv/abc", 422},
		{"/api/v1/library/bogus/1", 422},
		{fmt.Sprintf("/api/v1/library/tv/%d?include_metadata=maybe", id), 422},
	} {
		if code, body := le.get(p.path); code != p.want {
			t.Errorf("GET %s: %d, want %d (%v)", p.path, code, p.want, body)
		}
	}
	if code, _ := le.call(le.other, "GET", fmt.Sprintf("/api/v1/library/tv/%d", id), nil); code != 404 {
		t.Errorf("other user's entry: %d", code)
	}

	// Track a movie through POST /library, then update it by instance_id.
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300", "status": "Planning", "notes": "later"})
	m := le.expect(code, 200, body, "save movie")
	mid := libraryNum(m["id"])
	if m["status"] != "Planning" || m["notes"] != "later" || m["is_anime"] != false || m["media_type"] != "movie" {
		t.Fatalf("saved movie: %v", m)
	}
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "movie", "instance_id": mid, "status": "Completed", "score": 8.5, "start_date": "2025-01-02", "end_date": "2025-01-03T20:00:00Z"})
	m = le.expect(code, 200, body, "update movie")
	if m["status"] != "Completed" || m["score"].(float64) != 8.5 || m["progress"].(float64) != 1 || m["notes"] != "later" ||
		m["start_date"] == nil || m["end_date"] == nil {
		t.Fatalf("updated movie: %v", m)
	}
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "movie", "instance_id": mid, "start_date": ""})
	if m = le.expect(code, 200, body, "clear date"); m["start_date"] != nil || m["notes"] != "later" {
		t.Fatalf("cleared date: %v", m)
	}
	for _, tc := range []struct {
		what string
		body map[string]any
		want int
	}{
		{"bad status", map[string]any{"media_type": "movie", "instance_id": mid, "status": "Nope"}, 422},
		{"bad score", map[string]any{"media_type": "movie", "instance_id": mid, "score": 11}, 422},
		{"bad date", map[string]any{"media_type": "movie", "instance_id": mid, "end_date": "soon"}, 422},
		{"bad media type", map[string]any{"media_type": "season", "source": "tmdb", "media_id": "1"}, 422},
		{"missing id", map[string]any{"media_type": "movie", "source": "tmdb"}, 422},
		{"wrong type instance", map[string]any{"media_type": "tv", "instance_id": mid}, 404},
		{"unknown instance", map[string]any{"media_type": "movie", "instance_id": 99999}, 404},
		{"unknown source", map[string]any{"media_type": "movie", "source": "nope", "media_id": "300"}, 400},
		{"provider miss", map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "98765"}, 502},
		{"unsupported", map[string]any{"media_type": "movie", "source": "mal", "media_id": "1"}, 400},
	} {
		if code, body := le.post("/api/v1/library", tc.body); code != tc.want {
			t.Errorf("%s: %d, want %d (%v)", tc.what, code, tc.want, body)
		}
	}
	if code, _ := le.call(le.other, "POST", "/api/v1/library", map[string]any{"media_type": "movie", "instance_id": mid}); code != 404 {
		t.Errorf("other user's instance: %d", code)
	}

	// Tracking a show as Completed marks every released episode.
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "tv", "source": "tmdb", "media_id": "100", "status": "Completed"})
	if m = le.expect(code, 200, body, "complete show"); m["status"] != "Completed" || libraryNum(m["id"]) != id {
		t.Fatalf("complete show: %v", m)
	}
	// A season status through season_number.
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "tv", "source": "tmdb", "media_id": "100", "season_number": 2, "status": "Dropped", "score": 6})
	le.expect(code, 200, body, "season dropped")
	code, body = le.get(fmt.Sprintf("/api/v1/library/tv/%d?include_metadata=false", id))
	var s2 map[string]any
	for _, s := range libraryList(libraryMap(body)["seasons"]) {
		if libraryMap(s)["season_number"].(float64) == 2 {
			s2 = libraryMap(s)
		}
	}
	if s2 == nil || s2["status"] != "Dropped" || s2["score"].(float64) != 6 {
		t.Fatalf("season 2 after update: %v", s2)
	}

	// Delete.
	if code, _ := le.call(le.write, "DELETE", fmt.Sprintf("/api/v1/library/tv/%d", mid), nil); code != 404 {
		t.Errorf("delete with wrong type: %d", code)
	}
	if code, _ := le.call(le.other, "DELETE", fmt.Sprintf("/api/v1/library/movie/%d", mid), nil); code != 404 {
		t.Errorf("delete other user's: %d", code)
	}
	code, body = le.call(le.write, "DELETE", fmt.Sprintf("/api/v1/library/movie/%d", mid), nil)
	if le.expect(code, 200, body, "delete")["deleted"] != true {
		t.Fatalf("delete: %v", body)
	}
	if code, _ := le.get(fmt.Sprintf("/api/v1/library/movie/%d", mid)); code != 404 {
		t.Errorf("deleted entry still there: %d", code)
	}
}

func TestLibraryListAnimeSplit(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()
	track := func(kind, source, id string) int {
		code, body := le.post("/api/v1/library", map[string]any{"media_type": kind, "source": source, "media_id": id, "status": "Planning"})
		return libraryNum(le.expect(code, 200, body, "track "+kind+" "+id)["id"])
	}
	track("tv", "tmdb", "100")
	animeShow := track("tv", "tmdb", "200") // the mapping makes it anime
	track("anime", "mal", "1")
	track("movie", "tmdb", "300")
	track("movie", "tmdb", "301")

	list := func(q string) map[string]any {
		code, body := le.get("/api/v1/library" + q)
		return le.expect(code, 200, body, "list "+q)
	}
	titles := func(m map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, r := range libraryList(m["results"]) {
			out[libraryMap(libraryMap(r)["item"])["title"].(string)] = true
		}
		return out
	}
	if m := list(""); m["count"].(float64) != 5 || len(libraryList(m["results"])) != 5 {
		t.Fatalf("all: %v", m)
	}
	if got := titles(list("?media_type=tv")); len(got) != 1 || !got["Plain Show"] {
		t.Errorf("tv: %v", got)
	}
	if got := titles(list("?media_type=anime")); len(got) != 2 || !got["Anime Show"] || !got["Count Anime"] {
		t.Errorf("anime: %v", got)
	}
	if got := titles(list("?media_type=movie")); len(got) != 1 || !got["Plain Movie"] {
		t.Errorf("movie: %v", got)
	}
	if got := titles(list("?media_type=anime_movie")); len(got) != 1 || !got["Anime Film"] {
		t.Errorf("anime_movie: %v", got)
	}
	if m := list("?media_type=game"); m["count"].(float64) != 0 || len(libraryList(m["results"])) != 0 || m["results"] == nil || len(libraryList(m["years"])) != 0 {
		t.Errorf("game: %v", m)
	}
	for _, r := range libraryList(list("")["results"]) {
		row := libraryMap(r)
		isAnime := row["is_anime"].(bool)
		kind := row["media_type"].(string)
		if isAnime != (kind == "anime" || kind == "anime_movie") {
			t.Errorf("is_anime %v for %s", isAnime, kind)
		}
		_, hasMax := row["max_progress"]
		if hasMax != (kind == "tv" || (kind == "anime" && libraryMap(row["item"])["source"] == "tmdb")) {
			t.Errorf("max_progress presence %v for %s %v", hasMax, kind, libraryMap(row["item"])["source"])
		}
	}
	// Filters and paging.
	if m := list("?search=plain&limit=1"); m["count"].(float64) != 2 || len(libraryList(m["results"])) != 1 {
		t.Errorf("search/limit: %v", m)
	}
	if m := list("?limit=2&offset=4"); len(libraryList(m["results"])) != 1 {
		t.Errorf("offset: %v", m)
	}
	if m := list("?status=Completed"); m["count"].(float64) != 0 {
		t.Errorf("status: %v", m)
	}
	if m := list("?year=2025"); m["count"].(float64) != 5 || len(libraryList(m["years"])) != 1 || libraryList(m["years"])[0].(float64) != 2025 {
		t.Errorf("year: %v", m)
	}
	if m := list("?year=1999"); m["count"].(float64) != 0 || len(libraryList(m["years"])) != 1 {
		t.Errorf("other year: %v", m)
	}
	for _, q := range []string{"?media_type=bogus", "?limit=x", "?year=x", "?offset=x"} {
		if code, _ := le.get("/api/v1/library" + q); code != 422 {
			t.Errorf("list %s: %d", q, code)
		}
	}
	// Another user has an empty library.
	code, body := le.call(le.other, "GET", "/api/v1/library", nil)
	if m := le.expect(code, 200, body, "other list"); m["count"].(float64) != 0 {
		t.Errorf("other user's list: %v", m)
	}

	// The anime show works through the detail and shows endpoints like tv.
	code, body = le.get(fmt.Sprintf("/api/v1/library/anime/%d", animeShow))
	d := le.expect(code, 200, body, "anime detail")
	if d["is_anime"] != true || d["media_type"] != "anime" || libraryList(d["seasons"]) == nil {
		t.Fatalf("anime detail: %v", d)
	}
	if code, _ := le.get(fmt.Sprintf("/api/v1/library/tv/%d", animeShow)); code != 404 {
		t.Errorf("anime show under tv: %d", code)
	}
	if code, body = le.post("/api/v1/shows/tmdb/200/seasons/1/episodes", map[string]any{"up_to": 1}); code != 200 {
		t.Fatalf("anime episodes: %d %v", code, body)
	}
	code, body = le.get(fmt.Sprintf("/api/v1/library/anime/%d?include_metadata=false", animeShow))
	if ss := libraryList(libraryMap(body)["seasons"]); len(ss) != 1 || len(libraryList(libraryMap(ss[0])["episodes"])) != 1 {
		t.Fatalf("anime seasons: %v", body)
	}
}

func TestLibraryStepAndCountAnime(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()
	code, body := le.get("/api/v1/anime/mal/1/episodes")
	a := le.expect(code, 200, body, "episodes")
	if a["entry"] != nil || a["progress"].(float64) != 0 || a["total"].(float64) != 12 || len(libraryList(a["episodes"])) != 12 ||
		a["tmdb"] != nil || a["anilist_id"] != nil || len(libraryList(a["providers"])) != 0 || a["title"] != "Count Anime" {
		t.Fatalf("untracked anime: %v", a)
	}
	// Unwatching with nothing tracked changes nothing.
	code, body = le.post("/api/v1/anime/mal/1/episodes", map[string]any{"episode": 1, "watched": false})
	if le.expect(code, 200, body, "unwatch untracked")["entry"] != nil {
		t.Fatalf("entry created by unwatch: %v", body)
	}

	code, body = le.post("/api/v1/anime/mal/1/episodes", map[string]any{"episode": 3})
	a = le.expect(code, 200, body, "watch 3")
	entry := libraryMap(a["entry"])
	if a["progress"].(float64) != 3 || entry["status"] != "In progress" || entry["is_anime"] != true || entry["start_date"] == nil {
		t.Fatalf("after watch 3: %v", a)
	}
	if eps := libraryList(a["episodes"]); libraryMap(eps[2])["watched"] != true || libraryMap(eps[3])["watched"] != false || libraryMap(eps[2])["watch_count"].(float64) != 1 {
		t.Fatalf("episodes after watch: %v", eps[:4])
	}
	// Manual may move progress down.
	code, body = le.post("/api/v1/anime/mal/1/episodes", map[string]any{"episode": 3, "watched": false})
	if a = le.expect(code, 200, body, "unwatch 3"); a["progress"].(float64) != 2 {
		t.Fatalf("unwatch: %v", a)
	}
	// Reaching the last episode completes it; moving back reopens it.
	code, body = le.post("/api/v1/anime/mal/1/episodes", map[string]any{"episode": 12})
	if a = le.expect(code, 200, body, "watch 12"); libraryMap(a["entry"])["status"] != "Completed" {
		t.Fatalf("not completed: %v", a)
	}
	code, body = le.post("/api/v1/anime/mal/1/episodes", map[string]any{"episode": 5, "watched": false})
	if a = le.expect(code, 200, body, "rewind"); libraryMap(a["entry"])["status"] != "In progress" || a["progress"].(float64) != 4 {
		t.Fatalf("rewind: %v", a)
	}

	for _, tc := range []struct {
		what, method, path string
		body               any
		want               int
	}{
		{"zero episode", "POST", "/api/v1/anime/mal/1/episodes", map[string]any{"episode": 0}, 400},
		{"missing episode", "POST", "/api/v1/anime/mal/1/episodes", map[string]any{}, 422},
		{"tmdb source get", "GET", "/api/v1/anime/tmdb/200/episodes", nil, 400},
		{"tmdb source post", "POST", "/api/v1/anime/tmdb/200/episodes", map[string]any{"episode": 1}, 400},
		{"unknown anime", "GET", "/api/v1/anime/mal/777/episodes", nil, 502},
	} {
		if code, body := le.call(le.write, tc.method, tc.path, tc.body); code != tc.want {
			t.Errorf("%s: %d, want %d (%v)", tc.what, code, tc.want, body)
		}
	}

	// Step: count-based entries move by one; shows and movies are refused.
	id := libraryNum(libraryMap(libraryMap(a["entry"]))["id"])
	code, body = le.post(fmt.Sprintf("/api/v1/library/anime/%d/step", id), map[string]any{"direction": "up"})
	if m := le.expect(code, 200, body, "step up"); m["progress"].(float64) != 5 {
		t.Fatalf("step up: %v", m)
	}
	code, body = le.post(fmt.Sprintf("/api/v1/library/anime/%d/step", id), map[string]any{"direction": "down"})
	if m := le.expect(code, 200, body, "step down"); m["progress"].(float64) != 4 {
		t.Fatalf("step down: %v", m)
	}
	// An empty body defaults to up.
	req, _ := http.NewRequest("POST", le.ts.URL+fmt.Sprintf("/api/v1/library/anime/%d/step", id), nil)
	req.Header.Set("Authorization", "Bearer "+le.write)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("empty-body step: %v %v", resp, err)
	}
	resp.Body.Close()
	if code, _ = le.post(fmt.Sprintf("/api/v1/library/anime/%d/step", id), map[string]any{"direction": "sideways"}); code != 422 {
		t.Errorf("bad direction: %d", code)
	}
	if code, _ = le.post(fmt.Sprintf("/api/v1/library/tv/%d/step", id), map[string]any{}); code != 404 {
		t.Errorf("step wrong type: %d", code)
	}
	if code, _ = le.call(le.other, "POST", fmt.Sprintf("/api/v1/library/anime/%d/step", id), map[string]any{}); code != 404 {
		t.Errorf("step other user's: %d", code)
	}
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "tv", "source": "tmdb", "media_id": "100"})
	if code, _ = le.post(fmt.Sprintf("/api/v1/library/tv/%d/step", libraryNum(libraryMap(body)["id"])), map[string]any{}); code != 400 {
		t.Errorf("step show: %d", code)
	}
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300"})
	if code, _ = le.post(fmt.Sprintf("/api/v1/library/movie/%d/step", libraryNum(libraryMap(body)["id"])), map[string]any{}); code != 400 {
		t.Errorf("step movie: %d", code)
	}

	// Progress through POST /library on a count-based entry.
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "anime", "instance_id": id, "progress": 9, "score": 7.5})
	if m := le.expect(code, 200, body, "set progress"); m["progress"].(float64) != 9 || m["score"].(float64) != 7.5 {
		t.Fatalf("set progress: %v", m)
	}
	code, body = le.get(fmt.Sprintf("/api/v1/library/anime/%d?include_metadata=false", id))
	if h := libraryList(libraryMap(body)["history"]); len(h) == 0 || libraryMap(h[len(h)-1])["progress"] != float64(9) || libraryMap(body)["seasons"] != nil {
		t.Errorf("count-based anime history/seasons: %v", body)
	}
	if code, _ = le.post("/api/v1/library", map[string]any{"media_type": "anime", "instance_id": id, "progress": -1}); code != 422 {
		t.Errorf("negative progress: %d", code)
	}
}

func TestLibrarySearchAndMetadata(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()
	code, body := le.get("/api/v1/search?media_type=tv&q=plain")
	if m := le.expect(code, 200, body, "search"); m["results"] == nil {
		t.Fatalf("search: %v", m)
	}
	code, body = le.get("/api/v1/search?media_type=anime&q=count&source=mal")
	if m := le.expect(code, 200, body, "search mal"); m["total_pages"] == nil || m["page"] == nil {
		t.Fatalf("search mal: %v", m)
	}
	code, body = le.get("/api/v1/search?media_type=all&q=plain&types=tv,movie")
	if m := le.expect(code, 200, body, "search all"); m["results"] == nil || m["total_pages"] == nil {
		t.Fatalf("search all: %v", m)
	} else {
		for _, r := range m["results"].([]any) {
			if k := r.(map[string]any)["media_type"]; k != "tv" && k != "movie" {
				t.Errorf("search all returned kind %v", k)
			}
		}
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/search?media_type=tv", 422},
		{"/api/v1/search?q=x", 422},
		{"/api/v1/search?media_type=bogus&q=x", 422},
		{"/api/v1/search?media_type=tv&q=x&page=z", 422},
		{"/api/v1/search?media_type=book&q=x", 400}, // no Hardcover/Open Library client configured
		{"/api/v1/metadata/bogus/tmdb/100", 422},
		{"/api/v1/metadata/tv/nope/100", 400},
		{"/api/v1/metadata/tv/mal/100", 400},
		{"/api/v1/metadata/tv/tmdb/99999", 502},
		{"/api/v1/metadata/tv/tmdb/100?season_number=z", 422},
	} {
		if code, body := le.get(tc.path); code != tc.want {
			t.Errorf("GET %s: %d, want %d (%v)", tc.path, code, tc.want, body)
		}
	}
	le.env.TMDB.Err = &providers.Error{Provider: "The Movie Database", Status: 500}
	if code, _ := le.get("/api/v1/metadata/tv/tmdb/100"); code != 502 {
		t.Errorf("metadata outage: %d", code)
	}
	if code, _ := le.get("/api/v1/search?media_type=tv&q=x"); code != 502 {
		t.Errorf("search outage: %d", code)
	}
	le.env.TMDB.Err = nil

	code, body = le.get("/api/v1/metadata/tv/tmdb/100")
	m := le.expect(code, 200, body, "metadata tv")
	if m["title"] != "Plain Show" || m["source"] != "tmdb" {
		t.Fatalf("metadata: %v", m)
	}
	code, body = le.get("/api/v1/metadata/tv/tmdb/100?season_number=1")
	if m = le.expect(code, 200, body, "metadata season"); m["season/1"] == nil {
		t.Fatalf("season metadata: %v", m)
	}
	code, body = le.get("/api/v1/metadata/movie/tmdb/300")
	if m = le.expect(code, 200, body, "metadata movie"); m["title"] != "Plain Movie" {
		t.Fatalf("movie metadata: %v", m)
	}
	code, body = le.get("/api/v1/metadata/anime_movie/tmdb/301")
	le.expect(code, 200, body, "metadata anime_movie")
}

// "score": null clears a score; an omitted score leaves it alone. A movie
// can be watched again (a rewatch starts a new run).
func TestLibraryScoreClearAndMovieRewatch(t *testing.T) {
	le := newLibraryEnv(t)
	le.addShows()
	code, body := le.post("/api/v1/library", map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300", "status": "Completed", "score": 7})
	e := le.expect(code, 200, body, "track")
	mid := libraryNum(e["id"])
	if e["score"] != 7.0 {
		t.Fatalf("score = %v", e["score"])
	}
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "movie", "instance_id": mid, "notes": "kept"})
	if e = le.expect(code, 200, body, "omit score"); e["score"] != 7.0 {
		t.Fatalf("omitted score changed it: %v", e["score"])
	}
	code, body = le.post("/api/v1/library", map[string]any{"media_type": "movie", "instance_id": mid, "score": nil})
	if e = le.expect(code, 200, body, "clear score"); e["score"] != nil {
		t.Fatalf("score not cleared: %v", e["score"])
	}

	code, body = le.post(fmt.Sprintf("/api/v1/library/movie/%d/watches", mid), map[string]any{"watched_at": "2026-01-02"})
	re := le.expect(code, 200, body, "rewatch")
	if libraryNum(re["id"]) == mid || re["status"] != "Completed" {
		t.Fatalf("rewatch should start a new completed run: %v", re)
	}
	if code, _ := le.post(fmt.Sprintf("/api/v1/library/movie/%d/watches", mid), map[string]any{"watched_at": "yesterday"}); code != 422 {
		t.Errorf("bad date: %d", code)
	}
}

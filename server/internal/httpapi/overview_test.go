package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

var overviewT0 = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

// overviewFx is a server over a temp DB with fake providers and a fixed clock,
// one admin user "alice" (time zone UTC) logged in.
type overviewFx struct {
	t   *testing.T
	ts  *httptest.Server
	s   *Server
	env *catalogtest.Env
	lib *library.Service
	uid int64
	c   *client
}

func newOverviewFx(t *testing.T) *overviewFx {
	t.Helper()
	env := catalogtest.NewEnv(t, overviewT0)
	lib := library.New(env.Conn, env.Q, env.Catalog)
	lib.Now = env.Now
	s := &Server{Config: config.Config{}, DB: env.Conn, Queries: env.Q, Auth: auth.NewService(env.Q, 14),
		Catalog: env.Catalog, Library: lib, Web: fstest.MapFS{"200.html": {Data: []byte("<html></html>")}}}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	u, err := s.Auth.CreateUser(context.Background(), "alice", "password-1", true)
	if err != nil {
		t.Fatal(err)
	}
	f := &overviewFx{t: t, ts: ts, s: s, env: env, lib: lib, uid: u.ID}
	f.c = f.login("alice", "password-1")
	return f
}

func (f *overviewFx) login(user, password string) *client {
	f.t.Helper()
	c := newClient(f.t, f.ts.URL)
	_, body, _ := c.do("GET", "/api/v1/auth/csrf", nil, nil)
	c.csrf = body["csrf"].(string)
	code, body, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": password}, nil)
	if code != 200 {
		f.t.Fatalf("login %s: %d %v", user, code, body)
	}
	c.csrf = body["csrf"].(string)
	return c
}

func (f *overviewFx) setTZ(tz string) {
	f.t.Helper()
	if _, err := f.env.Conn.Exec(`UPDATE users SET timezone = ? WHERE id = ?`, tz, f.uid); err != nil {
		f.t.Fatal(err)
	}
}

func (f *overviewFx) show(id, title string, seasons ...catalogtest.Season) db.Item {
	f.t.Helper()
	f.env.TMDB.AddShow(id, title, seasons...)
	item, err := f.env.Catalog.EnsureItem(context.Background(), "tv", "tmdb", id)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.env.Catalog.SyncSeasons(context.Background(), item, catalog.Force); err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f *overviewFx) movie(id, title string) db.Item {
	f.t.Helper()
	f.env.TMDB.AddMovie(id, title, "2020-01-01")
	item, err := f.env.Catalog.EnsureItem(context.Background(), "movie", "tmdb", id)
	if err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f *overviewFx) countItem(kind, source, id string, total int) db.Item {
	f.t.Helper()
	item, err := f.env.Q.InsertItem(context.Background(), db.InsertItemParams{
		Kind: kind, Source: source, ExternalID: id, Title: kind + " " + id, Meta: `{"genres":["Drama"]}`,
		Total: sql.NullInt64{Int64: int64(total), Valid: total > 0},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f *overviewFx) watch(item db.Item, season, ep int, at time.Time) {
	f.t.Helper()
	if _, err := f.lib.WatchEpisode(context.Background(), f.uid, item.ID, season, ep, at, library.SourceManual, library.WatchOptions{}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *overviewFx) watchMovie(item db.Item, at time.Time) {
	f.t.Helper()
	if _, err := f.lib.WatchMovie(context.Background(), f.uid, item.ID, at, library.SourceManual, library.WatchOptions{}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *overviewFx) progress(item db.Item, n int, at time.Time) {
	f.t.Helper()
	if err := f.lib.SetProgress(context.Background(), f.uid, item.ID, n, library.SourceManual, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *overviewFx) readSession(start time.Time, pages int, minutes int) {
	f.t.Helper()
	_, err := f.env.Conn.Exec(`INSERT INTO reading_sessions (user_id, source, title, started_at, ended_at, duration_seconds, pages_read) VALUES (?, 'koreader', 'Book', ?, ?, ?, ?)`,
		f.uid, start.UTC().Format(time.RFC3339), start.Add(time.Duration(minutes)*time.Minute).UTC().Format(time.RFC3339), minutes*60, pages)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *overviewFx) token(scope string) map[string]string {
	f.t.Helper()
	nt, err := f.s.Auth.CreateToken(context.Background(), f.uid, "t-"+scope, scope)
	if err != nil {
		f.t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + nt.Key}
}

func getObj(t *testing.T, c *client, path string, hdr map[string]string) map[string]any {
	t.Helper()
	code, obj, _ := c.do("GET", path, nil, hdr)
	if code != 200 {
		t.Fatalf("GET %s: %d %v", path, code, obj)
	}
	return obj
}

func num(v any) int { f, _ := v.(float64); return int(f) }

func TestOverviewAuth(t *testing.T) {
	f := newOverviewFx(t)
	anon := newClient(t, f.ts.URL)
	bearer := newClient(t, f.ts.URL)
	read := f.token("read")
	for _, p := range []string{"/me", "/summary", "/now", "/now/finished", "/activity", "/years", "/years/2024", "/settings", "/users"} {
		if code, _, _ := anon.do("GET", "/api/v1"+p, nil, nil); code != 401 {
			t.Errorf("anon %s: %d", p, code)
		}
	}
	for _, p := range []string{"/me", "/summary", "/now", "/now/finished", "/activity", "/years", "/years/2024"} {
		if code, _, _ := bearer.do("GET", "/api/v1"+p, nil, read); code != 200 {
			t.Errorf("bearer read %s: %d", p, code)
		}
	}
	me := getObj(t, bearer, "/api/v1/me", read)
	if me["auth"] != "token" || me["scope"] != "read" || me["username"] != "alice" || me["timezone"] != "UTC" {
		t.Errorf("me via token = %v", me)
	}
	me = getObj(t, f.c, "/api/v1/me", nil)
	if me["auth"] != "session" || me["scope"] != "write" {
		t.Errorf("me via session = %v", me)
	}

	// Settings and user management reject bearer tokens, even write tokens.
	write := f.token("write")
	for _, hdr := range []map[string]string{read, write} {
		for _, tc := range [][2]string{{"GET", "/settings"}, {"PATCH", "/settings"}, {"POST", "/settings/password"},
			{"POST", "/settings/webhook-token"}, {"GET", "/users"}, {"POST", "/users"}, {"PATCH", "/users/1"}} {
			if code, _, _ := bearer.do(tc[0], "/api/v1"+tc[1], map[string]any{}, hdr); code != 403 {
				t.Errorf("bearer %s %s: %d, want 403", tc[0], tc[1], code)
			}
		}
	}
	// Unsafe session requests need CSRF.
	f.c.csrf = ""
	for _, tc := range [][2]string{{"PATCH", "/settings"}, {"POST", "/settings/password"}, {"POST", "/settings/webhook-token"}, {"POST", "/users"}, {"PATCH", "/users/1"}} {
		if code, _, _ := f.c.do(tc[0], "/api/v1"+tc[1], map[string]any{}, nil); code != 403 {
			t.Errorf("no csrf %s %s: %d, want 403", tc[0], tc[1], code)
		}
	}
}

func TestOverviewNowAndFinished(t *testing.T) {
	f := newOverviewFx(t)
	show := f.show("100", "The Show", catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: catalogtest.Eps(1, 3, "2020-01-01")})
	manga := f.countItem("manga", "mal", "7", 100)
	movie := f.movie("500", "A Film")
	f.env.Advance(time.Minute)
	f.watch(show, 1, 1, f.env.Now())
	f.env.Advance(time.Minute)
	f.progress(manga, 40, f.env.Now())
	f.env.Advance(time.Minute)
	f.watchMovie(movie, f.env.Now())

	now := getObj(t, f.c, "/api/v1/now", nil)
	groups := now["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("groups = %v", groups)
	}
	g0 := groups[0].(map[string]any) // manga was touched last
	if g0["media_type"] != "manga" {
		t.Errorf("first group = %v", g0["media_type"])
	}
	m := g0["entries"].([]any)[0].(map[string]any)
	if num(m["progress"]) != 40 || num(m["total"]) != 100 || num(m["percent"]) != 40 || m["media_type"] != "manga" {
		t.Errorf("manga entry = %v", m)
	}
	if next := m["next"].(map[string]any); num(next["number"]) != 41 || next["title"] != nil {
		t.Errorf("manga next = %v", next)
	}
	g1 := groups[1].(map[string]any)
	s := g1["entries"].([]any)[0].(map[string]any)
	if g1["media_type"] != "tv" || s["media_type"] != "season" || num(s["season_number"]) != 1 || num(s["tv_id"]) == 0 || num(s["total"]) != 3 {
		t.Errorf("season entry = %v", s)
	}
	if next := s["next"].(map[string]any); num(next["number"]) != 2 || next["title"] != "Episode 2" || next["air_date"] != "2020-01-01" {
		t.Errorf("season next = %v", next)
	}
	item := s["item"].(map[string]any)
	if item["media_type"] != "season" || num(item["season_number"]) != 1 || item["media_id"] != "100" {
		t.Errorf("season item = %v", item)
	}

	// Disabled kinds vanish from /now.
	if code, body, _ := f.c.do("PATCH", "/api/v1/settings", map[string]any{"media_types": []map[string]any{{"kind": "manga", "enabled": false}}}, nil); code != 200 {
		t.Fatalf("patch: %d %v", code, body)
	}
	now = getObj(t, f.c, "/api/v1/now", nil)
	if gs := now["groups"].([]any); len(gs) != 1 || gs[0].(map[string]any)["media_type"] != "tv" {
		t.Errorf("groups after disabling manga = %v", gs)
	}

	// Finished: the movie, newest first; limit is clamped, bad limits are 422.
	code, _, arr := f.c.do("GET", "/api/v1/now/finished?limit=14", nil, nil)
	if code != 200 || len(arr) != 1 {
		t.Fatalf("finished: %d %v", code, arr)
	}
	fin := arr[0].(map[string]any)
	if fin["media_type"] != "movie" || fin["score"] != nil || fin["end_date"] == "" || fin["item"].(map[string]any)["title"] != "A Film" {
		t.Errorf("finished = %v", fin)
	}
	if code, _, _ := f.c.do("GET", "/api/v1/now/finished?limit=abc", nil, nil); code != 422 {
		t.Errorf("limit=abc: %d", code)
	}
	if code, _, arr := f.c.do("GET", "/api/v1/now/finished?limit=0", nil, nil); code != 200 || len(arr) != 1 {
		t.Errorf("limit=0 clamps to 1: %d %v", code, arr)
	}
}

func TestOverviewSummary(t *testing.T) {
	f := newOverviewFx(t)
	show := f.show("100", "The Show", catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: catalogtest.Eps(1, 3, "2020-01-01")})
	manga := f.countItem("manga", "mal", "7", 100)
	comic := f.countItem("comic", "comicvine", "9", 10)
	movie := f.movie("500", "A Film")
	old := f.movie("501", "Old Film")
	f.watch(show, 1, 1, overviewT0)
	f.progress(manga, 5, overviewT0)
	f.progress(comic, 2, overviewT0)
	f.watchMovie(movie, overviewT0.AddDate(0, 0, -3))
	f.watchMovie(old, overviewT0.AddDate(0, 0, -90))
	// reading: today (June 1), yesterday and a day long ago
	f.readSession(overviewT0.Add(-2*time.Hour), 30, 20)
	f.readSession(overviewT0.AddDate(0, 0, -1), 10, 5)
	f.readSession(overviewT0.AddDate(0, 0, -40), 7, 3)

	sum := getObj(t, f.c, "/api/v1/summary", nil)
	ip := sum["in_progress"].(map[string]any)
	for _, k := range []string{"tv", "movie", "anime", "anime_movie", "manga", "book", "comic", "game"} {
		if _, ok := ip[k]; !ok {
			t.Errorf("in_progress lacks %q", k)
		}
	}
	if _, ok := ip["boardgame"]; ok {
		t.Errorf("boardgame should not be in the summary")
	}
	if len(ip["manga"].([]any)) != 1 || len(ip["tv"].([]any)) != 1 {
		t.Errorf("in_progress = %v", ip)
	}
	entry := ip["manga"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "media_type", "item", "status", "score", "progress", "progressed_at", "start_date", "end_date", "notes", "created_at"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("entry lacks %q: %v", k, entry)
		}
	}
	rc := sum["recently_completed"].([]any)
	if len(rc) != 1 || rc[0].(map[string]any)["item"].(map[string]any)["title"] != "A Film" {
		t.Errorf("recently_completed = %v", rc)
	}
	if wide := getObj(t, f.c, "/api/v1/summary?recent_days=365", nil); len(wide["recently_completed"].([]any)) != 2 {
		t.Errorf("recent_days=365: %v", wide["recently_completed"])
	}
	if code, _, _ := f.c.do("GET", "/api/v1/summary?recent_days=x", nil, nil); code != 422 {
		t.Errorf("recent_days=x: %d", code)
	}

	rd := sum["reading"].(map[string]any)
	days := rd["last_7_days"].([]any)
	if len(days) != 7 || days[6].(map[string]any)["date"] != "2024-06-01" || days[0].(map[string]any)["date"] != "2024-05-26" {
		t.Errorf("last_7_days = %v", days)
	}
	if rd["read_today"] != true || num(rd["pages_last_7_days"]) != 40 || num(rd["current_streak"]) != 2 || num(rd["longest_streak"]) != 2 {
		t.Errorf("reading = %v", rd)
	}
	if today := rd["today"].(map[string]any); num(today["pages"]) != 30 || num(today["minutes"]) != 20 || num(today["sessions"]) != 1 {
		t.Errorf("today = %v", today)
	}
	if ls := rd["last_session"].(map[string]any); ls["title"] != "Book" || ls["item"] != nil || num(ls["minutes"]) != 20 {
		t.Errorf("last_session = %v", ls)
	}

	// A disabled type is omitted entirely.
	if code, body, _ := f.c.do("PATCH", "/api/v1/settings", map[string]any{"media_types": []map[string]any{{"kind": "comic", "enabled": false}}}, nil); code != 200 {
		t.Fatalf("patch: %d %v", code, body)
	}
	sum = getObj(t, f.c, "/api/v1/summary", nil)
	if _, ok := sum["in_progress"].(map[string]any)["comic"]; ok {
		t.Errorf("comic still in summary")
	}
}

func TestOverviewSummaryEmptyReading(t *testing.T) {
	f := newOverviewFx(t)
	sum := getObj(t, f.c, "/api/v1/summary", nil)
	rd := sum["reading"].(map[string]any)
	if rd["last_session"] != nil || rd["read_today"] != false || num(rd["current_streak"]) != 0 {
		t.Errorf("reading = %v", rd)
	}
}

func TestOverviewYearBoundariesInUserZone(t *testing.T) {
	f := newOverviewFx(t)
	f.setTZ("America/Toronto") // UTC-5 in winter
	movie := f.movie("500", "New Year Film")
	later := f.movie("501", "Later Film")
	show := f.show("100", "The Show", catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: catalogtest.Eps(1, 3, "2020-01-01")})
	// 2024-01-01T03:00Z is still 2023-12-31 22:00 in Toronto.
	f.watchMovie(movie, time.Date(2024, 1, 1, 3, 0, 0, 0, time.UTC))
	f.watchMovie(later, time.Date(2024, 1, 1, 6, 0, 0, 0, time.UTC)) // 01:00 Jan 1 local
	f.watch(show, 1, 1, time.Date(2024, 1, 1, 4, 59, 0, 0, time.UTC))
	f.watch(show, 1, 2, time.Date(2024, 1, 1, 5, 1, 0, 0, time.UTC))
	f.readSession(time.Date(2024, 1, 1, 4, 0, 0, 0, time.UTC), 11, 10) // Dec 31 local

	_, _, years := f.c.do("GET", "/api/v1/years", nil, nil)
	if fmt.Sprint(years) != "[2024 2023]" {
		t.Errorf("years = %v", years)
	}
	y23 := getObj(t, f.c, "/api/v1/years/2023", nil)
	y24 := getObj(t, f.c, "/api/v1/years/2024", nil)
	if got := len(y23["finished"].([]any)); got != 1 {
		t.Errorf("2023 finished = %d", got)
	}
	if got := len(y24["finished"].([]any)); got != 1 || y24["finished"].([]any)[0].(map[string]any)["item"].(map[string]any)["title"] != "Later Film" {
		t.Errorf("2024 finished = %v", y24["finished"])
	}
	tv23 := y23["sections"].(map[string]any)["tv"].(map[string]any)
	tv24 := y24["sections"].(map[string]any)["tv"].(map[string]any)
	if num(tv23["units"]) != 1 || num(tv24["units"]) != 1 || num(tv23["by_month"].([]any)[11]) != 1 || num(tv24["by_month"].([]any)[0]) != 1 {
		t.Errorf("tv sections: %v / %v", tv23, tv24)
	}
	if num(tv24["minutes"]) != 24 || num(y24["totals"].(map[string]any)["watch_minutes"]) != 24 {
		t.Errorf("minutes = %v", tv24["minutes"])
	}
	r23 := y23["reading"].(map[string]any)
	if num(r23["pages"]) != 11 || num(r23["days_in_range"]) != 365 {
		t.Errorf("2023 reading = pages %v days %v", r23["pages"], r23["days_in_range"])
	}
	if bd := r23["best_day"].(map[string]any); bd["date"] != "2023-12-31" {
		t.Errorf("best day = %v", bd)
	}
	if r24 := y24["reading"].(map[string]any); num(r24["pages"]) != 0 || r24["best_day"] != nil || num(r24["days_in_range"]) != 153 {
		t.Errorf("2024 reading = %v", r24)
	}
	// A year in the future has no reading section; one with nothing is still valid.
	if y := getObj(t, f.c, "/api/v1/years/2030", nil); y["reading"] != nil || len(y["sections"].(map[string]any)) != 0 {
		t.Errorf("2030 = %v", y)
	}
	if code, _, _ := f.c.do("GET", "/api/v1/years/abc", nil, nil); code != 422 {
		t.Errorf("years/abc: %d", code)
	}
}

func TestOverviewYearSections(t *testing.T) {
	f := newOverviewFx(t)
	manga := f.countItem("manga", "mal", "7", 100)
	animeMovie := f.countItem("anime_movie", "tmdb", "900", 1)
	movie := f.movie("500", "Film")
	f.progress(manga, 10, time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC))
	f.progress(manga, 25, time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC))
	f.progress(manga, 100, time.Date(2024, 3, 11, 0, 0, 0, 0, time.UTC))
	f.watchMovie(animeMovie, time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC))
	f.watchMovie(movie, time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))
	if err := f.lib.SetScore(context.Background(), f.uid, movie.ID, ptrFloat(9), library.SourceManual); err != nil {
		t.Fatal(err)
	}
	if err := f.lib.SetScore(context.Background(), f.uid, animeMovie.ID, ptrFloat(7), library.SourceManual); err != nil {
		t.Fatal(err)
	}

	y := getObj(t, f.c, "/api/v1/years/2024", nil)
	secs := y["sections"].(map[string]any)
	mg := secs["manga"].(map[string]any)
	if num(mg["units"]) != 100 || num(mg["titles"]) != 1 || num(mg["finished"]) != 1 || num(mg["started"]) != 1 || num(mg["by_month"].([]any)[2]) != 90 {
		t.Errorf("manga = %v", mg)
	}
	if top := mg["top"].([]any); len(top) != 1 || num(top[0].(map[string]any)["count"]) != 100 {
		t.Errorf("manga top = %v", top)
	}
	for _, k := range []string{"movie", "anime_movie"} {
		sec, ok := secs[k].(map[string]any)
		if !ok || num(sec["units"]) != 1 || len(sec["top"].([]any)) != 0 {
			t.Errorf("%s section = %v", k, secs[k])
		}
	}
	if _, ok := secs["tv"]; ok {
		t.Errorf("unexpected tv section")
	}
	tr := y["top_rated"].([]any)
	if len(tr) != 2 || tr[0].(map[string]any)["item"].(map[string]any)["title"] != "Film" {
		t.Errorf("top_rated = %v", tr)
	}
	totals := y["totals"].(map[string]any)
	if num(totals["finished"]) != 3 || num(totals["titles"]) != 3 {
		t.Errorf("totals = %v", totals)
	}
	genres := y["genres"].([]any)
	if len(genres) == 0 || genres[0].(map[string]any)["name"] == "" {
		t.Errorf("genres = %v", genres)
	}

	// Disabling a kind drops its section and finishes.
	f.c.do("PATCH", "/api/v1/settings", map[string]any{"media_types": []map[string]any{{"kind": "manga", "enabled": false}}}, nil)
	y = getObj(t, f.c, "/api/v1/years/2024", nil)
	if _, ok := y["sections"].(map[string]any)["manga"]; ok || num(y["totals"].(map[string]any)["finished"]) != 2 {
		t.Errorf("after disabling: %v", y)
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestOverviewActivity(t *testing.T) {
	f := newOverviewFx(t)
	manga := f.countItem("manga", "mal", "7", 100)
	show := f.show("100", "The Show", catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: catalogtest.Eps(1, 3, "2020-01-01")})
	f.progress(manga, 10, time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC))
	f.progress(manga, 30, time.Date(2024, 5, 2, 10, 0, 0, 0, time.UTC))
	f.watch(show, 1, 2, time.Date(2024, 5, 3, 10, 0, 0, 0, time.UTC))

	act := getObj(t, f.c, "/api/v1/activity", nil)
	entries := act["entries"].([]any)
	if len(entries) < 4 || act["next_cursor"] != nil {
		t.Fatalf("entries = %d, next = %v", len(entries), act["next_cursor"])
	}
	first := entries[0].(map[string]any)
	ch := first["changes"].([]any)[0].(map[string]any)
	if first["media_type"] != "episode" || ch["description"] != "Watched episode 2" || ch["field"] != "end_date" {
		t.Errorf("first = %v", first)
	}
	if it := first["item"].(map[string]any); num(it["season_number"]) != 1 || num(it["episode_number"]) != 2 {
		t.Errorf("episode item = %v", it)
	}
	var progress map[string]any
	for _, e := range entries {
		for _, c := range e.(map[string]any)["changes"].([]any) {
			if c.(map[string]any)["field"] == "progress" && num(c.(map[string]any)["new"]) == 30 {
				progress = c.(map[string]any)
			}
		}
	}
	if progress == nil || progress["description"] != "Progress set to 30 chapters" {
		t.Errorf("progress change = %v", progress)
	}

	// Paging with the cursor walks every row once.
	seen := 0
	path := "/api/v1/activity?limit=2"
	for i := 0; i < 20; i++ {
		page := getObj(t, f.c, path, nil)
		seen += len(page["entries"].([]any))
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		path = "/api/v1/activity?limit=2&cursor=" + strings.ReplaceAll(next, "|", "%7C")
	}
	if seen != len(entries) {
		t.Errorf("paged %d rows, want %d", seen, len(entries))
	}

	// start/end are inclusive local days.
	day := getObj(t, f.c, "/api/v1/activity?start=2024-05-02&end=2024-05-02", nil)
	for _, e := range day["entries"].([]any) {
		if !strings.HasPrefix(e.(map[string]any)["date"].(string), "2024-05-02") {
			t.Errorf("entry outside the day: %v", e)
		}
	}
	if len(day["entries"].([]any)) == 0 {
		t.Errorf("no entries on 2024-05-02")
	}
	for _, bad := range []string{"start=nope", "end=2024-13-01", "cursor=garbage", "limit=x"} {
		if code, _, _ := f.c.do("GET", "/api/v1/activity?"+bad, nil, nil); code != 422 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

func TestOverviewChangeDescriptions(t *testing.T) {
	cases := []struct {
		kind string
		c    library.Change
		want string
	}{
		{"manga", library.Change{Field: "created", New: "Planning"}, "Added to reading list"},
		{"tv", library.Change{Field: "status", Old: "Planning", New: "In progress"}, "Currently watching"},
		{"game", library.Change{Field: "status", Old: "In progress", New: "Dropped"}, "Stopped playing"},
		{"tv", library.Change{Field: "status", Old: "Completed", New: "Planning"}, "Changed status from Completed to Planning"},
		{"movie", library.Change{Field: "score", New: "8"}, "Rated 8.0/10"},
		{"movie", library.Change{Field: "score", Old: "8", New: "9.5"}, "Changed rating from 8 to 9.5"},
		{"game", library.Change{Field: "progress", Old: "60", New: "150"}, "Added 1:30 of playtime"},
		{"book", library.Change{Field: "progress", Old: "0", New: "1"}, "Progress set to 1 page"},
		{"anime", library.Change{Field: "progress", Old: "1", New: "5"}, "Progress set to 5 episodes"},
	}
	for _, tc := range cases {
		if got := overviewChange(tc.kind, tc.c)["description"]; got != tc.want {
			t.Errorf("%s %+v = %q, want %q", tc.kind, tc.c, got, tc.want)
		}
	}
}

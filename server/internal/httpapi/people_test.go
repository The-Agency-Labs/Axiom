package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/auth"
	"github.com/the-agency-labs/axiom/server/internal/cache"
	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
)

type peopleFakeCredits struct{ data map[string]map[string]any }

func (f *peopleFakeCredits) Credits(_ context.Context, kind, id string) (map[string]any, error) {
	return f.data[kind+":"+id], nil
}

type peopleEnv struct {
	*catalogtest.Env
	ts    *httptest.Server
	key   string
	user  int64
	creds *peopleFakeCredits
}

func newPeopleEnv(t *testing.T) *peopleEnv {
	t.Helper()
	env := catalogtest.NewEnv(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	creds := &peopleFakeCredits{data: map[string]map[string]any{}}
	env.Catalog.TMDBCredits = creds
	svc := auth.NewService(env.Q, 14)
	s := &Server{
		Config: config.Config{}, DB: env.Conn, Queries: env.Q, Auth: svc, Catalog: env.Catalog,
		Jobs: jobs.New(env.Conn, env.Q, time.UTC), Cache: cache.New(env.Q, 0),
		Web: fstest.MapFS{"200.html": {Data: []byte("<html></html>")}},
	}
	uid := env.User(t, "alice")
	tok, err := svc.CreateToken(context.Background(), uid, "t", auth.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	return &peopleEnv{Env: env, ts: ts, key: tok.Key, user: uid, creds: creds}
}

func (e *peopleEnv) get(t *testing.T, path string, authed bool) (int, any) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
	if authed {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return resp.StatusCode, v
}

func (e *peopleEnv) exec(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	res, err := e.Conn.Exec(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *peopleEnv) item(t *testing.T, kind, source, ext, title string) int64 {
	return e.exec(t, `INSERT INTO items (kind, source, external_id, title, image) VALUES (?, ?, ?, ?, 'img')`, kind, source, ext, title)
}

func (e *peopleEnv) entry(t *testing.T, item int64, status string) int64 {
	return e.exec(t, `INSERT INTO entries (user_id, item_id, run, status, progress, created_at, updated_at)
		VALUES (?, ?, 1, ?, 0, '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z')`, e.user, item, status)
}

func (e *peopleEnv) person(t *testing.T, source, ext, name string) int64 {
	return e.exec(t, `INSERT INTO people (source, external_id, name, image) VALUES (?, ?, ?, '')`, source, ext, name)
}

func (e *peopleEnv) credit(t *testing.T, item, person int64, role, character, job, lang string, ord int) {
	e.exec(t, `INSERT INTO credits (item_id, person_id, role, character, job, language, character_role, ord)
		VALUES (?, ?, ?, ?, ?, ?, 'MAIN', ?)`, item, person, role, character, job, lang, ord)
}

func (e *peopleEnv) fetched(t *testing.T, item int64) {
	e.exec(t, `INSERT INTO credit_fetches (item_id, fetched_at) VALUES (?, '2026-01-01T00:00:00Z')`, item)
}

func obj(v any) map[string]any { return v.(map[string]any) }
func arr(v any) []any          { return v.([]any) }

func (e *peopleEnv) queuedCredits(t *testing.T) int {
	var n int
	if err := e.Conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, catalog.JobCredits).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPeopleRequireAuth(t *testing.T) {
	e := newPeopleEnv(t)
	for _, p := range []string{"/api/v1/people", "/api/v1/people/1", "/api/v1/people/search/by-name?q=ab", "/api/v1/credits/tv/tmdb/1"} {
		if code, _ := e.get(t, p, false); code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", p, code)
		}
	}
}

// seedLibrary: three watched titles (one anime with voice actors), one planned,
// one watched title whose credits were never fetched.
func seedLibrary(t *testing.T, e *peopleEnv) (a, b, c int64) {
	i1 := e.item(t, "tv", "tmdb", "1", "Show One")
	i2 := e.item(t, "movie", "tmdb", "2", "Movie Two")
	i3 := e.item(t, "anime", "tmdb", "3", "Anime Three")
	i4 := e.item(t, "tv", "tmdb", "4", "Planned Four")
	i5 := e.item(t, "tv", "tmdb", "5", "Unfetched Five")
	i6 := e.item(t, "manga", "mangaupdates", "6", "Manga Six") // not cast-bearing
	for _, i := range []int64{i1, i2, i3, i4} {
		e.fetched(t, i)
	}
	e.entry(t, i1, "Completed")
	e.entry(t, i2, "In progress")
	e.entry(t, i3, "Completed")
	e.entry(t, i4, "Planning")
	e.entry(t, i5, "In progress")
	e.entry(t, i6, "Completed")

	a = e.person(t, "tmdb", "10", "Ann Actor")
	b = e.person(t, "tmdb", "11", "Bob Actor")
	c = e.person(t, "anilist", "12", "Cho Voice")
	e.credit(t, i1, a, "cast", "Hero", "", "", 0)
	e.credit(t, i2, a, "cast", "Hero", "", "", 0)
	e.credit(t, i2, a, "cast", "Villain", "", "", 1)
	e.credit(t, i3, a, "voice", "Kid", "", "english", 2)
	e.credit(t, i1, b, "cast", "Friend", "", "", 1)
	e.credit(t, i2, b, "crew", "", "Director", "", 2)
	e.credit(t, i4, b, "cast", "Planned only", "", "", 3)
	e.credit(t, i3, c, "voice", "Kid", "", "japanese", 3)
	e.credit(t, i1, c, "cast", "Cameo", "", "", 4)
	return a, b, c
}

func TestPeopleRecurring(t *testing.T) {
	e := newPeopleEnv(t)
	a, b, c := seedLibrary(t, e)

	code, v := e.get(t, "/api/v1/people", true)
	if code != 200 {
		t.Fatalf("status %d: %v", code, v)
	}
	res := obj(v)
	if res["library_titles"] != float64(4) || res["pending"] != float64(1) {
		t.Errorf("library_titles/pending = %v/%v, want 4/1", res["library_titles"], res["pending"])
	}
	results := arr(res["results"])
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3 (planned-only credit must not count): %v", len(results), results)
	}
	// Ann: 3 titles; Bob: 2 (planned excluded); Cho: 2 -> name order Bob < Cho.
	wantOrder := []int64{a, b, c}
	for i, w := range wantOrder {
		if got := int64(obj(obj(results[i])["person"])["id"].(float64)); got != w {
			t.Errorf("results[%d] person %d, want %d", i, got, w)
		}
	}
	ann := obj(results[0])
	if ann["titles"] != float64(3) {
		t.Errorf("ann titles %v", ann["titles"])
	}
	apps := arr(ann["appearances"])
	if len(apps) != 3 {
		t.Fatalf("ann appearances %d", len(apps))
	}
	movie := obj(apps[1])
	chars := arr(movie["characters"])
	if len(chars) != 2 || chars[0] != "Hero" || chars[1] != "Villain" {
		t.Errorf("movie characters %v", chars)
	}
	if obj(movie["item"])["media_type"] != "movie" || obj(movie["entry"])["status"] != "In progress" || movie["role"] != "actor" {
		t.Errorf("movie appearance %v", movie)
	}
	if obj(movie["item"])["season_number"] != nil {
		t.Errorf("season_number should be null")
	}
	if url := obj(obj(results[2])["person"])["url"]; url != "https://anilist.co/staff/12" {
		t.Errorf("anilist url %v", url)
	}
	if url := obj(ann["person"])["url"]; url != "https://www.themoviedb.org/person/10" {
		t.Errorf("tmdb url %v", url)
	}
	if obj(ann["person"])["image"] != nil {
		t.Errorf("empty image must be null")
	}

	// Pending kicked a backfill job once; the 10 minute lock suppresses a second.
	if n := e.queuedCredits(t); n != 1 {
		t.Errorf("queued credit jobs = %d, want 1", n)
	}
	e.get(t, "/api/v1/people", true)
	if n := e.queuedCredits(t); n != 1 {
		t.Errorf("queued credit jobs after repeat = %d, want 1", n)
	}

	// min_titles=3 keeps only Ann; limit caps the list.
	_, v = e.get(t, "/api/v1/people?min_titles=3", true)
	if got := arr(obj(v)["results"]); len(got) != 1 {
		t.Errorf("min_titles=3 -> %d results", len(got))
	}
	_, v = e.get(t, "/api/v1/people?limit=1", true)
	if got := arr(obj(v)["results"]); len(got) != 1 || obj(obj(got[0])["person"])["id"] != float64(a) {
		t.Errorf("limit=1 -> %v", got)
	}
	// role filter: crew only Bob (1 title) -> min_titles 1
	_, v = e.get(t, "/api/v1/people?role=crew&min_titles=1", true)
	got := arr(obj(v)["results"])
	if len(got) != 1 || obj(got[0])["appearances"].([]any)[0].(map[string]any)["role"] != "crew" {
		t.Errorf("role=crew -> %v", got)
	}
	// voice actors in Japanese
	_, v = e.get(t, "/api/v1/people?role=voice&language=ja&min_titles=1", true)
	got = arr(obj(v)["results"])
	if len(got) != 1 || obj(obj(got[0])["person"])["id"] != float64(c) {
		t.Errorf("voice ja -> %v", got)
	}
	_, v = e.get(t, "/api/v1/people?role=voice&language=english&min_titles=1", true)
	got = arr(obj(v)["results"])
	if len(got) != 1 || obj(obj(got[0])["person"])["id"] != float64(a) {
		t.Errorf("voice english -> %v", got)
	}
	if code, _ := e.get(t, "/api/v1/people?limit=x", true); code != 422 {
		t.Errorf("bad limit status %d", code)
	}
}

func TestPeopleEmptyLibrary(t *testing.T) {
	e := newPeopleEnv(t)
	code, v := e.get(t, "/api/v1/people", true)
	res := obj(v)
	if code != 200 || res["pending"] != float64(0) || res["library_titles"] != float64(0) || len(arr(res["results"])) != 0 {
		t.Errorf("%d %v", code, v)
	}
	if n := e.queuedCredits(t); n != 0 {
		t.Errorf("no backfill expected, got %d", n)
	}
}

func TestPeoplePerson(t *testing.T) {
	e := newPeopleEnv(t)
	_, b, _ := seedLibrary(t, e)

	code, v := e.get(t, fmt.Sprintf("/api/v1/people/%d", b), true)
	if code != 200 {
		t.Fatalf("status %d: %v", code, v)
	}
	res := obj(v)
	if obj(res["person"])["name"] != "Bob Actor" {
		t.Errorf("person %v", res["person"])
	}
	in := arr(res["in_library"])
	if len(in) != 2 {
		t.Fatalf("in_library %v", in)
	}
	crew := obj(in[1])
	if crew["role"] != "crew" || arr(crew["characters"])[0] != "Director" {
		t.Errorf("crew row %v", crew)
	}
	out := arr(res["elsewhere"])
	if len(out) != 1 {
		t.Fatalf("elsewhere %v", out)
	}
	planned := obj(out[0])
	if obj(planned["entry"])["status"] != "Planning" || planned["language"] != "" || len(arr(planned["character_images"])) != len(arr(planned["characters"])) {
		t.Errorf("planned row %v", planned)
	}

	if code, v := e.get(t, "/api/v1/people/9999", true); code != 404 || obj(v)["detail"] != "Not Found" {
		t.Errorf("missing person: %d %v", code, v)
	}
	if code, _ := e.get(t, "/api/v1/people/abc", true); code != 422 {
		t.Errorf("non-int id: %d", code)
	}
}

func TestPeopleSearch(t *testing.T) {
	e := newPeopleEnv(t)
	e.person(t, "tmdb", "1", "Zed Alpha")
	e.person(t, "tmdb", "2", "alpha beta")
	e.person(t, "tmdb", "3", "100% Real")
	e.person(t, "tmdb", "4", "Nobody")

	code, v := e.get(t, "/api/v1/people/search/by-name?q=ALPHA", true)
	got := v.([]any)
	if code != 200 || len(got) != 2 || obj(got[0])["name"] != "Zed Alpha" || obj(got[1])["name"] != "alpha beta" {
		// binary name order: uppercase sorts before lowercase
		t.Errorf("alpha: %d %v", code, v)
	}
	_, v = e.get(t, "/api/v1/people/search/by-name?q=0%25", true)
	if got := v.([]any); len(got) != 1 || obj(got[0])["name"] != "100% Real" {
		t.Errorf("escaped percent: %v", v)
	}
	if code, _ := e.get(t, "/api/v1/people/search/by-name?q=%20a%20", true); code != 400 {
		t.Errorf("short query: %d", code)
	}
	if code, _ := e.get(t, "/api/v1/people/search/by-name", true); code != 422 {
		t.Errorf("missing q: %d", code)
	}
}

func TestPeopleTitleCredits(t *testing.T) {
	e := newPeopleEnv(t)
	e.TMDB.AddShow("77", "Fetched Show", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 2, "2020-01-01")})
	e.creds.data["tv:77"] = map[string]any{
		"cast": []any{
			map[string]any{"id": float64(10), "name": "Ann Actor", "profile_path": "/a.jpg", "total_episode_count": float64(5),
				"roles": []any{map[string]any{"character": "Hero"}}},
			map[string]any{"id": float64(13), "name": "Dan New", "roles": []any{map[string]any{"character": "Side"}}},
		},
		"crew": []any{
			map[string]any{"id": float64(20), "name": "Dir Ector",
				"jobs": []any{map[string]any{"job": "Director", "episode_count": float64(4)}}},
		},
	}
	// Ann is already in the library through another title.
	other := e.item(t, "movie", "tmdb", "2", "Movie Two")
	e.fetched(t, other)
	e.entry(t, other, "Completed")
	ann := e.person(t, "tmdb", "10", "Ann Actor")
	e.credit(t, other, ann, "cast", "Lead", "", "", 0)

	code, v := e.get(t, "/api/v1/credits/tv/tmdb/77", true)
	if code != 200 {
		t.Fatalf("status %d: %v", code, v)
	}
	res := obj(v)
	if res["supported"] != true {
		t.Fatalf("supported: %v", res)
	}
	cast := arr(res["cast"])
	if len(cast) != 2 {
		t.Fatalf("cast %v", cast)
	}
	first := obj(cast[0])
	if first["character"] != "Hero" || first["episode_count"] != float64(5) || obj(first["person"])["image"] != "https://image.tmdb.org/t/p/w185/a.jpg" {
		t.Errorf("first cast %v", first)
	}
	also := arr(first["also_in"])
	if len(also) != 1 || obj(also[0])["character"] != "Lead" || obj(obj(also[0])["item"])["title"] != "Movie Two" {
		t.Errorf("also_in %v", also)
	}
	if len(arr(obj(cast[1])["also_in"])) != 0 || obj(cast[1])["episode_count"] != nil {
		t.Errorf("second cast %v", cast[1])
	}
	crew := arr(res["crew"])
	if len(crew) != 1 || obj(crew[0])["job"] != "Director" || obj(obj(crew[0])["person"])["image"] != nil {
		t.Errorf("crew %v", crew)
	}
	if len(arr(res["characters"])) != 0 || len(arr(res["languages"])) != 0 {
		t.Errorf("characters/languages %v %v", res["characters"], res["languages"])
	}

	// Voice actors, grouped per character, with languages.
	show := e.item(t, "anime", "tmdb", "88", "Anime")
	e.fetched(t, show)
	jp := e.person(t, "anilist", "30", "Jun Seiyuu")
	en := e.person(t, "anilist", "31", "Eve Dub")
	e.credit(t, show, jp, "voice", "Kid", "", "japanese", 0)
	e.credit(t, show, en, "voice", "Kid", "", "english", 0)
	e.credit(t, show, jp, "voice", "Dad", "", "japanese", 1)
	_, v = e.get(t, "/api/v1/credits/anime/tmdb/88", true)
	res = obj(v)
	chars := arr(res["characters"])
	if len(chars) != 2 {
		t.Fatalf("characters %v", chars)
	}
	kid := obj(chars[0])
	if kid["character"] != "Kid" || kid["role"] != "main" || kid["image"] != nil || len(arr(kid["voice_actors"])) != 2 {
		t.Errorf("kid %v", kid)
	}
	if va := obj(arr(kid["voice_actors"])[1]); va["language"] != "english" {
		t.Errorf("voice actor %v", va)
	}
	langs := arr(res["languages"])
	if len(langs) != 2 || langs[0] != "english" || langs[1] != "japanese" {
		t.Errorf("languages %v", langs)
	}

	// Unsupported titles answer supported=false without touching providers.
	_, v = e.get(t, "/api/v1/credits/manga/mal/20", true)
	res = obj(v)
	if res["supported"] != false || len(arr(res["cast"])) != 0 || len(arr(res["crew"])) != 0 || len(arr(res["characters"])) != 0 {
		t.Errorf("unsupported %v", res)
	}
	if _, has := res["languages"]; has {
		t.Errorf("unsupported response must not carry languages")
	}

	// Unknown supported title: provider has nothing -> 502.
	if code, v := e.get(t, "/api/v1/credits/tv/tmdb/99999", true); code != 502 || obj(v)["detail"] != "Metadata unavailable" {
		t.Errorf("unknown title: %d %v", code, v)
	}
}

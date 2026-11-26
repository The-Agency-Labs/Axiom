package igdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

type fake struct {
	srv        *httptest.Server
	tokens     atomic.Int32
	queries    []string
	unauthOnce atomic.Bool
	status     int
	errBody    string
}

func newFake(t *testing.T, fixture string) (*fake, *Client) {
	t.Helper()
	f := &fake{}
	data, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			n := f.tokens.Add(1)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"grant_type":"client_credentials"`) {
				t.Errorf("token body = %s", body)
			}
			w.Write([]byte(`{"access_token":"tok` + string(rune('0'+n)) + `","expires_in":3600}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.queries = append(f.queries, string(body))
		if r.Header.Get("Client-Id") != "id" {
			t.Errorf("Client-Id = %q", r.Header.Get("Client-Id"))
		}
		if f.unauthOnce.Swap(false) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			w.Write([]byte(f.errBody))
			return
		}
		if r.Header.Get("Authorization") == "Bearer tok1" || r.Header.Get("Authorization") == "Bearer tok2" {
			w.Write(data)
			return
		}
		t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}))
	t.Cleanup(f.srv.Close)
	c := New(providers.NewClient(nil), config.Config{IGDBID: "id", IGDBSecret: "secret", TimeZone: "UTC"})
	c.BaseURL, c.TokenURL = f.srv.URL, f.srv.URL+"/token"
	return f, c
}

func TestSearch(t *testing.T) {
	f, c := newFake(t, "search.json")
	page, err := c.Search(context.Background(), `Elden "Ring"`, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if providers.Int(page, "page") != 2 || providers.Int(page, "total_results") != 48 || providers.Int(page, "total_pages") != 3 {
		t.Errorf("page = %v", page)
	}
	res := providers.List(page, "results")
	if len(res) != 2 || providers.Int(res[0], "media_id") != 119133 || res[0]["title"] != "Elden Ring" {
		t.Errorf("results = %v", res)
	}
	if res[0]["image"] != "https://images.igdb.com/igdb/image/upload/t_original/co4jni.jpg" || res[1]["image"] != ImgNone {
		t.Errorf("images = %v %v", res[0]["image"], res[1]["image"])
	}
	q := f.queries[0]
	for _, want := range []string{`name ~ *"Elden \"Ring\""*`, "offset 24;", "limit 24;", "& themes != (42)", `query games/count "TotalCount"`} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
}

func TestSearchNSFW(t *testing.T) {
	f, c := newFake(t, "search.json")
	c.cfg.IGDBNSFW = true
	if _, err := c.Search(context.Background(), "a", 1, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.queries[0], "themes != (42)") {
		t.Error("adult filter applied although NSFW is on")
	}
	if _, err := c.Search(context.Background(), "b", 1, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.queries[1], "themes != (42)") {
		t.Error("adult filter missing when show_adult_titles is false")
	}
}

func TestGame(t *testing.T) {
	f, c := newFake(t, "game.json")
	g, err := c.Game(context.Background(), "14593")
	if err != nil {
		t.Fatal(err)
	}
	if providers.Int(g, "media_id") != 14593 || g["title"] != "Hollow Knight" || g["score"] != 9.0 || providers.Int(g, "score_count") != 1500 {
		t.Errorf("game = %v", g)
	}
	d := g["details"].(map[string]any)
	if d["format"] != "Main game" || d["release_date"] != "2017-02-24" || d["companies"].([]any)[0] != "Team Cherry" {
		t.Errorf("details = %v", d)
	}
	rel := g["related"].(map[string]any)
	if rel["parent_game"].([]any) == nil || len(rel["parent_game"].([]any)) != 0 {
		t.Errorf("parent = %v", rel["parent_game"])
	}
	dlcs := rel["dlcs"].([]any)
	if len(dlcs) != 1 || dlcs[0].(map[string]any)["title"] != "Godmaster" {
		t.Errorf("dlcs = %v", dlcs)
	}
	if sim := rel["recommendations"].([]any); sim[0].(map[string]any)["image"] != ImgNone {
		t.Errorf("recs = %v", sim)
	}
	ttb := g["time_to_beat"].(map[string]any)
	if len(ttb) != 2 || ttb["hastily"] != float64(20000) {
		t.Errorf("ttb = %v", ttb)
	}
	if !strings.Contains(f.queries[0], "where id = 14593;") || !strings.Contains(f.queries[0], "where game_id = 14593;") {
		t.Errorf("query = %s", f.queries[0])
	}
}

func TestGameNotFound(t *testing.T) {
	_, c := newFake(t, "empty.json")
	_, err := c.Game(context.Background(), "1")
	if !providers.NotFound(err) || !strings.Contains(err.Error(), "No result found with ID 1") {
		t.Errorf("err = %v", err)
	}
}

func TestTokenCachedAndRefreshedOn401(t *testing.T) {
	f, c := newFake(t, "search.json")
	ctx := context.Background()
	for _, q := range []string{"a", "b"} {
		if _, err := c.Search(ctx, q, 1, true); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokens.Load() != 1 {
		t.Errorf("token fetched %d times, want 1", f.tokens.Load())
	}
	f.unauthOnce.Store(true)
	if _, err := c.Search(ctx, "c", 1, true); err != nil {
		t.Fatal(err)
	}
	if f.tokens.Load() != 2 {
		t.Errorf("token fetched %d times after 401, want 2", f.tokens.Load())
	}
}

func TestBadRequestDetails(t *testing.T) {
	f, c := newFake(t, "search.json")
	f.status, f.errBody = http.StatusForbidden, `{"message":"Authorization Failure"}`
	_, err := c.Search(context.Background(), "a", 1, true)
	if err == nil || !strings.Contains(err.Error(), "(HTTP 403): Authorization failure.") {
		t.Errorf("err = %v", err)
	}
}

func TestExternalGame(t *testing.T) {
	f, c := newFake(t, "external.json")
	id, err := c.ExternalGame(context.Background(), "1245620", SourceSteam)
	if err != nil || id != 119133 {
		t.Fatalf("id = %d, err = %v", id, err)
	}
	if want := `fields game; where uid = "1245620" & external_game_source = 1;`; f.queries[0] != want {
		t.Errorf("query = %s", f.queries[0])
	}
}

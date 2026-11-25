package bgg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

func serve(t *testing.T, files map[string]string, status int) (*Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		data, err := os.ReadFile("testdata/" + files[r.URL.Path])
		if err != nil {
			t.Errorf("no fixture for %s", r.URL.Path)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{BGGToken: "tok"})
	c.BaseURL = srv.URL
	return c, &seen
}

func TestSearch(t *testing.T) {
	c, seen := serve(t, map[string]string{"/search": "search.xml", "/thing": "thing.xml"}, 0)
	page, err := c.Search(context.Background(), "catan jr", 1)
	if err != nil {
		t.Fatal(err)
	}
	// the third item has no name and is skipped
	if providers.Int(page, "total_results") != 2 || providers.Int(page, "total_pages") != 1 || providers.Int(page, "page") != 1 {
		t.Errorf("page = %v", page)
	}
	res := providers.List(page, "results")
	if res[0]["media_id"] != "13" || res[0]["title"] != "CATAN" || res[1]["title"] != "Catan Junior" {
		t.Errorf("results = %v", res)
	}
	// thumbnails come from /thing; ids without one fall back to the placeholder
	if res[0]["image"] != ImgNone {
		t.Errorf("image = %v", res[0]["image"])
	}
	if !strings.Contains((*seen)[0], "query=catan+jr") || !strings.Contains((*seen)[0], "type=boardgame") {
		t.Errorf("search url = %s", (*seen)[0])
	}
	if !strings.Contains((*seen)[1], "id=13%2C134277") {
		t.Errorf("thing url = %s", (*seen)[1])
	}
}

func TestSearchPaging(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<items>`)
	for i := 1; i <= 45; i++ {
		b.WriteString(`<item id="` + string(rune('A'+i%26)) + `"><name value="n"/></item>`)
	}
	b.WriteString(`</items>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			w.Write([]byte(b.String()))
			return
		}
		w.Write([]byte(`<items/>`))
	}))
	defer srv.Close()
	c := New(providers.NewClient(nil), config.Config{})
	c.BaseURL = srv.URL
	page, err := c.Search(context.Background(), "x", 3)
	if err != nil {
		t.Fatal(err)
	}
	if providers.Int(page, "total_results") != 45 || providers.Int(page, "total_pages") != 3 || len(providers.List(page, "results")) != 5 {
		t.Errorf("page = %v", page)
	}
}

func TestThumbnailFailureIsSwallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			w.Write([]byte(`<items><item id="1"><name value="A"/></item></items>`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	c := New(providers.NewClient(nil), config.Config{})
	c.BaseURL = srv.URL
	page, err := c.Search(context.Background(), "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if providers.List(page, "results")[0]["image"] != ImgNone {
		t.Errorf("page = %v", page)
	}
}

func TestBoardgame(t *testing.T) {
	c, seen := serve(t, map[string]string{"/thing": "thing.xml"}, 0)
	g, err := c.Boardgame(context.Background(), "266192")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*seen)[0], "stats=1") {
		t.Errorf("url = %s", (*seen)[0])
	}
	want := map[string]any{
		"media_id": "266192", "source": "bgg", "media_type": "boardgame", "title": "Wingspan",
		"source_url": "https://boardgamegeek.com/boardgame/266192",
		"image":      "https://example.test/wingspan.jpg", "score": 8.0,
		"synopsis": "Attract a beautiful and diverse collection of birds.\n\nSecond paragraph & more.",
	}
	for k, v := range want {
		if g[k] != v {
			t.Errorf("%s = %#v, want %#v", k, g[k], v)
		}
	}
	if providers.Int(g, "score_count") != 102000 {
		t.Errorf("score_count = %v", g["score_count"])
	}
	if cats := g["genres"].([]any); len(cats) != 2 || cats[1] != "Card Game" {
		t.Errorf("genres = %v", g["genres"])
	}
	d := g["details"].(map[string]any)
	wantD := map[string]any{
		"year": "2019", "players": "1-5 players", "playtime": "70 min", "min_age": "10+",
		"designers": "Elizabeth Hargrave, Second Designer", "publishers": "Stonemaier Games, 999 Games, Angry Lion Games",
	}
	for k, v := range wantD {
		if d[k] != v {
			t.Errorf("details.%s = %#v, want %#v", k, d[k], v)
		}
	}
	if v, ok := g["max_progress"]; !ok || v != nil {
		t.Errorf("max_progress = %v, %v", v, ok)
	}
}

func TestBoardgameSparse(t *testing.T) {
	c, _ := serve(t, map[string]string{"/thing": "sparse.xml"}, 0)
	g, err := c.Boardgame(context.Background(), "5")
	if err != nil {
		t.Fatal(err)
	}
	d := g["details"].(map[string]any)
	if g["title"] != "Unknown" || g["image"] != ImgNone || g["synopsis"] != "No synopsis available" ||
		g["genres"] != nil || g["score"] != nil || g["score_count"] != nil ||
		d["year"] != nil || d["players"] != "2 players" || d["playtime"] != "0 min" || d["min_age"] != nil ||
		d["designers"] != nil || d["publishers"] != nil {
		t.Errorf("game = %v", g)
	}
}

func TestBoardgameNotFound(t *testing.T) {
	c, _ := serve(t, map[string]string{"/thing": "empty.xml"}, 0)
	_, err := c.Boardgame(context.Background(), "1")
	if !providers.NotFound(err) || !strings.Contains(err.Error(), "No result found with ID 1") {
		t.Errorf("err = %v", err)
	}
}

func TestUnauthorized(t *testing.T) {
	c, _ := serve(t, nil, http.StatusUnauthorized)
	_, err := c.Boardgame(context.Background(), "1")
	if err == nil || !strings.Contains(err.Error(), "(HTTP 401): BGG API requires authorization.") {
		t.Errorf("err = %v", err)
	}
}

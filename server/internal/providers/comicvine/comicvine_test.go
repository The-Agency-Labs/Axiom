package comicvine

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

func serve(t *testing.T, routes map[string]string) (*Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		if r.Header.Get("User-Agent") != "Mozilla/5.0" || r.URL.Query().Get("api_key") != "key" || r.URL.Query().Get("format") != "json" {
			t.Errorf("bad request: %s %v", r.URL, r.Header)
		}
		file, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if strings.HasPrefix(file, "!") {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(file[1:]))
			return
		}
		data, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Error(err)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{ComicVineKey: "key"})
	c.BaseURL = srv.URL
	return c, &seen
}

func TestSearch(t *testing.T) {
	c, seen := serve(t, map[string]string{"/search/": "search.json"})
	page, err := c.Search(context.Background(), "Saga", 2)
	if err != nil {
		t.Fatal(err)
	}
	if providers.Int(page, "total_results") != 30 || providers.Int(page, "total_pages") != 2 || providers.Int(page, "page") != 2 {
		t.Errorf("page = %v", page)
	}
	res := providers.List(page, "results")
	if res[0]["media_id"] != "46568" || res[0]["image"] != "https://example.test/saga.jpg" || res[1]["image"] != ImgNone {
		t.Errorf("results = %v", res)
	}
	q := (*seen)[0]
	for _, want := range []string{"resources=volume", "limit=24", "page=2", "query=Saga", "field_list=id%2Cname%2Cimage"} {
		if !strings.Contains(q, want) {
			t.Errorf("url missing %q: %s", want, q)
		}
	}
}

func TestComic(t *testing.T) {
	c, seen := serve(t, map[string]string{"/volume/4050-46568/": "volume.json", "/volumes/": "volumes.json"})
	m, err := c.Comic(context.Background(), "46568")
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]any{
		"media_id": "46568", "title": "Saga", "source_url": "https://comicvine.gamespot.com/saga/4050-46568/",
		"image": "https://example.test/saga.jpg", "synopsis": "Saga is a space opera & more. Collected Vol 1",
	}
	for k, v := range checks {
		if m[k] != v {
			t.Errorf("%s = %#v, want %#v", k, m[k], v)
		}
	}
	if providers.Int(m, "max_issue_number") != 72 || providers.Int(m, "last_issue_id") != 1000 {
		t.Errorf("issue fields = %v %v", m["max_issue_number"], m["last_issue_id"])
	}
	if g := m["genres"].([]any); len(g) != 5 {
		t.Errorf("genres = %v", g)
	}
	d := m["details"].(map[string]any)
	if d["start_date"] != "2012" || d["publisher"] != "Image" || d["last_issue_number"] != "72" ||
		d["last_updated"] != "2026-07-15" || d["last_issue_name"] != "Chapter Seventy-Two" || len(d["people"].([]any)) != 2 {
		t.Errorf("details = %v", d)
	}
	recs := m["related"].(map[string]any)["recommendations"].([]any)
	if len(recs) != 2 || recs[0].(map[string]any)["media_id"] != "100" {
		t.Errorf("recommendations = %v (current volume must be excluded)", recs)
	}
	if !strings.Contains((*seen)[1], "filter=publisher%3A513") || !strings.Contains((*seen)[1], "limit=16") {
		t.Errorf("publisher url = %s", (*seen)[1])
	}
}

func TestComicNotFound(t *testing.T) {
	c, _ := serve(t, map[string]string{"/volume/4050-1/": "empty.json"})
	_, err := c.Comic(context.Background(), "1")
	if !providers.NotFound(err) || !strings.Contains(err.Error(), "No result found with ID 1") {
		t.Errorf("err = %v", err)
	}
}

func TestIssue(t *testing.T) {
	c, _ := serve(t, map[string]string{"/issue/4000-9/": "issue.json"})
	m, err := c.Issue(context.Background(), "9")
	if err != nil {
		t.Fatal(err)
	}
	if m["cover_date"] != "2024-01-01" || m["store_date"] != nil {
		t.Errorf("issue = %v", m)
	}
}

func TestUnauthorizedDetails(t *testing.T) {
	c, _ := serve(t, map[string]string{"/search/": `!{"error":"Invalid API Key","status_code":100}`})
	_, err := c.Search(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "(HTTP 401): Invalid API Key.") {
		t.Errorf("err = %v", err)
	}
}

func TestIssueNumber(t *testing.T) {
	cases := map[string]any{"72": 72, "463-464": 464, "12-3": 12, "abc": nil, "-5": -5, "": nil}
	for in, want := range cases {
		if got := issueNumber(in); got != want {
			t.Errorf("issueNumber(%q) = %v, want %v", in, got, want)
		}
	}
	if issueNumber(nil) != nil {
		t.Error("nil issue number must stay nil")
	}
}

func TestSynopsis(t *testing.T) {
	cases := map[string]string{
		"":                               "No synopsis available",
		"<p>a</p><p>b</p>":               "a b",
		"x &lt; y &amp; z<br/>w":         "x < y & z w",
		"1 < 2 <i>ok</i>":                "1 < 2 ok",
		"<style>p{}</style>t<!-- c -->u": "t u",
		"<a href=\"x>y\">link</a> after": "link after",
	}
	for in, want := range cases {
		if got := synopsis(obj{"description": in}); got != want {
			t.Errorf("synopsis(%q) = %q, want %q", in, got, want)
		}
	}
}

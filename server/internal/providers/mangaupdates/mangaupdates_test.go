package mangaupdates

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/config"
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

func norm(t *testing.T, v any) any {
	t.Helper()
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func newClient(t *testing.T, nsfw bool, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{MUNSFW: nsfw})
	c.URL = srv.URL
	return c
}

func TestSearchExcludesAdultUnlessAllowed(t *testing.T) {
	for _, tc := range []struct {
		nsfw, show  bool
		wantExclude bool
	}{{false, true, true}, {true, false, true}, {true, true, false}} {
		var sent map[string]any
		c := newClient(t, tc.nsfw, func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &sent)
			w.Write(fixture(t, "search.json"))
		})
		page, err := c.Search(context.Background(), "berserk", 1, tc.show)
		if err != nil {
			t.Fatal(err)
		}
		if _, has := sent["exclude_genre"]; has != tc.wantExclude {
			t.Errorf("nsfw=%v show=%v: exclude_genre sent = %v", tc.nsfw, tc.show, has)
		}
		if sent["stype"] != "title" || sent["perpage"] != float64(30) || sent["search"] != "berserk" {
			t.Errorf("body = %v", sent)
		}
		want := map[string]any{
			"page": float64(1), "total_results": float64(61), "total_pages": float64(3),
			"results": []any{
				map[string]any{"media_id": float64(51239621230), "source": "mangaupdates", "media_type": "manga", "title": "Berserk", "image": "https://cdn.example/b.jpg"},
				map[string]any{"media_id": float64(2), "source": "mangaupdates", "media_type": "manga", "title": "No Image", "image": ImgNone},
			},
		}
		if got := norm(t, page); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v\nwant %v", got, want)
		}
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	c := newClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write(fixture(t, "empty_query_400.json"))
	})
	page, err := c.Search(context.Background(), "", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if page["total_results"] != float64(0) || page["total_pages"] != float64(1) || len(page["results"].([]any)) != 0 {
		t.Errorf("page = %v", page)
	}
}

func TestSearchOtherErrorsPropagate(t *testing.T) {
	c := newClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"context":{}}`))
	})
	if _, err := c.Search(context.Background(), "x", 1, true); err == nil {
		t.Fatal("want error")
	}
}

func TestManga(t *testing.T) {
	c := newClient(t, false, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/series/51239621230":
			w.Write(fixture(t, "series.json"))
		case "/series/10":
			w.Write(fixture(t, "ref10.json"))
		case "/series/20":
			w.Write(fixture(t, "ref20.json"))
		default:
			http.NotFound(w, r)
		}
	})
	m, err := c.Manga(context.Background(), "51239621230")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"media_id": "51239621230", "source": "mangaupdates", "source_url": "https://www.mangaupdates.com/series/xyz/berserk",
		"media_type": "manga", "title": "Berserk", "image": "https://cdn.example/b.jpg", "synopsis": "Guts.",
		"max_progress": float64(386), "genres": []any{"Action", "Drama"}, "score": 9.3, "score_count": float64(100),
		"details": map[string]any{
			"format": "Manga", "authors": []any{"MIURA Kentaro"}, "year": "1989",
			"status_in_country_of_origin": "43 Volumes (Ongoing)", "latest_chapter_translated": float64(386),
		},
		"related": map[string]any{
			"related_manga": []any{
				map[string]any{"source": "mangaupdates", "media_id": float64(10), "media_type": "manga", "title": "Berserk Prototype", "image": "https://cdn.example/10.jpg"},
			},
			"recommendations": []any{
				map[string]any{"source": "mangaupdates", "media_id": float64(20), "media_type": "manga", "title": "Vagabond", "image": ImgNone},
			},
		},
	}
	if got := norm(t, m); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestMangaNotFound(t *testing.T) {
	c := newClient(t, false, http.NotFound)
	if _, err := c.Manga(context.Background(), "1"); !providers.NotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusAndScore(t *testing.T) {
	s := "x"
	if status(&s) != "x" || status(nil) != nil || score(0) != nil || score(8.04) != 8.0 {
		t.Error("helpers wrong")
	}
}

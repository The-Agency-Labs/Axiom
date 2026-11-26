package mal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/providers"
)

func TestPreflight(t *testing.T) {
	im := &Importer{Key: "k"}
	if err := im.Preflight("good_name-1"); err != nil {
		t.Fatal(err)
	}
	if err := im.Preflight("a b/../x"); err == nil {
		t.Fatal("bad username accepted")
	}
	if err := (&Importer{}).Preflight("name"); err != ErrNoKey {
		t.Fatalf("got %v", err)
	}
}

func TestConvert(t *testing.T) {
	it := listItem{Node: node{ID: 5, Title: "Show", NumEpisodes: 12, MediaType: "movie", StartDate: "2020-04"},
		ListStatus: listStatus{Status: "completed", Score: 8, Watched: 3, StartDate: "2021", FinishDate: "2021-05-06", UpdatedAt: "2021-05-06T10:00:00+00:00"}}
	e := convert(it, true)
	if e.Status != "COMPLETED" || e.Progress != 3 || *e.Media.IDMal != 5 || e.Media.ID != 0 || e.Media.Format != "MOVIE" || *e.Media.Episodes != 12 {
		t.Fatalf("%+v", e)
	}
	if e.StartedAt.Year != 2021 || e.StartedAt.Month != 0 || e.CompletedAt.Day != 6 || e.Media.StartDate.Month != 4 || e.UpdatedAt == 0 {
		t.Fatalf("dates %+v", e)
	}
	if anilistStatus("reading", true) != "REPEATING" || anilistStatus("on_hold", false) != "PAUSED" || anilistStatus("plan_to_read", false) != "PLANNING" {
		t.Fatal("status mapping")
	}
}

func TestFetchPages(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mal-Client-Id") != "k" {
			t.Error("missing client id")
		}
		if strings.Contains(r.URL.Path, "nobody") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("offset") == "" {
			fmt.Fprintf(w, `{"data":[{"node":{"id":1,"title":"A"},"list_status":{"status":"watching"}}],"paging":{"next":"%s%s?offset=1"}}`, srv.URL, r.URL.Path)
			return
		}
		fmt.Fprint(w, `{"data":[{"node":{"id":2,"title":"B"},"list_status":{"status":"dropped"}}],"paging":{}}`)
	}))
	defer srv.Close()
	im := &Importer{Key: "k", HTTP: providers.NewClient(nil), BaseURL: srv.URL}
	got, err := im.fetch(context.Background(), "me", "anime")
	if err != nil || len(got) != 2 || got[1].Node.ID != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := im.fetch(context.Background(), "nobody", "anime"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

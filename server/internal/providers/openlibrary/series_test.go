package openlibrary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

func TestEditionSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/books/OL1M.json":
			w.Write([]byte(`{"title":"Golden Son","series":["Red Rising Saga #2"," ","The Red Rising Trilogy, Book 2"]}`))
		case "/books/OL2M.json":
			w.Write([]byte(`{"title":"Standalone"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{})
	c.Root = srv.URL
	got, err := c.EditionSeries(context.Background(), "OL1M")
	if err != nil || !reflect.DeepEqual(got, []string{"Red Rising Saga #2", "The Red Rising Trilogy, Book 2"}) {
		t.Fatalf("series = %v, %v", got, err)
	}
	if got, err := c.EditionSeries(context.Background(), "OL2M"); err != nil || len(got) != 0 {
		t.Fatalf("standalone = %v, %v", got, err)
	}
	if _, err := c.EditionSeries(context.Background(), "OL9M"); !providers.NotFound(err) {
		t.Fatalf("missing edition: %v", err)
	}
}

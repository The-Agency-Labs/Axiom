// Package readingtest builds a reading service on a temp database with fake
// Hardcover and Open Library providers, for tests of the reading domain and
// its HTTP layer.
package readingtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/providers"
	"github.com/the-agency-labs/axiom/server/internal/reading"
)

// Book is a title a fake provider knows.
type Book struct {
	ID       string
	Title    string
	Author   string
	Synopsis string
	Pages    int
	TOC      []string
}

// Provider is a fake book provider (satisfies catalog.Hardcover and
// catalog.OpenLibrary). Search returns every book sharing a word with the
// query, in the order added.
type Provider struct {
	Source string
	Books  []Book
	Err    error // returned by every call when set
	Calls  int   // read only when no request is in flight
	mu     sync.Mutex
}

// Add registers a book.
func (p *Provider) Add(b Book) *Provider {
	p.Books = append(p.Books, b)
	return p
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,:;!?'\"()-")
		if len(w) > 2 {
			out[w] = true
		}
	}
	return out
}

// Search implements the provider search.
func (p *Provider) Search(_ context.Context, query string, page int) (providers.SearchPage, error) {
	p.mu.Lock()
	p.Calls++
	p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	q := words(query)
	results := []any{}
	for _, b := range p.Books {
		for w := range words(b.Title) {
			if q[w] {
				results = append(results, map[string]any{
					"media_id": b.ID, "source": p.Source, "media_type": "book", "title": b.Title, "image": "https://img.example/" + b.ID,
				})
				break
			}
		}
	}
	return providers.SearchPage{"page": page, "total_results": len(results), "total_pages": 1, "results": results}, nil
}

// Book implements the provider metadata lookup.
func (p *Provider) Book(_ context.Context, id string) (providers.Metadata, error) {
	p.mu.Lock()
	p.Calls++
	p.mu.Unlock()
	if p.Err != nil {
		return nil, p.Err
	}
	for _, b := range p.Books {
		if b.ID != id {
			continue
		}
		meta := providers.Metadata{
			"media_id": b.ID, "source": p.Source, "media_type": "book", "title": b.Title,
			"max_progress": b.Pages, "image": "https://img.example/" + b.ID,
			"synopsis": b.Synopsis, "details": map[string]any{"author": b.Author, "number_of_pages": b.Pages},
		}
		if len(b.TOC) > 0 {
			var toc []any
			for _, t := range b.TOC {
				toc = append(toc, map[string]any{"title": t, "level": 0})
			}
			meta["table_of_contents"] = toc
		}
		return meta, nil
	}
	return nil, errors.New("fake provider: not found: " + id)
}

// Env is a reading service with fakes.
type Env struct {
	*catalogtest.Env
	HC      *Provider
	OL      *Provider
	Library *library.Service
	Reading *reading.Service
	User    int64
}

// NewEnv builds an Env (clock at start) with one user, "owner".
func NewEnv(t testing.TB, start time.Time) *Env {
	t.Helper()
	// Most fixtures model a session as a single page turn; tests of the
	// one-page rule itself set reading.MinSessionPages back to 2.
	old := reading.MinSessionPages
	reading.MinSessionPages = 1
	t.Cleanup(func() { reading.MinSessionPages = old })
	ce := catalogtest.NewEnv(t, start)
	e := &Env{Env: ce, HC: &Provider{Source: "hardcover"}, OL: &Provider{Source: "openlibrary"}}
	ce.Catalog.Reg.P.Hardcover = e.HC
	ce.Catalog.Reg.P.OpenLibrary = e.OL
	e.Library = library.New(ce.Conn, ce.Q, ce.Catalog)
	e.Library.Now = ce.Now
	e.Reading = reading.New(ce.Conn, ce.Q, ce.Catalog, e.Library)
	e.User = ce.User(t, "owner")
	return e
}

var _ catalog.Hardcover = (*Provider)(nil)
var _ catalog.OpenLibrary = (*Provider)(nil)

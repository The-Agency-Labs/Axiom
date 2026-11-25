package catalogtest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Hardcover is a scriptable fake of the Hardcover client with series support
// (catalog.Hardcover and catalog.HardcoverSeries).
type Hardcover struct {
	mu     sync.Mutex
	books  map[string]Metadata
	series []providers.SeriesInfo
	// Calls counts client calls by name ("Book", "BookSeries", "Series", ...).
	Calls map[string]int
	// SeriesErr, when set, is returned by BookSeries, Series and SearchSeries.
	SeriesErr error
}

// NewHardcover returns an empty fake.
func NewHardcover() *Hardcover {
	return &Hardcover{books: map[string]Metadata{}, Calls: map[string]int{}}
}

// AddBook registers a book that Book can return.
func (f *Hardcover) AddBook(id, title string) *Hardcover {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.books[id] = Metadata{
		"media_id": id, "source": "hardcover", "media_type": "book", "title": title, "max_progress": 300,
		"image": "https://img/hc-" + id + ".jpg", "synopsis": "A book.",
		"details": map[string]any{"publish_date": "2016-01-01"},
	}
	return f
}

// AddSeries registers a series with its books (listed or not as books). The
// series are returned from BookSeries in registration order.
func (f *Hardcover) AddSeries(id, name string, books ...providers.SeriesBook) *Hardcover {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.series = append(f.series, providers.SeriesInfo{ExternalID: id, Name: name, Slug: strings.ToLower(id), BooksCount: len(books), Books: books})
	return f
}

// SetSeries replaces the books of a registered series (the provider changed it).
func (f *Hardcover) SetSeries(id, name string, books ...providers.SeriesBook) *Hardcover {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.series {
		if s.ExternalID == id {
			f.series[i] = providers.SeriesInfo{ExternalID: id, Name: name, Slug: strings.ToLower(id), BooksCount: len(books), Books: books}
		}
	}
	return f
}

// SeriesBook builds a series book entry.
func SeriesBook(position float64, id, title string) providers.SeriesBook {
	return providers.SeriesBook{Position: position, ExternalID: id, Title: title, Image: "https://img/hc-" + id + ".jpg", Year: 2016}
}

func (f *Hardcover) call(name string) {
	f.Calls[name]++
}

// Book returns a registered book.
func (f *Hardcover) Book(_ context.Context, id string) (Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("Book")
	m, ok := f.books[id]
	if !ok {
		return nil, providers.NotFoundError("Hardcover", "book", id)
	}
	return clone(m), nil
}

// Search finds no books.
func (f *Hardcover) Search(context.Context, string, int) (providers.SearchPage, error) {
	return providers.SearchPage{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}}, nil
}

// BookSeries returns every registered series that lists the book.
func (f *Hardcover) BookSeries(_ context.Context, bookID string) ([]providers.BookSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("BookSeries")
	if f.SeriesErr != nil {
		return nil, f.SeriesErr
	}
	out := []providers.BookSeries{}
	for _, s := range f.series {
		for _, b := range s.Books {
			if b.ExternalID == bookID {
				out = append(out, providers.BookSeries{Position: b.Position, Series: s})
			}
		}
	}
	return out, nil
}

// Series returns a registered series.
func (f *Hardcover) Series(_ context.Context, id string) (providers.SeriesInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("Series")
	if f.SeriesErr != nil {
		return providers.SeriesInfo{}, f.SeriesErr
	}
	for _, s := range f.series {
		if s.ExternalID == id {
			return s, nil
		}
	}
	return providers.SeriesInfo{}, providers.NotFoundError("Hardcover", "series", id)
}

// SearchSeries finds registered series whose name contains the query.
func (f *Hardcover) SearchSeries(_ context.Context, q string) ([]providers.SeriesHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("SearchSeries")
	if f.SeriesErr != nil {
		return nil, f.SeriesErr
	}
	out := []providers.SeriesHit{}
	for _, s := range f.series {
		if strings.Contains(strings.ToLower(s.Name), strings.ToLower(q)) {
			out = append(out, providers.SeriesHit{ExternalID: s.ExternalID, Name: s.Name, BooksCount: s.BooksCount})
		}
	}
	return out, nil
}

// OpenLibrary is a scriptable fake of the Open Library client with edition
// series strings (catalog.OpenLibrary and catalog.OpenLibrarySeries).
type OpenLibrary struct {
	mu      sync.Mutex
	books   map[string]Metadata
	strings map[string][]string
}

// NewOpenLibrary returns an empty fake.
func NewOpenLibrary() *OpenLibrary {
	return &OpenLibrary{books: map[string]Metadata{}, strings: map[string][]string{}}
}

// AddBook registers an edition with its free-text series strings.
func (f *OpenLibrary) AddBook(id, title string, series ...string) *OpenLibrary {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.books[id] = Metadata{
		"media_id": id, "source": "openlibrary", "media_type": "book", "title": title, "max_progress": 250,
		"image": "https://img/ol-" + id + ".jpg", "synopsis": "A book.",
		"details": map[string]any{"publish_date": "2015"},
	}
	f.strings[id] = series
	return f
}

// Book returns a registered edition.
func (f *OpenLibrary) Book(_ context.Context, id string) (Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.books[id]
	if !ok {
		return nil, providers.NotFoundError("Open Library", "book", id)
	}
	return clone(m), nil
}

// Search finds no books.
func (f *OpenLibrary) Search(context.Context, string, int) (providers.SearchPage, error) {
	return providers.SearchPage{"page": 1, "total_pages": 1, "total_results": 0, "results": []any{}}, nil
}

// EditionSeries returns the series strings of an edition.
func (f *OpenLibrary) EditionSeries(_ context.Context, id string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.books[id]; !ok {
		return nil, fmt.Errorf("no edition %s", id)
	}
	return f.strings[id], nil
}

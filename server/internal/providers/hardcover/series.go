package hardcover

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// seriesBooksFragment lists the books of a series in reading order. Books
// without a position (box sets, omnibuses) are left out. Hardcover lists
// every translation and split edition as its own book; info() keeps one
// book per position (see there).
const seriesBooksFragment = `
            id
            name
            slug
            books_count
            book_series(where: {position: {_is_null: false}}, order_by: {position: asc}) {
              position
              featured
              compilation
              book {
                id
                title
                cached_image(path: "url")
                release_year
                canonical_id
                users_count
              }
            }`

const bookSeriesQuery = `
        query GetBookSeries($book_id: Int!) {
          books_by_pk(id: $book_id) {
            id
            book_series(order_by: [{featured: desc}, {position: asc}]) {
              position
              featured
              series {` + seriesBooksFragment + `
              }
            }
          }
        }
        `

const seriesQuery = `
        query GetSeries($series_id: Int!) {
          series_by_pk(id: $series_id) {` + seriesBooksFragment + `
          }
        }
        `

const searchSeriesQuery = `
        query SearchSeries($query: String!, $per_page: Int!, $page: Int!) {
          search(
            query: $query,
            query_type: "Series",
            per_page: $per_page,
            page: $page,
          ) {
            results
          }
        }
        `

type seriesData struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	BooksCount int    `json:"books_count"`
	BookSeries []struct {
		Position    *float64 `json:"position"`
		Featured    *bool    `json:"featured"`
		Compilation bool     `json:"compilation"`
		Book        *struct {
			ID          int    `json:"id"`
			Title       string `json:"title"`
			CachedImage string `json:"cached_image"`
			ReleaseYear *int   `json:"release_year"`
			CanonicalID *int   `json:"canonical_id"`
			UsersCount  int    `json:"users_count"`
			Compilation bool   `json:"compilation"`
		} `json:"book"`
	} `json:"book_series"`
}

func (d *seriesData) info() providers.SeriesInfo {
	out := providers.SeriesInfo{
		ExternalID: strconv.Itoa(d.ID), Name: d.Name, Slug: d.Slug, BooksCount: d.BooksCount,
	}
	// Hardcover lists translations (canonical_id set), split editions ("Iron
	// Gold - Partie 1", not featured) and compilations as books of the
	// series. Keep the canonical, featured, single books, and of those the
	// most-read one per position: "Red Rising Saga" is 7 books, not 31.
	best := map[float64]int{} // position -> index in out.Books
	users := map[float64]int{}
	for _, bs := range d.BookSeries {
		if bs.Book == nil || bs.Position == nil {
			continue
		}
		if (bs.Featured != nil && !*bs.Featured) || bs.Compilation || bs.Book.Compilation || bs.Book.CanonicalID != nil {
			continue
		}
		b := providers.SeriesBook{
			Position: *bs.Position, ExternalID: strconv.Itoa(bs.Book.ID), Title: bs.Book.Title, Image: bs.Book.CachedImage,
		}
		if bs.Book.ReleaseYear != nil {
			b.Year = *bs.Book.ReleaseYear
		}
		if i, ok := best[b.Position]; ok {
			if bs.Book.UsersCount > users[b.Position] {
				out.Books[i], users[b.Position] = b, bs.Book.UsersCount
			}
			continue
		}
		best[b.Position], users[b.Position] = len(out.Books), bs.Book.UsersCount
		out.Books = append(out.Books, b)
	}
	return out
}

// BookSeries returns the series a book belongs to (first = primary), each with
// all of its books. A book in no series gives an empty list.
func (c *Client) BookSeries(ctx context.Context, bookID string) ([]providers.BookSeries, error) {
	id, err := strconv.Atoi(strings.TrimSpace(bookID))
	if err != nil {
		return nil, &providers.Error{Provider: label, Status: 404, Details: "No result found with ID " + bookID}
	}
	var out []providers.BookSeries
	err = c.base.Cached(ctx, "hardcover_book_series_"+bookID, cacheTTL, &out, func() (any, error) {
		raw, err := c.graphql(ctx, bookSeriesQuery, map[string]any{"book_id": id})
		if err != nil {
			return nil, err
		}
		var data struct {
			Book *struct {
				BookSeries []struct {
					Position *float64    `json:"position"`
					Series   *seriesData `json:"series"`
				} `json:"book_series"`
			} `json:"books_by_pk"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, &providers.Error{Provider: label, Status: 200, Details: "unexpected response", Err: err}
		}
		if data.Book == nil {
			return nil, &providers.Error{Provider: label, Status: 404, Details: "No result found with ID " + bookID}
		}
		res := []providers.BookSeries{}
		for _, bs := range data.Book.BookSeries {
			if bs.Series == nil {
				continue
			}
			var pos float64
			if bs.Position != nil {
				pos = *bs.Position
			}
			res = append(res, providers.BookSeries{Position: pos, Series: bs.Series.info()})
		}
		return res, nil
	})
	return out, err
}

// Series returns one series with all of its books.
func (c *Client) Series(ctx context.Context, seriesID string) (providers.SeriesInfo, error) {
	id, err := strconv.Atoi(strings.TrimSpace(seriesID))
	if err != nil {
		return providers.SeriesInfo{}, &providers.Error{Provider: label, Status: 404, Details: "No result found with ID " + seriesID}
	}
	var out providers.SeriesInfo
	err = c.base.Cached(ctx, "hardcover_series_"+seriesID, cacheTTL, &out, func() (any, error) {
		raw, err := c.graphql(ctx, seriesQuery, map[string]any{"series_id": id})
		if err != nil {
			return nil, err
		}
		var data struct {
			Series *seriesData `json:"series_by_pk"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, &providers.Error{Provider: label, Status: 200, Details: "unexpected response", Err: err}
		}
		if data.Series == nil {
			return nil, &providers.Error{Provider: label, Status: 404, Details: "No result found with ID " + seriesID}
		}
		return data.Series.info(), nil
	})
	return out, err
}

// idString reads a search document id, which Typesense sends as a string
// (or a number).
func idString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// SearchSeries searches series by name.
func (c *Client) SearchSeries(ctx context.Context, query string) ([]providers.SeriesHit, error) {
	query = CapSearchQuery(query)
	var out []providers.SeriesHit
	err := c.base.Cached(ctx, "search_hardcover_series_"+query, cacheTTL, &out, func() (any, error) {
		raw, err := c.graphql(ctx, searchSeriesQuery, map[string]any{"query": query, "per_page": PerPage, "page": 1})
		if err != nil {
			return nil, err
		}
		var data struct {
			Search struct {
				Results struct {
					Hits []struct {
						Document struct {
							ID         any    `json:"id"`
							Name       string `json:"name"`
							BooksCount int    `json:"books_count"`
						} `json:"document"`
					} `json:"hits"`
				} `json:"results"`
			} `json:"search"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, &providers.Error{Provider: label, Status: 200, Details: "unexpected response", Err: err}
		}
		res := []providers.SeriesHit{}
		for _, h := range data.Search.Results.Hits {
			id := idString(h.Document.ID)
			if id == "" || h.Document.Name == "" {
				continue
			}
			res = append(res, providers.SeriesHit{ExternalID: id, Name: h.Document.Name, BooksCount: h.Document.BooksCount})
		}
		return res, nil
	})
	return out, err
}

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
// every translation and split edition as its own book; info() cleans that up
// (see series_clean.go), which is why each entry carries the signals it uses:
// users_count, release date, is_partial_book, book_series.details and the
// languages of the book's default editions.
const seriesBooksFragment = `
            id
            name
            slug
            books_count
            book_series(where: {position: {_is_null: false}}, order_by: {position: asc}) {
              position
              featured
              compilation
              details
              book {
                id
                title
                cached_image(path: "url")
                release_year
                release_date
                canonical_id
                users_count
                is_partial_book
                default_physical_edition { language { language } }
                default_ebook_edition { language { language } }
                default_cover_edition { language { language } }
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
	ID         int           `json:"id"`
	Name       string        `json:"name"`
	Slug       string        `json:"slug"`
	BooksCount int           `json:"books_count"`
	BookSeries []seriesEntry `json:"book_series"`
}

func (d *seriesData) info() providers.SeriesInfo {
	return providers.SeriesInfo{
		ExternalID: strconv.Itoa(d.ID), Name: d.Name, Slug: d.Slug, BooksCount: d.BooksCount,
		Books: cleanSeriesBooks(d.BookSeries, nowFunc()),
	}
}

// BookSeries returns the series a book belongs to (first = primary; see
// rankBookSeries), each with all of its books. A book in no series gives an empty list.
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
					Featured *bool       `json:"featured"`
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
		var ranked []rankedSeries
		for _, bs := range data.Book.BookSeries {
			if bs.Series == nil {
				continue
			}
			var pos float64
			if bs.Position != nil {
				pos = *bs.Position
			}
			ranked = append(ranked, rankedSeries{
				BookSeries: providers.BookSeries{Position: pos, Series: bs.Series.info()},
				featured:   bs.Featured == nil || *bs.Featured,
			})
		}
		return rankBookSeries(ranked), nil
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

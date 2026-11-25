// Package hardcover is the Hardcover (GraphQL) book provider, ported from
// src/app/providers/hardcover.py.
//
// Hardcover's schema has no table of contents (neither books nor editions
// expose one), so metadata carries no "table_of_contents" key.
package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	// PerPage is settings.PER_PAGE.
	PerPage = 24

	// MaxSearchQueryLength caps queries sent to Hardcover search.
	MaxSearchQueryLength = 50

	label    = "Hardcover"
	source   = "hardcover"
	cacheTTL = 24 * time.Hour
)

// DefaultURL is the Hardcover GraphQL endpoint.
const DefaultURL = "https://api.hardcover.app/v1/graphql"

// Client talks to Hardcover.
type Client struct {
	// URL is the GraphQL endpoint (overridable for tests).
	URL string

	base *providers.Client
	key  string
}

// New returns a Hardcover client using cfg.HardcoverKey.
func New(base *providers.Client, cfg config.Config) *Client {
	return &Client{URL: DefaultURL, base: base, key: cfg.HardcoverKey}
}

// CapSearchQuery limits long queries (cap_search_query).
func CapSearchQuery(query string) string {
	r := []rune(query)
	if len(r) <= MaxSearchQueryLength {
		return query
	}
	capped := r[:MaxSearchQueryLength]
	if unicode.IsSpace(r[MaxSearchQueryLength]) {
		return strings.TrimRightFunc(string(capped), unicode.IsSpace)
	}
	boundary := -1
	for i := len(capped) - 1; i >= 0; i-- {
		if capped[i] == ' ' {
			boundary = i
			break
		}
	}
	if boundary == -1 {
		return string(capped)
	}
	return strings.TrimRightFunc(string(capped[:boundary]), unicode.IsSpace)
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// graphql runs one query (graphql_request) and returns the "data" object.
func (c *Client) graphql(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	body, err := c.base.Do(ctx, providers.Request{
		Provider: label,
		Method:   "POST",
		URL:      c.URL,
		Header:   map[string][]string{"Authorization": {"Bearer " + c.key}},
		Body:     map[string]any{"query": query, "variables": variables},
	})
	if err != nil {
		return nil, handleError(err)
	}
	var resp gqlResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &providers.Error{Provider: label, Status: 200, Details: "invalid JSON response", Err: err}
	}
	// GraphQL errors come back with a 200 status.
	if len(resp.Errors) > 0 {
		return nil, &providers.Error{Provider: label, Status: 200, Details: resp.Errors[0].Message}
	}
	return resp.Data, nil
}

// handleError adds the 401 details Python's handle_error extracted.
func handleError(err error) error {
	pe, ok := err.(*providers.Error)
	if !ok || pe.Status != 401 || pe.Err == nil {
		return err
	}
	var body struct {
		ErrorDescription string `json:"error_description"`
		Error            string `json:"error"`
	}
	if json.Unmarshal([]byte(pe.Err.Error()), &body) != nil {
		return err
	}
	details := body.ErrorDescription
	if details == "" {
		details = body.Error
	}
	return &providers.Error{Provider: label, Status: 401, Details: details, Err: pe.Err}
}

const searchQuery = `
        query SearchBooks($query: String!, $per_page: Int!, $page: Int!) {
          search(
            query: $query,
            query_type: "Book",
            per_page: $per_page,
            page: $page,
          ) {
            results
          }
        }
        `

// Search searches for books (hardcover.search).
func (c *Client) Search(ctx context.Context, query string, page int) (providers.SearchPage, error) {
	query = CapSearchQuery(query)
	key := fmt.Sprintf("search_hardcover_book_%s_%d", query, page)
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		raw, err := c.graphql(ctx, searchQuery, map[string]any{"query": query, "per_page": PerPage, "page": page})
		if err != nil {
			return nil, err
		}
		var data struct {
			Search struct {
				Results struct {
					Hits []struct {
						Document struct {
							ID    any    `json:"id"`
							Title string `json:"title"`
							Image *struct {
								URL string `json:"url"`
							} `json:"image"`
						} `json:"document"`
					} `json:"hits"`
					Found int `json:"found"`
				} `json:"results"`
			} `json:"search"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, &providers.Error{Provider: label, Status: 200, Details: "unexpected response", Err: err}
		}
		results := make([]any, 0, len(data.Search.Results.Hits))
		for _, hit := range data.Search.Results.Hits {
			image := ImgNone
			if hit.Document.Image != nil && hit.Document.Image.URL != "" {
				image = hit.Document.Image.URL
			}
			results = append(results, map[string]any{
				"media_id":   hit.Document.ID,
				"source":     source,
				"media_type": "book",
				"title":      hit.Document.Title,
				"image":      image,
			})
		}
		return FormatSearchResponse(page, PerPage, data.Search.Results.Found, results), nil
	})
	return out, err
}

// FormatSearchResponse mirrors helpers.format_search_response.
func FormatSearchResponse(page, perPage, total int, results []any) providers.SearchPage {
	return providers.SearchPage{
		"page":          page,
		"total_results": total,
		"total_pages":   total/perPage + 1,
		"results":       results,
	}
}

const bookQuery = `
        query GetBookDetails($book_id: Int!) {
          books_by_pk(id: $book_id) {
            id
            title
            cached_image(path: "url")
            description
            cached_tags(path: "Genre")
            rating
            ratings_count
            pages
            release_date
            slug
            cached_contributors(path: "[0]['author']['name']")
            default_cover_edition {
              edition_format
              isbn_13
              isbn_10
              release_date
              publisher {
                name
              }
            }
          }
        }
        `

type edition struct {
	EditionFormat string  `json:"edition_format"`
	ISBN13        string  `json:"isbn_13"`
	ISBN10        string  `json:"isbn_10"`
	ReleaseDate   *string `json:"release_date"`
	Publisher     *struct {
		Name *string `json:"name"`
	} `json:"publisher"`
}

type bookData struct {
	ID            int      `json:"id"`
	Title         string   `json:"title"`
	CachedImage   string   `json:"cached_image"`
	Description   string   `json:"description"`
	CachedTags    []tag    `json:"cached_tags"`
	Rating        float64  `json:"rating"`
	RatingsCount  *int     `json:"ratings_count"`
	Pages         *int     `json:"pages"`
	ReleaseDate   *string  `json:"release_date"`
	Slug          string   `json:"slug"`
	Contributors  *string  `json:"cached_contributors"`
	DefaultEdtion *edition `json:"default_cover_edition"`
}

type tag struct {
	Tag string `json:"tag"`
}

// Book returns metadata for a book (hardcover.book).
func (c *Client) Book(ctx context.Context, mediaID string) (providers.Metadata, error) {
	key := "hardcover_book_" + mediaID
	var out providers.Metadata
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		id, err := strconv.Atoi(strings.TrimSpace(mediaID))
		if err != nil {
			return nil, &providers.Error{Provider: label, Status: 404, Details: "No result found with ID " + mediaID}
		}
		raw, err := c.graphql(ctx, bookQuery, map[string]any{"book_id": id})
		if err != nil {
			return nil, err
		}
		var data struct {
			Book *bookData `json:"books_by_pk"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, &providers.Error{Provider: label, Status: 200, Details: "unexpected response", Err: err}
		}
		b := data.Book
		if b == nil {
			return nil, &providers.Error{Provider: label, Status: 404, Details: "No result found with ID " + mediaID}
		}
		return buildBook(b), nil
	})
	return out, err
}

func buildBook(b *bookData) providers.Metadata {
	var (
		format, publisher, isbn, editionDate any
	)
	if e := b.DefaultEdtion; e != nil {
		f := e.EditionFormat
		if f == "" {
			f = "Unknown"
		}
		format = f
		if e.Publisher != nil && e.Publisher.Name != nil {
			publisher = *e.Publisher.Name
		}
		var isbns []any
		if e.ISBN10 != "" {
			isbns = append(isbns, e.ISBN10)
		}
		if e.ISBN13 != "" {
			isbns = append(isbns, e.ISBN13)
		}
		if isbns != nil {
			isbn = isbns
		}
		if e.ReleaseDate != nil {
			editionDate = *e.ReleaseDate
		}
	}

	publishDate := editionDate
	if s, _ := publishDate.(string); s == "" {
		publishDate = nilIfNil(b.ReleaseDate)
	}

	synopsis := b.Description
	if synopsis == "" {
		synopsis = "No synopsis available."
	}
	image := b.CachedImage
	if image == "" {
		image = ImgNone
	}
	var scoreCount any
	if b.RatingsCount != nil {
		scoreCount = *b.RatingsCount
	}
	var pages any
	if b.Pages != nil {
		pages = *b.Pages
	}
	var author any
	if b.Contributors != nil {
		author = *b.Contributors
	}

	return providers.Metadata{
		"media_id":     b.ID,
		"source":       source,
		"source_url":   "https://hardcover.app/books/" + b.Slug,
		"media_type":   "book",
		"title":        b.Title,
		"max_progress": pages,
		"image":        image,
		"synopsis":     synopsis,
		"genres":       Tags(b.CachedTags),
		"score":        Rating(b.Rating),
		"score_count":  scoreCount,
		"details": map[string]any{
			"format":          format,
			"number_of_pages": pages,
			"publish_date":    publishDate,
			"author":          author,
			"publisher":       publisher,
			"isbn":            isbn,
		},
	}
}

func nilIfNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// Tags returns the genre names, or nil when there are none (get_tags).
func Tags(tags []tag) any {
	if len(tags) == 0 {
		return nil
	}
	out := make([]any, len(tags))
	for i, t := range tags {
		out[i] = t.Tag
	}
	return out
}

// Rating converts Hardcover's 5-point rating to the 10-point scale with one
// decimal, nil when unrated (get_ratings).
func Rating(r float64) any {
	if r == 0 {
		return nil
	}
	v, _ := strconv.ParseFloat(strconv.FormatFloat(r*2, 'f', 1, 64), 64)
	return v
}

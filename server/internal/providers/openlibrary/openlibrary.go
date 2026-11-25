// Package openlibrary is the Open Library book provider, ported from
// src/app/providers/openlibrary.py. Edition metadata additionally carries
// "table_of_contents" ([]any of {"title": string, "level": int}) when the
// edition has one.
package openlibrary

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

	label     = "Open Library"
	source    = "openlibrary"
	cacheTTL  = 24 * time.Hour
	userAgent = "Encompass/1.0 (+https://github.com/The-Agency-Labs/Encompass)"
)

// Client talks to Open Library.
type Client struct {
	// Root is the site root (overridable for tests).
	Root string

	base *providers.Client
}

// New returns an Open Library client.
func New(base *providers.Client, _ config.Config) *Client {
	return &Client{Root: "https://openlibrary.org", base: base}
}

func (c *Client) get(ctx context.Context, rawURL string, dst any) error {
	return c.base.JSON(ctx, providers.Request{
		Provider: label,
		URL:      rawURL,
		Header:   http.Header{"User-Agent": {userAgent}},
	}, dst)
}

// ExtractID returns the last path segment ("/works/OL123W" -> "OL123W"),
// "" for an empty path (extract_openlibrary_id).
func ExtractID(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimRight(path, "/")
	return path[strings.LastIndex(path, "/")+1:]
}

func coverURL(id any) string {
	switch v := id.(type) {
	case float64:
		return "https://covers.openlibrary.org/b/id/" + strconv.FormatFloat(v, 'f', -1, 64) + "-L.jpg"
	case string:
		return "https://covers.openlibrary.org/b/id/" + v + "-L.jpg"
	}
	return ""
}

// Search searches for books (openlibrary.search).
func (c *Client) Search(ctx context.Context, query string, page int) (providers.SearchPage, error) {
	key := fmt.Sprintf("search_openlibrary_book_%s_%d", query, page)
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		params := url.Values{
			"q":      {query},
			"fields": {"title,key,editions,editions.key,editions.cover_i,editions.title"},
			"limit":  {strconv.Itoa(PerPage)},
			"page":   {strconv.Itoa(page)},
		}
		var resp struct {
			NumFound int `json:"numFound"`
			Docs     []struct {
				Title    string `json:"title"`
				Editions struct {
					Docs []map[string]any `json:"docs"`
				} `json:"editions"`
			} `json:"docs"`
		}
		if err := c.get(ctx, c.Root+"/search.json?"+params.Encode(), &resp); err != nil {
			return nil, err
		}
		results := []any{}
		for _, doc := range resp.Docs {
			if len(doc.Editions.Docs) == 0 {
				continue
			}
			top := doc.Editions.Docs[0]
			editionTitle, _ := top["title"].(string)
			title := doc.Title
			if editionTitle != title {
				title = editionTitle + ": " + title
			}
			results = append(results, map[string]any{
				"media_id":   ExtractID(providers.Str(top, "key")),
				"source":     source,
				"media_type": "book",
				"title":      title,
				"image":      searchImage(top),
			})
		}
		return providers.SearchPage{
			"page":          page,
			"total_results": resp.NumFound,
			"total_pages":   resp.NumFound/PerPage + 1,
			"results":       results,
		}, nil
	})
	return out, err
}

// searchImage ports get_image_url: a missing cover_i gives the placeholder,
// a present-but-empty one gives nil (as the Python returns None).
func searchImage(doc map[string]any) any {
	id, ok := doc["cover_i"]
	if !ok {
		return ImgNone
	}
	if f, isNum := id.(float64); isNum && f != 0 {
		return coverURL(f)
	}
	return nil
}

// Book returns metadata for an edition id like "OL123M" (openlibrary.book).
func (c *Client) Book(ctx context.Context, mediaID string) (providers.Metadata, error) {
	key := "openlibrary_book_" + mediaID
	if c.base.Cache != nil {
		var cached providers.Metadata
		if ok, _ := c.base.Cache.Get(ctx, key, &cached); ok && cached != nil {
			return cached, nil
		}
	}

	var book map[string]any
	if err := c.get(ctx, c.Root+"/books/"+mediaID+".json", &book); err != nil {
		return nil, err
	}

	work := map[string]any{}
	if works := providers.List(book, "works"); len(works) > 0 {
		workID := ExtractID(providers.Str(works[0], "key"))
		if err := c.get(ctx, c.Root+"/works/"+workID+".json", &work); err != nil {
			return nil, err
		}
	}

	var (
		wg              sync.WaitGroup
		authors         any
		authorsComplete bool
		editions        []any
		score, count    any
	)
	wg.Add(3)
	go func() { defer wg.Done(); authors, authorsComplete = c.authors(ctx, work) }()
	go func() { defer wg.Done(); editions = c.editions(ctx, book, work) }()
	go func() { defer wg.Done(); score, count = c.ratings(ctx, work) }()
	wg.Wait()

	var pages any
	if v, ok := book["number_of_pages"]; ok {
		pages = v
	}
	data := providers.Metadata{
		"media_id":     mediaID,
		"source":       source,
		"source_url":   "https://openlibrary.org/books/" + mediaID,
		"media_type":   "book",
		"title":        book["title"],
		"max_progress": pages,
		"image":        coverImage(book),
		"synopsis":     Description(book, work),
		"genres":       firstN(work, "subjects", 5),
		"score":        score,
		"score_count":  count,
		"details": map[string]any{
			"physical_format": physicalFormat(book),
			"number_of_pages": pages,
			"publish_date":    publishDate(book),
			"author":          authors,
			"publishers":      firstN(book, "publishers", 5),
			"isbn":            isbns(book),
		},
		"related": map[string]any{
			"other_editions": editions,
		},
	}
	if toc := TableOfContents(book); len(toc) > 0 {
		data["table_of_contents"] = toc
	}

	// don't cache missing authors from transient errors for the full timeout
	if authorsComplete && c.base.Cache != nil {
		_ = c.base.Cache.Set(ctx, key, data, cacheTTL)
	}
	return data, nil
}

func coverImage(resp map[string]any) string {
	if covers, ok := resp["covers"].([]any); ok && len(covers) > 0 {
		return coverURL(covers[0])
	}
	return ImgNone
}

// TableOfContents extracts an edition's table_of_contents as a list of
// {"title": string, "level": int}; nil when there is none.
func TableOfContents(book map[string]any) []any {
	raw, ok := book["table_of_contents"].([]any)
	if !ok {
		return nil
	}
	var out []any
	for _, e := range raw {
		title, level := "", 0
		switch v := e.(type) {
		case string:
			title = v
		case map[string]any:
			title = strings.TrimSpace(providers.Str(v, "title"))
			if title == "" {
				title = strings.TrimSpace(providers.Str(v, "label"))
			}
			level = providers.Int(v, "level")
		}
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		out = append(out, map[string]any{"title": title, "level": level})
	}
	return out
}

var tagRe = regexp.MustCompile(`<[a-zA-Z/!][^>]*>`)

// Description ports get_description: edition description first, then the
// work's; dict values unwrapped; HTML stripped and whitespace collapsed.
func Description(book, work map[string]any) string {
	const none = "No synopsis available."
	var raw any
	if v, ok := book["description"]; ok {
		raw = v
	} else if v, ok := work["description"]; ok {
		raw = v
	} else {
		return none
	}
	if m, ok := raw.(map[string]any); ok {
		raw = m["value"]
	}
	text, _ := raw.(string)
	text = html.UnescapeString(tagRe.ReplaceAllString(text, " "))
	return strings.Join(strings.Fields(text), " ")
}

// pyTitle is Python's str.title().
func pyTitle(s string) string {
	var b strings.Builder
	prevCased := false
	for _, r := range s {
		if prevCased {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(unicode.ToUpper(r))
		}
		prevCased = unicode.IsLetter(r)
	}
	return b.String()
}

func physicalFormat(book map[string]any) any {
	if f := providers.Str(book, "physical_format"); f != "" {
		return pyTitle(f)
	}
	return nil
}

func publishDate(book map[string]any) any {
	s, ok := book["publish_date"].(string)
	if !ok {
		return nil
	}
	s = strings.TrimPrefix(s, "cop. ")
	for _, layout := range []string{"January 2, 2006", "Jan 2, 2006", "2 January 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// firstN returns the first n entries of m[key], nil when the key is missing.
func firstN(m map[string]any, key string, n int) any {
	list, ok := m[key].([]any)
	if !ok {
		return nil
	}
	if len(list) > n {
		list = list[:n]
	}
	return list
}

func isbns(book map[string]any) any {
	var out []any
	for _, k := range []string{"isbn_13", "isbn_10"} {
		if l, ok := book[k].([]any); ok {
			out = append(out, l...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// authors fetches author names. complete is false when a transient failure
// dropped an author (so the result must not be cached for long).
func (c *Client) authors(ctx context.Context, work map[string]any) (any, bool) {
	var keys []string
	if entries, ok := work["authors"].([]any); ok {
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok {
				if a, ok := m["author"].(map[string]any); ok {
					keys = append(keys, providers.Str(a, "key"))
				}
			}
		}
	}
	type result struct {
		data   map[string]any
		failed bool
	}
	results := make([]result, len(keys))
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var data map[string]any
			err := c.get(ctx, c.Root+k+".json", &data)
			switch {
			case err == nil:
				results[i] = result{data: data}
			case isTransient(err):
				results[i] = result{failed: true}
			}
			// permanent errors (404, ...) skip the author but stay cacheable
		}()
	}
	wg.Wait()
	var names []any
	complete := true
	for _, r := range results {
		if r.failed {
			complete = false
		}
		if len(r.data) > 0 {
			name, ok := r.data["name"].(string)
			if !ok {
				name = "Unknown Author"
			}
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, complete
	}
	return names, complete
}

func isTransient(err error) bool {
	pe, ok := err.(*providers.Error)
	if !ok {
		return true
	}
	return pe.Status == 0 || pe.Status == http.StatusTooManyRequests || pe.Status >= 500
}

// editions lists other editions of the work (get_editions); failures give
// an empty list as a non-200 does in Python.
func (c *Client) editions(ctx context.Context, book, work map[string]any) []any {
	bookID := ExtractID(providers.Str(book, "key"))
	workID := ExtractID(providers.Str(work, "key"))
	if workID == "" {
		workID = bookID
	}
	var data struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := c.get(ctx, c.Root+"/works/"+workID+"/editions.json?limit=500", &data); err != nil {
		return []any{}
	}
	out := []any{}
	for _, e := range data.Entries {
		id := ExtractID(providers.Str(e, "key"))
		title, _ := e["title"].(string)
		if id == bookID || title == "" {
			continue
		}
		out = append(out, map[string]any{
			"source":     source,
			"source_url": "https://openlibrary.org/books/" + id,
			"media_id":   id,
			"media_type": "book",
			"title":      title,
			"image":      coverImage(e),
		})
	}
	return out
}

// ratings returns (score, count) on a 10-point scale (get_ratings).
func (c *Client) ratings(ctx context.Context, work map[string]any) (any, any) {
	workID := ExtractID(providers.Str(work, "key"))
	if workID == "" {
		return nil, nil
	}
	var data struct {
		Summary struct {
			Average float64 `json:"average"`
			Count   int     `json:"count"`
		} `json:"summary"`
	}
	if err := c.get(ctx, c.Root+"/works/"+workID+"/ratings.json", &data); err != nil {
		return nil, 0
	}
	if data.Summary.Average != 0 && data.Summary.Count != 0 {
		score, _ := strconv.ParseFloat(strconv.FormatFloat(data.Summary.Average*2, 'f', 1, 64), 64)
		return score, data.Summary.Count
	}
	return nil, 0
}

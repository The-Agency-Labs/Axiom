// Package comicvine ports src/app/providers/comicvine.py: comic volume search
// and metadata from Comic Vine.
package comicvine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

const (
	// Label is the provider name used in error messages.
	Label = "Comic Vine"
	// Source is the value of the "source" key.
	Source = "comicvine"
	// MediaType is the value of the "media_type" key.
	MediaType = "comic"

	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	perPage  = 24 // settings.PER_PAGE
	cacheTTL = 24 * time.Hour

	defaultBaseURL = "https://comicvine.gamespot.com/api"
)

// Client talks to Comic Vine.
type Client struct {
	base *providers.Client
	cfg  config.Config

	// BaseURL can be overridden (tests).
	BaseURL string
}

// New returns a Comic Vine client.
func New(base *providers.Client, cfg config.Config) *Client {
	return &Client{base: base, cfg: cfg, BaseURL: defaultBaseURL}
}

type obj = map[string]any

func (c *Client) get(ctx context.Context, path string, params url.Values) (obj, error) {
	params.Set("api_key", c.cfg.ComicVineKey)
	params.Set("format", "json")
	var out obj
	err := c.base.JSON(ctx, providers.Request{
		Provider: Label,
		URL:      c.BaseURL + path + "?" + params.Encode(),
		Header:   http.Header{"User-Agent": {"Mozilla/5.0"}},
	}, &out)
	return out, handleError(err)
}

// handleError gives a 401 the API's own message as details.
func handleError(err error) error {
	var pe *providers.Error
	if !errors.As(err, &pe) || pe.Status != http.StatusUnauthorized || pe.Details != "" || pe.Err == nil {
		return err
	}
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(pe.Err.Error()), &body) == nil && body.Error != "" {
		return &providers.Error{Provider: Label, Status: pe.Status, Details: body.Error, Err: pe.Err}
	}
	return err
}

// Search ports comicvine.search.
func (c *Client) Search(ctx context.Context, query string, page int) (providers.SearchPage, error) {
	key := fmt.Sprintf("search_comicvine_comic_%s_%d", query, page)
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		resp, err := c.get(ctx, "/search/", url.Values{
			"query":      {query},
			"resources":  {"volume"},
			"field_list": {"id,name,image"},
			"limit":      {strconv.Itoa(perPage)},
			"page":       {strconv.Itoa(page)},
		})
		if err != nil {
			return nil, err
		}
		items, _ := resp["results"].([]any)
		results := make([]providers.Metadata, 0, len(items))
		for _, it := range items {
			results = append(results, summary(asObj(it)))
		}
		total := toInt(resp["number_of_total_results"])
		return providers.SearchPage{
			"page":          page,
			"total_results": total,
			"total_pages":   total/perPage + 1,
			"results":       results,
		}, nil
	})
	return out, err
}

// Comic ports comicvine.comic: metadata for one volume. A missing volume is a
// provider 404 ("No result found with ID ...").
func (c *Client) Comic(ctx context.Context, mediaID string) (providers.Metadata, error) {
	var out providers.Metadata
	key := "comicvine_comic_" + mediaID
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		resp, err := c.get(ctx, "/volume/4050-"+mediaID+"/", url.Values{
			"field_list": {"publisher,site_detail_url,name,last_issue,image,description," +
				"concepts,start_year,count_of_issues,people,date_last_updated"},
		})
		if err != nil {
			return nil, err
		}
		r, _ := resp["results"].(obj)
		if len(r) == 0 {
			return nil, &providers.Error{Provider: Label, Status: http.StatusNotFound, Details: "No result found with ID " + mediaID}
		}

		publisher := asObj(r["publisher"])
		publisherComics := []providers.Metadata{}
		if pid := idString(publisher["id"]); pid != "" {
			if publisherComics, err = c.publisherComics(ctx, pid, mediaID, 15); err != nil {
				return nil, err
			}
		}

		lastIssue := asObj(r["last_issue"])
		var lastUpdated any
		if s, ok := r["date_last_updated"].(string); ok {
			if f := strings.Fields(s); len(f) > 0 {
				lastUpdated = f[0]
			}
		}
		var publisherName any
		if len(publisher) > 0 {
			publisherName = publisher["name"]
		}

		return providers.Metadata{
			"media_id":         mediaID,
			"source":           Source,
			"source_url":       r["site_detail_url"],
			"media_type":       MediaType,
			"title":            r["name"],
			"max_progress":     nil,
			"max_issue_number": issueNumber(lastIssue["issue_number"]),
			"image":            imageURL(r),
			"synopsis":         synopsis(r),
			"genres":           genres(r),
			"score":            nil,
			"score_count":      nil,
			"details": providers.Metadata{
				"start_date":        r["start_year"],
				"publisher":         publisherName,
				"issues_count":      r["count_of_issues"],
				"last_issue_name":   lastIssue["name"],
				"last_issue_number": lastIssue["issue_number"],
				"people":            people(r),
				"last_updated":      lastUpdated,
			},
			"related": providers.Metadata{
				"recommendations": publisherComics,
			},
			// used for events fetching
			"last_issue_id": lastIssue["id"],
		}, nil
	})
	return out, err
}

// publisherComics ports get_publisher_comics: other volumes from a publisher.
func (c *Client) publisherComics(ctx context.Context, publisherID, currentID string, limit int) ([]providers.Metadata, error) {
	var out []providers.Metadata
	key := fmt.Sprintf("comicvine_publisher_%s_%s", publisherID, currentID)
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		resp, err := c.get(ctx, "/volumes/", url.Values{
			"field_list": {"id,name,image,start_year,publisher"},
			"filter":     {"publisher:" + publisherID},
			"limit":      {strconv.Itoa(limit + 1)}, // one extra to account for the current comic
		})
		if err != nil {
			return nil, err
		}
		items, _ := resp["results"].([]any)
		data := []providers.Metadata{}
		for _, it := range items {
			o := asObj(it)
			if idString(o["id"]) == currentID {
				continue
			}
			data = append(data, summary(o))
		}
		if len(data) > limit {
			data = data[:limit]
		}
		return data, nil
	})
	return out, err
}

// Issue ports comicvine.issue: the cover and store dates of one issue.
func (c *Client) Issue(ctx context.Context, mediaID string) (providers.Metadata, error) {
	var out providers.Metadata
	key := "comicvine_issue_" + mediaID
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		resp, err := c.get(ctx, "/issue/4000-"+mediaID+"/", url.Values{
			"field_list": {"cover_date,store_date"},
		})
		if err != nil {
			return nil, err
		}
		r := asObj(resp["results"])
		return providers.Metadata{
			"cover_date": r["cover_date"],
			"store_date": r["store_date"],
		}, nil
	})
	return out, err
}

func summary(o obj) providers.Metadata {
	return providers.Metadata{
		"media_id":   idString(o["id"]),
		"source":     Source,
		"media_type": MediaType,
		"title":      o["name"],
		"image":      imageURL(o),
	}
}

func asObj(v any) obj {
	o, _ := v.(obj)
	return o
}

func toInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

// idString is Python's str(id) for a JSON number or string; "" when absent
// or zero.
func idString(v any) string {
	switch n := v.(type) {
	case float64:
		if n == 0 {
			return ""
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case string:
		return n
	}
	return ""
}

func imageURL(o obj) string {
	if img, ok := o["image"].(obj); ok {
		if u, ok := img["medium_url"].(string); ok {
			return u
		}
	}
	return ImgNone
}

func genres(o obj) any {
	concepts, ok := o["concepts"].([]any)
	if !ok {
		return nil
	}
	if len(concepts) > 5 {
		concepts = concepts[:5]
	}
	out := make([]any, 0, len(concepts))
	for _, c := range concepts {
		out = append(out, asObj(c)["name"])
	}
	return out
}

func people(o obj) []any {
	items, _ := o["people"].([]any)
	if len(items) > 5 {
		items = items[:5]
	}
	out := []any{}
	for _, p := range items {
		if m, ok := p.(obj); ok {
			out = append(out, m["name"])
		}
	}
	return out
}

// issueNumber ports get_issue_number: the number as an int, the highest part
// of a compound number such as "463-464", or nil.
func issueNumber(v any) any {
	s, ok := v.(string)
	if !ok {
		if f, isNum := v.(float64); isNum {
			return int(f)
		}
		return nil
	}
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	best, have := 0, false
	for _, part := range strings.Split(s, "-") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		if !have || n > best {
			best, have = n, true
		}
	}
	return best
}

// synopsis ports get_synopsis: the description's text with tags removed and
// whitespace collapsed.
func synopsis(o obj) string {
	desc, _ := o["description"].(string)
	if desc == "" {
		return "No synopsis available"
	}
	return strings.Join(strings.Fields(htmlText(desc)), " ")
}

// htmlText mimics BeautifulSoup(...).get_text(separator=" ") closely enough
// for Comic Vine descriptions: text nodes joined by a space, entities
// decoded, comments, script and style content dropped.
func htmlText(s string) string {
	var b strings.Builder
	skipUntil := ""
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			if skipUntil == "" {
				b.WriteString(html.UnescapeString(s))
			}
			break
		}
		if i > 0 && skipUntil == "" {
			b.WriteString(html.UnescapeString(s[:i]))
			b.WriteByte(' ')
		}
		s = s[i:]
		switch {
		case strings.HasPrefix(s, "<!--"):
			end := strings.Index(s[4:], "-->")
			if end < 0 {
				return b.String()
			}
			s = s[4+end+3:]
			continue
		case len(s) > 1 && (s[1] == '/' || s[1] == '!' || s[1] == '?' || isLetter(s[1])):
			end := tagEnd(s)
			if end < 0 {
				return b.String()
			}
			tag := strings.ToLower(s[1:end])
			s = s[end+1:]
			name := strings.TrimPrefix(tag, "/")
			if k := strings.IndexAny(name, " \t\r\n/"); k >= 0 {
				name = name[:k]
			}
			switch {
			case skipUntil != "":
				if tag == "/"+skipUntil {
					skipUntil = ""
				}
			case !strings.HasPrefix(tag, "/") && (name == "script" || name == "style"):
				skipUntil = name
			}
			b.WriteByte(' ')
		default: // a literal "<" in text
			if skipUntil == "" {
				b.WriteByte('<')
			}
			s = s[1:]
		}
	}
	return b.String()
}

func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// tagEnd returns the index of the ">" closing the tag at the start of s,
// ignoring ">" inside quoted attribute values; -1 when unterminated.
func tagEnd(s string) int {
	var quote byte
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

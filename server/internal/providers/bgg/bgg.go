// Package bgg ports src/app/providers/bgg.py: board game search and metadata
// from the BoardGameGeek XML API2 (bearer token auth). The Python does not
// special-case BGG's 202 "queued" responses, so neither does this port.
package bgg

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// Label is the provider name used in error messages.
	Label = "BoardGameGeek"
	// Source is the value of the "source" key.
	Source = "bgg"
	// MediaType is the value of the "media_type" key.
	MediaType = "boardgame"

	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	// resultsPerPage: BGG's /thing endpoint takes at most 20 ids per request.
	resultsPerPage = 20
	cacheTTL       = 24 * time.Hour

	defaultBaseURL = "https://boardgamegeek.com/xmlapi2"
)

// Client talks to BoardGameGeek.
type Client struct {
	base *providers.Client
	cfg  config.Config

	// BaseURL can be overridden (tests).
	BaseURL string
}

// New returns a BGG client.
func New(base *providers.Client, cfg config.Config) *Client {
	return &Client{base: base, cfg: cfg, BaseURL: defaultBaseURL}
}

type attr struct {
	Value *string `xml:"value,attr"`
}

func (a *attr) get() (string, bool) {
	if a == nil || a.Value == nil {
		return "", false
	}
	return *a.Value, true
}

type name struct {
	Type  string  `xml:"type,attr"`
	Value *string `xml:"value,attr"`
}

type link struct {
	Type  string `xml:"type,attr"`
	Value string `xml:"value,attr"`
}

type item struct {
	ID          string  `xml:"id,attr"`
	Names       []name  `xml:"name"`
	Thumbnail   *string `xml:"thumbnail"`
	Image       *string `xml:"image"`
	Description *string `xml:"description"`
	Year        *attr   `xml:"yearpublished"`
	MinPlayers  *attr   `xml:"minplayers"`
	MaxPlayers  *attr   `xml:"maxplayers"`
	PlayingTime *attr   `xml:"playingtime"`
	MinAge      *attr   `xml:"minage"`
	Links       []link  `xml:"link"`
	Average     *attr   `xml:"statistics>ratings>average"`
	UsersRated  *attr   `xml:"statistics>ratings>usersrated"`
}

type items struct {
	Items []item `xml:"item"`
}

func (c *Client) get(ctx context.Context, path string, params url.Values) (items, error) {
	var out items
	err := c.base.XML(ctx, providers.Request{
		Provider: Label,
		URL:      c.BaseURL + path + "?" + params.Encode(),
		Header:   http.Header{"Authorization": {"Bearer " + c.cfg.BGGToken}},
	}, &out)
	return out, handleError(err)
}

// handleError turns a 401 into BGG's "requires authorization" message.
func handleError(err error) error {
	if pe, ok := err.(*providers.Error); ok && pe.Status == http.StatusUnauthorized && pe.Details == "" {
		return &providers.Error{Provider: Label, Status: pe.Status, Details: "BGG API requires authorization", Err: pe.Err}
	}
	return err
}

type hit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Search ports bgg.search. BGG returns every match at once, so results are
// cached per query and paged client-side (20 per page).
func (c *Client) Search(ctx context.Context, query string, page int) (providers.SearchPage, error) {
	key := fmt.Sprintf("search_bgg_boardgame_%s_%d", query, page)
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		var all []hit
		resultsKey := "search_results_bgg_boardgame_" + query
		err := c.base.Cached(ctx, resultsKey, cacheTTL, &all, func() (any, error) {
			root, err := c.get(ctx, "/search", url.Values{"query": {query}, "type": {"boardgame"}})
			if err != nil {
				return nil, err
			}
			list := []hit{}
			for _, it := range root.Items {
				if len(it.Names) == 0 || it.ID == "" {
					continue
				}
				n := "Unknown"
				if it.Names[0].Value != nil {
					n = *it.Names[0].Value
				}
				list = append(list, hit{ID: it.ID, Name: n})
			}
			return list, nil
		})
		if err != nil {
			return nil, err
		}

		total := len(all)
		start := max((page-1)*resultsPerPage, 0)
		start = min(start, total)
		end := min(start+resultsPerPage, total)
		pageResults := all[start:end]

		ids := make([]string, len(pageResults))
		for i, r := range pageResults {
			ids[i] = r.ID
		}
		thumbs := c.thumbnails(ctx, ids)

		results := make([]providers.Metadata, 0, len(pageResults))
		for _, r := range pageResults {
			img, ok := thumbs[r.ID]
			if !ok {
				img = ImgNone
			}
			results = append(results, providers.Metadata{
				"media_id":   r.ID,
				"source":     Source,
				"media_type": MediaType,
				"title":      r.Name,
				"image":      img,
			})
		}
		return providers.SearchPage{
			"page":          page,
			"total_results": total,
			"total_pages":   total/resultsPerPage + 1,
			"results":       results,
		}, nil
	})
	return out, err
}

// thumbnails fetches thumbnail (or image) URLs; failures are logged and
// yield no thumbnails, as in the Python.
func (c *Client) thumbnails(ctx context.Context, ids []string) map[string]string {
	if len(ids) == 0 {
		return map[string]string{}
	}
	root, err := c.get(ctx, "/thing", url.Values{"id": {strings.Join(ids, ",")}})
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch thumbnails from BGG", "err", err)
		return map[string]string{}
	}
	out := map[string]string{}
	for _, it := range root.Items {
		if it.Thumbnail != nil && *it.Thumbnail != "" {
			out[it.ID] = *it.Thumbnail
		} else if it.Image != nil && *it.Image != "" {
			out[it.ID] = *it.Image
		}
	}
	return out
}

// Boardgame ports bgg.boardgame: metadata for one board game. A missing game
// is a provider 404 ("No result found with ID ...").
func (c *Client) Boardgame(ctx context.Context, mediaID string) (providers.Metadata, error) {
	var out providers.Metadata
	key := "bgg_boardgame_" + mediaID
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		root, err := c.get(ctx, "/thing", url.Values{"id": {mediaID}, "stats": {"1"}})
		if err != nil {
			return nil, err
		}
		if len(root.Items) == 0 {
			return nil, &providers.Error{Provider: Label, Status: http.StatusNotFound, Details: "No result found with ID " + mediaID}
		}
		it := root.Items[0]
		return providers.Metadata{
			"media_id":     mediaID,
			"source":       Source,
			"source_url":   "https://boardgamegeek.com/boardgame/" + mediaID,
			"media_type":   MediaType,
			"title":        title(it),
			"max_progress": nil,
			"image":        image(it),
			"synopsis":     description(it),
			"genres":       linkList(it, "boardgamecategory"),
			"score":        score(it),
			"score_count":  scoreCount(it),
			"details": providers.Metadata{
				"year":       year(it),
				"players":    players(it),
				"playtime":   suffixed(it.PlayingTime, " min"),
				"min_age":    suffixed(it.MinAge, "+"),
				"designers":  joined(it, "boardgamedesigner", -1),
				"publishers": joined(it, "boardgamepublisher", 3),
			},
		}, nil
	})
	return out, err
}

func title(it item) string {
	for _, n := range it.Names {
		if n.Type == "primary" {
			if n.Value == nil {
				return "Unknown"
			}
			return *n.Value
		}
	}
	return "Unknown"
}

func image(it item) string {
	if it.Image != nil && *it.Image != "" {
		return *it.Image
	}
	return ImgNone
}

func description(it item) string {
	if it.Description != nil && *it.Description != "" {
		return *it.Description
	}
	return "No synopsis available"
}

func year(it item) any {
	if v, ok := it.Year.get(); ok {
		return v
	}
	return nil
}

func players(it item) any {
	lo, _ := it.MinPlayers.get()
	hi, _ := it.MaxPlayers.get()
	if lo != "" && hi != "" {
		if lo == hi {
			return lo + " players"
		}
		return lo + "-" + hi + " players"
	}
	return nil
}

func suffixed(a *attr, suffix string) any {
	if v, _ := a.get(); v != "" {
		return v + suffix
	}
	return nil
}

func score(it item) any {
	if it.Average == nil {
		return nil
	}
	v, ok := it.Average.get()
	if !ok {
		return 0.0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return nil
	}
	// Python's round(x, 1).
	r, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 1, 64), 64)
	return r
}

func scoreCount(it item) any {
	if it.UsersRated == nil {
		return nil
	}
	v, ok := it.UsersRated.get()
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return nil
	}
	return n
}

// linkValues returns the non-empty values of links of a type; limit < 0
// means all, otherwise only the first limit links are considered.
func linkValues(it item, typ string, limit int) []string {
	var all []string
	for _, l := range it.Links {
		if l.Type == typ {
			all = append(all, l.Value)
		}
	}
	if limit >= 0 && len(all) > limit {
		all = all[:limit]
	}
	var out []string
	for _, v := range all {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func linkList(it item, typ string) any {
	vals := linkValues(it, typ, -1)
	if len(vals) == 0 {
		return nil
	}
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

func joined(it item, typ string, limit int) any {
	vals := linkValues(it, typ, limit)
	if len(vals) == 0 {
		return nil
	}
	return strings.Join(vals, ", ")
}

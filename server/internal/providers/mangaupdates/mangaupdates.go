// Package mangaupdates is the MangaUpdates manga provider, ported from
// src/app/providers/mangaupdates.py.
package mangaupdates

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	perPage  = 30
	label    = "MangaUpdates"
	source   = "mangaupdates"
	cacheTTL = 24 * time.Hour

	// DefaultURL is the MangaUpdates API root.
	DefaultURL = "https://api.mangaupdates.com/v1"
)

// Client talks to MangaUpdates.
type Client struct {
	// URL is the API root (overridable for tests).
	URL string

	base *providers.Client
	nsfw bool
}

// New returns a MangaUpdates client; cfg.MUNSFW controls adult results.
func New(base *providers.Client, cfg config.Config) *Client {
	return &Client{URL: DefaultURL, base: base, nsfw: cfg.MUNSFW}
}

type imageObj struct {
	URL struct {
		Original string `json:"original"`
	} `json:"url"`
}

func imageURL(img *imageObj) string {
	if img == nil || img.URL.Original == "" {
		return ImgNone
	}
	return img.URL.Original
}

// emptyQueryMsg is the 400 message MangaUpdates gives for an empty search.
const emptyQueryMsg = `"" must have a length between 1 and 400`

// Search searches for manga (mangaupdates.search). showAdult is the
// per-user show_adult_titles flag; adult genres are excluded unless both it
// and MU_NSFW allow them.
func (c *Client) Search(ctx context.Context, query string, page int, showAdult bool) (providers.SearchPage, error) {
	key := fmt.Sprintf("search_mangaupdates_manga_%s_%d_%s", query, page, pyBool(showAdult))
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		params := map[string]any{
			"search":  query,
			"stype":   "title",
			"perpage": perPage,
			"page":    page,
		}
		if !c.nsfw || !showAdult {
			params["exclude_genre"] = []string{"Adult", "Hentai", "Doujinshi"}
		}
		var resp struct {
			TotalHits int `json:"total_hits"`
			Results   []struct {
				Record struct {
					SeriesID int       `json:"series_id"`
					Title    string    `json:"title"`
					Image    *imageObj `json:"image"`
				} `json:"record"`
			} `json:"results"`
		}
		err := c.base.JSON(ctx, providers.Request{
			Provider: label,
			Method:   "POST",
			URL:      c.URL + "/series/search",
			Body:     params,
		}, &resp)
		if err != nil && isEmptyQuery(err) {
			err = nil
		}
		if err != nil {
			return nil, err
		}
		results := make([]any, 0, len(resp.Results))
		for _, m := range resp.Results {
			results = append(results, map[string]any{
				"media_id":   m.Record.SeriesID,
				"source":     source,
				"media_type": "manga",
				"title":      m.Record.Title,
				"image":      imageURL(m.Record.Image),
			})
		}
		return providers.SearchPage{
			"page":          page,
			"total_results": resp.TotalHits,
			"total_pages":   resp.TotalHits/perPage + 1,
			"results":       results,
		}, nil
	})
	return out, err
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// isEmptyQuery reports the 400 MangaUpdates returns for an empty search
// string, which the Python turns into an empty result set.
func isEmptyQuery(err error) bool {
	pe, ok := err.(*providers.Error)
	if !ok || pe.Status != 400 || pe.Err == nil {
		return false
	}
	var body struct {
		Context struct {
			Search []struct {
				Errors []string `json:"errors"`
			} `json:"search"`
		} `json:"context"`
	}
	if json.Unmarshal([]byte(pe.Err.Error()), &body) != nil {
		return false
	}
	s := body.Context.Search
	return len(s) > 0 && len(s[0].Errors) > 0 && s[0].Errors[0] == emptyQueryMsg
}

type series struct {
	URL           string    `json:"url"`
	Title         string    `json:"title"`
	Image         *imageObj `json:"image"`
	Description   *string   `json:"description"`
	Completed     bool      `json:"completed"`
	LatestChapter *int      `json:"latest_chapter"`
	Genres        []struct {
		Genre string `json:"genre"`
	} `json:"genres"`
	Bayesian    float64 `json:"bayesian_rating"`
	RatingVotes *int    `json:"rating_votes"`
	Type        *string `json:"type"`
	Authors     []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Year    *string `json:"year"`
	Status  *string `json:"status"`
	Related []struct {
		ID   int     `json:"related_series_id"`
		Name *string `json:"related_series_name"`
	} `json:"related_series"`
	Recommendations []struct {
		ID   int     `json:"series_id"`
		Name *string `json:"series_name"`
	} `json:"recommendations"`
}

// Manga returns metadata for a series (mangaupdates.manga).
func (c *Client) Manga(ctx context.Context, mediaID string) (providers.Metadata, error) {
	key := "mangaupdates_manga_" + mediaID
	var out providers.Metadata
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		var s series
		if err := c.base.JSON(ctx, providers.Request{Provider: label, URL: c.URL + "/series/" + mediaID}, &s); err != nil {
			return nil, err
		}

		var wg sync.WaitGroup
		var related, recs []any
		wg.Add(2)
		go func() {
			defer wg.Done()
			var items []seriesRef
			for _, r := range s.Related {
				if r.Name != nil && *r.Name != "" {
					items = append(items, seriesRef{r.ID, *r.Name})
				}
			}
			related = c.fetchRefs(ctx, items)
		}()
		go func() {
			defer wg.Done()
			var items []seriesRef
			for _, r := range s.Recommendations {
				if r.Name != nil && *r.Name != "" {
					items = append(items, seriesRef{r.ID, *r.Name})
				}
			}
			recs = c.fetchRefs(ctx, items)
		}()
		wg.Wait()

		var maxProgress, synopsis, format, year, latest, scoreCount any
		if s.Completed && s.LatestChapter != nil {
			maxProgress = *s.LatestChapter
		}
		if s.Description != nil {
			synopsis = *s.Description
		}
		if s.Type != nil {
			format = *s.Type
		}
		if s.Year != nil {
			year = *s.Year
		}
		if s.LatestChapter != nil {
			latest = *s.LatestChapter
		}
		if s.RatingVotes != nil {
			scoreCount = *s.RatingVotes
		}
		return providers.Metadata{
			"media_id":     mediaID,
			"source":       source,
			"source_url":   s.URL,
			"media_type":   "manga",
			"title":        s.Title,
			"image":        imageURL(s.Image),
			"synopsis":     synopsis,
			"max_progress": maxProgress,
			"genres":       genres(s),
			"score":        score(s.Bayesian),
			"score_count":  scoreCount,
			"details": map[string]any{
				"format":                      format,
				"authors":                     authors(s),
				"year":                        year,
				"status_in_country_of_origin": status(s.Status),
				"latest_chapter_translated":   latest,
			},
			"related": map[string]any{
				"related_manga":   related,
				"recommendations": recs,
			},
		}, nil
	})
	return out, err
}

func genres(s series) any {
	if len(s.Genres) == 0 {
		return nil
	}
	out := make([]any, len(s.Genres))
	for i, g := range s.Genres {
		out[i] = g.Genre
	}
	return out
}

func authors(s series) any {
	if len(s.Authors) == 0 {
		return nil
	}
	out := make([]any, len(s.Authors))
	for i, a := range s.Authors {
		out[i] = a.Name
	}
	return out
}

var volumesRe = regexp.MustCompile(`(\d+\s+Volumes\s+\([^)]+\))`)

// status ports get_status: pulls "N Volumes (Complete)" out of the longer
// status text when present, otherwise returns it unchanged.
func status(s *string) any {
	if s == nil {
		return nil
	}
	if m := volumesRe.FindString(*s); m != "" {
		return m
	}
	return *s
}

// score ports get_score: nil when unrated, else rounded to one decimal.
func score(v float64) any {
	if v == 0 {
		return nil
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 1, 64), 64)
	return r
}

type seriesRef struct {
	id   int
	name string
}

// fetchRefs fetches each series (for its image) concurrently, keeping order
// and dropping the ones that fail, as fetch_series_data does for non-200s.
func (c *Client) fetchRefs(ctx context.Context, refs []seriesRef) []any {
	results := make([]any, len(refs))
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var s struct {
				Image *imageObj `json:"image"`
			}
			if err := c.base.JSON(ctx, providers.Request{Provider: label, URL: c.URL + "/series/" + strconv.Itoa(ref.id)}, &s); err != nil {
				return
			}
			results[i] = map[string]any{
				"source":     source,
				"media_id":   ref.id,
				"media_type": "manga",
				"title":      ref.name,
				"image":      imageURL(s.Image),
			}
		}()
	}
	wg.Wait()
	out := []any{}
	for _, r := range results {
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

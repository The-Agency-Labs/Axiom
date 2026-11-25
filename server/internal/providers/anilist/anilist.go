// Package anilist ports src/app/providers/anilist.py: AniList search and
// anime/manga metadata over GraphQL, plus the AniList parts of
// src/catalog/anime.py (streaming episodes) and src/catalog/credits.py
// (characters and voice actors).
package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // zone database for TZ on scratch images

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// Label is the provider name shown in errors.
	Label = "AniList"
	// Source is the item source value.
	Source = "anilist"

	baseURL = "https://graphql.anilist.co"
	// PerPage is the page size of search results (settings.PER_PAGE).
	PerPage = 24
	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	cacheTTL = 24 * time.Hour
)

const mediaCardFragment = `
fragment MediaCard on Media {
    id
    type
    title {
        userPreferred
    }
    coverImage {
        large
    }
}
`

const searchQuery = `
query (
    $search: String
    $type: MediaType
    $page: Int
    $perPage: Int
    $isAdult: Boolean
) {
    Page(page: $page, perPage: $perPage) {
        pageInfo {
            hasNextPage
        }
        media(search: $search, type: $type, isAdult: $isAdult, sort: SEARCH_MATCH) {
            ...MediaCard
        }
    }
}
` + mediaCardFragment

const mediaQuery = `
query ($id: Int, $type: MediaType) {
    Media(id: $id, type: $type) {
        id
        idMal
        siteUrl
        title {
            userPreferred
        }
        coverImage {
            extraLarge
            large
        }
        description
        format
        status
        startDate {
            year
            month
            day
        }
        endDate {
            year
            month
            day
        }
        season
        seasonYear
        episodes
        duration
        chapters
        source
        genres
        averageScore
        stats {
            scoreDistribution {
                amount
            }
        }
        studios(isMain: true) {
            nodes {
                name
            }
        }
        nextAiringEpisode {
            airingAt
        }
        relations {
            edges {
                node {
                    ...MediaCard
                }
            }
        }
        recommendations(sort: RATING_DESC) {
            nodes {
                mediaRecommendation {
                    ...MediaCard
                }
            }
        }
    }
}
` + mediaCardFragment

// Client talks to AniList. Safe for concurrent use.
type Client struct {
	base *providers.Client
	cfg  config.Config
	loc  *time.Location
	url  string
}

// New returns a Client that makes every call through base.
func New(base *providers.Client, cfg config.Config) *Client {
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	return &Client{base: base, cfg: cfg, loc: loc, url: baseURL}
}

// GraphQL sends a query to AniList and returns the "data" object. HTTP
// errors carry the first GraphQL error message as their details.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	body, err := c.base.Do(ctx, providers.Request{
		Provider: Label, Method: http.MethodPost, URL: c.url,
		Body: map[string]any{"query": query, "variables": variables},
	})
	if err != nil {
		return nil, handleError(err)
	}
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, &providers.Error{Provider: Label, Status: http.StatusOK, Details: "invalid JSON response", Err: err}
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return nil, &providers.Error{Provider: Label, Status: http.StatusOK, Details: "response has no data"}
	}
	return resp.Data, nil
}

// handleError adds the GraphQL error message to an HTTP error, like the
// Python handle_error. A body that is not JSON leaves the error unchanged.
func handleError(err error) error {
	var pe *providers.Error
	if !errors.As(err, &pe) || pe.Status == 0 || pe.Err == nil {
		return err
	}
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(pe.Err.Error()), &payload) != nil {
		return err
	}
	details := ""
	if len(payload.Errors) > 0 {
		details = payload.Errors[0].Message
	}
	return &providers.Error{Provider: pe.Provider, Status: pe.Status, Details: details, Err: pe.Err}
}

type card struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Title struct {
		UserPreferred string `json:"userPreferred"`
	} `json:"title"`
	CoverImage struct {
		ExtraLarge *string `json:"extraLarge"`
		Large      *string `json:"large"`
	} `json:"coverImage"`
}

type fuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

type media struct {
	card
	IDMal        *int      `json:"idMal"`
	SiteURL      string    `json:"siteUrl"`
	Description  *string   `json:"description"`
	Format       *string   `json:"format"`
	Status       *string   `json:"status"`
	StartDate    fuzzyDate `json:"startDate"`
	EndDate      fuzzyDate `json:"endDate"`
	Season       *string   `json:"season"`
	SeasonYear   *int      `json:"seasonYear"`
	Episodes     *int      `json:"episodes"`
	Duration     *int      `json:"duration"`
	Chapters     *int      `json:"chapters"`
	Source       *string   `json:"source"`
	Genres       []string  `json:"genres"`
	AverageScore *int      `json:"averageScore"`
	Stats        struct {
		ScoreDistribution []struct {
			Amount int `json:"amount"`
		} `json:"scoreDistribution"`
	} `json:"stats"`
	Studios struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"studios"`
	NextAiringEpisode *struct {
		AiringAt int64 `json:"airingAt"`
	} `json:"nextAiringEpisode"`
	Relations struct {
		Edges []struct {
			Node card `json:"node"`
		} `json:"edges"`
	} `json:"relations"`
	Recommendations struct {
		Nodes []struct {
			MediaRecommendation *card `json:"mediaRecommendation"`
		} `json:"nodes"`
	} `json:"recommendations"`
}

// Search searches AniList for "anime" or "manga".
func (c *Client) Search(ctx context.Context, mediaType, query string, page int, showAdult bool) (providers.SearchPage, error) {
	if page < 1 {
		page = 1
	}
	key := fmt.Sprintf("search_%s_%s_%s_%d_%s", Source, mediaType, query, page, pyBool(showAdult))
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		variables := map[string]any{
			"search":  query,
			"type":    strings.ToUpper(mediaType),
			"page":    page,
			"perPage": PerPage,
		}
		// AniList filters by null instead of ignoring it, so leave it out to get all.
		if !c.cfg.AniListNSFW || !showAdult {
			variables["isAdult"] = false
		}
		data, err := c.GraphQL(ctx, searchQuery, variables)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Page struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Media []card `json:"media"`
			} `json:"Page"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		results := make([]providers.Metadata, 0, len(resp.Page.Media))
		for _, m := range resp.Page.Media {
			results = append(results, mediaCard(m, mediaType))
		}
		// AniList caps the total results, so only count up to the next page.
		total := (page-1)*PerPage + len(results)
		if resp.Page.PageInfo.HasNextPage {
			total += PerPage
		}
		return formatSearchResponse(page, total, results), nil
	})
	return out, err
}

// Anime returns the metadata for an AniList anime.
func (c *Client) Anime(ctx context.Context, mediaID string) (providers.Metadata, error) {
	return c.metadata(ctx, "anime", mediaID)
}

// Manga returns the metadata for an AniList manga.
func (c *Client) Manga(ctx context.Context, mediaID string) (providers.Metadata, error) {
	return c.metadata(ctx, "manga", mediaID)
}

// Metadata is services.get_media_metadata for AniList: mediaType is "anime" or "manga".
func (c *Client) Metadata(ctx context.Context, mediaType, mediaID string) (providers.Metadata, error) {
	switch mediaType {
	case "anime", "manga":
		return c.metadata(ctx, mediaType, mediaID)
	}
	return nil, fmt.Errorf("anilist: unsupported media type %q", mediaType)
}

func (c *Client) metadata(ctx context.Context, mediaType, mediaID string) (providers.Metadata, error) {
	var out providers.Metadata
	err := c.base.Cached(ctx, Source+"_"+mediaType+"_"+mediaID, cacheTTL, &out, func() (any, error) {
		m, err := c.getMedia(ctx, mediaID, mediaType)
		if err != nil {
			return nil, err
		}
		data := c.baseMetadata(m, mediaID, mediaType)
		if mediaType == "anime" {
			data["max_progress"] = intOrNil(m.Episodes)
			data["details"] = map[string]any{
				"format":     format(m),
				"start_date": date(m.StartDate),
				"end_date":   date(m.EndDate),
				"status":     readableStatus(m, mediaType),
				"episodes":   intOrNil(m.Episodes),
				"runtime":    runtime(m),
				"studios":    studios(m),
				"season":     season(m),
				"broadcast":  c.broadcast(m),
				"source":     sourceName(m),
			}
			data["related"] = map[string]any{
				"related_anime":   related(m, "anime"),
				"related_manga":   related(m, "manga"),
				"recommendations": recommendations(m, "anime"),
			}
		} else {
			data["max_progress"] = intOrNil(m.Chapters)
			data["details"] = map[string]any{
				"format":             format(m),
				"start_date":         date(m.StartDate),
				"end_date":           date(m.EndDate),
				"status":             readableStatus(m, mediaType),
				"number_of_chapters": intOrNil(m.Chapters),
			}
			data["related"] = map[string]any{
				"related_manga":   related(m, "manga"),
				"related_anime":   related(m, "anime"),
				"recommendations": recommendations(m, "manga"),
			}
		}
		return data, nil
	})
	return out, err
}

func (c *Client) getMedia(ctx context.Context, mediaID, mediaType string) (media, error) {
	id, err := strconv.Atoi(strings.TrimSpace(mediaID))
	if err != nil {
		return media{}, fmt.Errorf("anilist: invalid media id %q", mediaID)
	}
	data, err := c.GraphQL(ctx, mediaQuery, map[string]any{"id": id, "type": strings.ToUpper(mediaType)})
	if err != nil {
		return media{}, err
	}
	var resp struct {
		Media *media `json:"Media"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return media{}, err
	}
	if resp.Media == nil {
		return media{}, providers.NotFoundError(Label, mediaType, mediaID)
	}
	return *resp.Media, nil
}

func (c *Client) baseMetadata(m media, mediaID, mediaType string) providers.Metadata {
	var genres any
	if len(m.Genres) > 0 {
		genres = m.Genres
	}
	return providers.Metadata{
		"media_id":       mediaID,
		"source":         Source,
		"source_url":     m.SiteURL,
		"media_type":     mediaType,
		"title":          m.Title.UserPreferred,
		"image":          imageURL(m.card),
		"synopsis":       synopsis(m),
		"genres":         genres,
		"score":          score(m),
		"score_count":    scoreCount(m),
		"external_links": externalLinks(m, mediaType),
	}
}

func mediaCard(m card, mediaType string) providers.Metadata {
	return providers.Metadata{
		"media_id":   m.ID,
		"source":     Source,
		"media_type": mediaType,
		"title":      m.Title.UserPreferred,
		"image":      imageURL(m),
	}
}

func imageURL(m card) string {
	if m.CoverImage.ExtraLarge != nil && *m.CoverImage.ExtraLarge != "" {
		return *m.CoverImage.ExtraLarge
	}
	if m.CoverImage.Large != nil && *m.CoverImage.Large != "" {
		return *m.CoverImage.Large
	}
	return ImgNone
}

var (
	brTag  = regexp.MustCompile(`<br\s*/?>\s*`)
	anyTag = regexp.MustCompile(`<[^>]*>`)
)

// synopsis returns the description as plain text. Descriptions include HTML
// like <br> and <i>, and can be null.
func synopsis(m media) string {
	if m.Description == nil || *m.Description == "" {
		return "No synopsis available."
	}
	d := brTag.ReplaceAllString(*m.Description, "\n")
	d = anyTag.ReplaceAllString(d, "")
	return strings.TrimSpace(html.UnescapeString(d))
}

func format(m media) any {
	if m.Format == nil || *m.Format == "" {
		return nil
	}
	switch f := *m.Format; f {
	case "TV":
		return "TV"
	case "TV_SHORT":
		return "TV Short"
	case "OVA":
		return "OVA"
	case "ONA":
		return "ONA"
	case "NOVEL":
		return "Light Novel"
	default:
		return pyTitle(strings.ReplaceAll(f, "_", " "))
	}
}

func readableStatus(m media, mediaType string) any {
	if m.Status == nil || *m.Status == "" {
		return nil
	}
	switch s := *m.Status; s {
	case "FINISHED":
		return "Finished"
	case "RELEASING":
		if mediaType == "anime" {
			return "Airing"
		}
		return "Publishing"
	case "NOT_YET_RELEASED":
		return "Upcoming"
	case "CANCELLED":
		return "Discontinued"
	case "HIATUS":
		return "On Hiatus"
	default:
		return pyTitle(strings.ReplaceAll(s, "_", " "))
	}
}

// date renders an AniList fuzzy date as YYYY-MM-DD, YYYY-MM or YYYY.
func date(d fuzzyDate) any {
	if d.Year == nil || *d.Year == 0 {
		return nil
	}
	if d.Month == nil || *d.Month == 0 {
		return strconv.Itoa(*d.Year)
	}
	if d.Day == nil || *d.Day == 0 {
		return fmt.Sprintf("%d-%02d", *d.Year, *d.Month)
	}
	return fmt.Sprintf("%d-%02d-%02d", *d.Year, *d.Month, *d.Day)
}

// runtime is the average episode duration (minutes, null when unknown).
func runtime(m media) any {
	if m.Duration == nil || *m.Duration == 0 {
		return nil
	}
	hours, minutes := *m.Duration/60, *m.Duration%60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%d min", minutes)
}

func studios(m media) any {
	if len(m.Studios.Nodes) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.Studios.Nodes))
	for _, s := range m.Studios.Nodes {
		names = append(names, s.Name)
	}
	return names
}

func season(m media) any {
	if m.Season != nil && *m.Season != "" && m.SeasonYear != nil && *m.SeasonYear != 0 {
		return fmt.Sprintf("%s %d", pyTitle(*m.Season), *m.SeasonYear)
	}
	return nil
}

// broadcast is the weekday and time of the next episode, in the configured zone.
func (c *Client) broadcast(m media) any {
	if m.NextAiringEpisode == nil {
		return nil
	}
	return time.Unix(m.NextAiringEpisode.AiringAt, 0).In(c.loc).Format("Monday 15:04")
}

func sourceName(m media) any {
	if m.Source != nil && *m.Source != "" {
		return pyTitle(strings.ReplaceAll(*m.Source, "_", " "))
	}
	return nil
}

// score is the average score on a 0-10 scale, null with too few ratings.
func score(m media) any {
	if m.AverageScore == nil || *m.AverageScore == 0 {
		return nil
	}
	return round1(float64(*m.AverageScore) / 10)
}

func scoreCount(m media) int {
	if score(m) == nil {
		return 0
	}
	n := 0
	for _, s := range m.Stats.ScoreDistribution {
		n += s.Amount
	}
	return n
}

func externalLinks(m media, mediaType string) map[string]any {
	if m.IDMal != nil && *m.IDMal != 0 {
		return map[string]any{"MyAnimeList": fmt.Sprintf("https://myanimelist.net/%s/%d", mediaType, *m.IDMal)}
	}
	return map[string]any{}
}

func related(m media, mediaType string) []providers.Metadata {
	out := []providers.Metadata{}
	for _, e := range m.Relations.Edges {
		if e.Node.Type == strings.ToUpper(mediaType) {
			out = append(out, mediaCard(e.Node, mediaType))
		}
	}
	return out
}

// recommendations skips deleted media (mediaRecommendation is null).
func recommendations(m media, mediaType string) []providers.Metadata {
	out := []providers.Metadata{}
	for _, n := range m.Recommendations.Nodes {
		if n.MediaRecommendation != nil {
			out = append(out, mediaCard(*n.MediaRecommendation, mediaType))
		}
	}
	return out
}

func intOrNil(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func formatSearchResponse(page, total int, results []providers.Metadata) providers.SearchPage {
	return providers.SearchPage{
		"page":          page,
		"total_results": total,
		"total_pages":   total/PerPage + 1,
		"results":       results,
	}
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// round1 rounds to one decimal like Python's round(x, 1) (correctly rounded, ties to even).
func round1(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 1, 64), 64)
	return v
}

// pyTitle mimics Python's str.title(): the first letter of each run of
// letters is upper-cased and the rest lower-cased.
func pyTitle(s string) string {
	var b strings.Builder
	prevLetter := false
	for _, r := range s {
		isLetter := strings.ToUpper(string(r)) != strings.ToLower(string(r))
		switch {
		case !isLetter:
			b.WriteRune(r)
		case prevLetter:
			b.WriteString(strings.ToLower(string(r)))
		default:
			b.WriteString(strings.ToUpper(string(r)))
		}
		prevLetter = isLetter
	}
	return b.String()
}

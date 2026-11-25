// Package mal ports src/app/providers/mal.py: MyAnimeList search and
// anime/manga metadata.
package mal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // zone database for TZ and Asia/Tokyo on scratch images

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// Label is the provider name shown in errors.
	Label = "MyAnimeList"
	// Source is the item source value.
	Source = "mal"

	baseURL = "https://api.myanimelist.net/v2"
	// PerPage is the page size of search results (settings.PER_PAGE).
	PerPage = 24
	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	baseFields = "title,alternative_titles,main_picture,media_type,start_date,end_date,synopsis,status,genres,mean,num_scoring_users,recommendations"
	cacheTTL   = 24 * time.Hour
)

// Client talks to the MyAnimeList API. Safe for concurrent use.
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

// get calls the API and decodes the response. HTTP errors are rewritten like
// the Python handle_error; an "invalid q" search error returns a nil body
// with ok=false so Search can treat it as no results.
func (c *Client) get(ctx context.Context, path string, params url.Values) (body []byte, err error) {
	return c.base.Do(ctx, providers.Request{
		Provider: Label,
		URL:      c.url + path + "?" + params.Encode(),
		Header:   http.Header{"X-Mal-Client-Id": {c.cfg.MALKey}},
	})
}

// handleError ports handle_error. It returns (empty search page, true) for
// the "invalid q" error and the rewritten error otherwise.
func handleError(err error) (invalidQuery bool, out error) {
	var pe *providers.Error
	if !errors.As(err, &pe) || pe.Status == 0 || pe.Err == nil {
		return false, err
	}
	var payload struct {
		Message string `json:"message"`
	}
	// A body that is not JSON gives the bare error (no details).
	if json.Unmarshal([]byte(pe.Err.Error()), &payload) != nil {
		return false, &providers.Error{Provider: pe.Provider, Status: pe.Status, Err: pe.Err}
	}
	switch {
	case pe.Status == http.StatusForbidden:
		return false, &providers.Error{Provider: pe.Provider, Status: pe.Status, Details: "API key is missing", Err: pe.Err}
	case pe.Status == http.StatusBadRequest && payload.Message == "Invalid client id":
		return false, &providers.Error{Provider: pe.Provider, Status: pe.Status, Details: "Invalid API key", Err: pe.Err}
	case pe.Status == http.StatusBadRequest && payload.Message == "invalid q":
		return true, nil
	}
	return false, &providers.Error{Provider: pe.Provider, Status: pe.Status, Err: pe.Err}
}

type picture struct {
	Large *string `json:"large"`
}

type node struct {
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	AltTitles   *altTitles `json:"alternative_titles"`
	MainPicture *picture   `json:"main_picture"`
}

// altTitles are MAL's other names for an entry; En is the official English
// title, empty when there is none.
type altTitles struct {
	En       string   `json:"en"`
	Ja       string   `json:"ja"`
	Synonyms []string `json:"synonyms"`
}

// aliases lists every name of an entry (main, English, Japanese, synonyms),
// used to tell whether another provider's hit is the same title.
func aliases(main string, alt *altTitles) []string {
	out := []string{main}
	if alt != nil {
		out = append(out, alt.En, alt.Ja)
		out = append(out, alt.Synonyms...)
	}
	return out
}

// displayTitles returns the title to show and the original one to keep as
// a subtitle: the English title when MAL has one that differs, else MAL's
// main (usually romanized) title and no subtitle.
func displayTitles(main string, alt *altTitles) (title, native string) {
	if alt != nil {
		if en := strings.TrimSpace(alt.En); en != "" && !strings.EqualFold(en, strings.TrimSpace(main)) {
			return en, main
		}
	}
	return main, ""
}

type edge struct {
	Node node `json:"node"`
}

func imageURL(n node) string {
	if n.MainPicture != nil && n.MainPicture.Large != nil {
		return *n.MainPicture.Large
	}
	return ImgNone
}

// Search searches MyAnimeList; mediaType is "anime" or "manga".
func (c *Client) Search(ctx context.Context, mediaType, query string, page int, showAdult bool) (providers.SearchPage, error) {
	if page < 1 {
		page = 1
	}
	key := fmt.Sprintf("search3_%s_%s_%s_%d_%s", Source, mediaType, query, page, pyBool(showAdult))
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		params := url.Values{
			"q":      {query},
			"fields": {"media_type,alternative_titles"},
			"limit":  {strconv.Itoa(PerPage)},
			"offset": {strconv.Itoa((page - 1) * PerPage)},
		}
		if c.cfg.MALNSFW && showAdult {
			params.Set("nsfw", "true")
		}
		var resp struct {
			Data []edge `json:"data"`
		}
		body, err := c.get(ctx, "/"+mediaType, params)
		if err != nil {
			invalid, herr := handleError(err)
			if !invalid {
				return nil, herr
			}
		} else if err := json.Unmarshal(body, &resp); err != nil {
			return nil, &providers.Error{Provider: Label, Status: http.StatusOK, Details: "invalid JSON response", Err: err}
		}
		results := make([]providers.Metadata, 0, len(resp.Data))
		for _, m := range resp.Data {
			title, native := displayTitles(m.Node.Title, m.Node.AltTitles)
			md := providers.Metadata{
				"media_id":   m.Node.ID,
				"source":     Source,
				"media_type": mediaType,
				"title":      title,
				"image":      imageURL(m.Node),
				"aliases":    aliases(m.Node.Title, m.Node.AltTitles),
			}
			if native != "" {
				md["native_title"] = native
			}
			results = append(results, md)
		}
		total := PerPage * 3 // MAL has no limit, we set a max of 3 pages
		return providers.SearchPage{
			"page":          page,
			"total_results": total,
			"total_pages":   total/PerPage + 1,
			"results":       results,
		}, nil
	})
	return out, err
}

type genre struct {
	Name string `json:"name"`
}

type response struct {
	Title           string     `json:"title"`
	AltTitles       *altTitles `json:"alternative_titles"`
	MainPicture     *picture   `json:"main_picture"`
	MediaType       *string    `json:"media_type"`
	StartDate       *string    `json:"start_date"`
	EndDate         *string    `json:"end_date"`
	Synopsis        *string    `json:"synopsis"`
	Status          *string    `json:"status"`
	Genres          []genre    `json:"genres"`
	Mean            *float64   `json:"mean"`
	NumScoringUsers int        `json:"num_scoring_users"`
	NumEpisodes     *int       `json:"num_episodes"`
	NumChapters     *int       `json:"num_chapters"`
	AvgEpisodeDur   *float64   `json:"average_episode_duration"`
	Studios         []genre    `json:"studios"`
	StartSeason     *struct {
		Year   int    `json:"year"`
		Season string `json:"season"`
	} `json:"start_season"`
	Broadcast *struct {
		StartTime *string `json:"start_time"`
	} `json:"broadcast"`
	Source          *string `json:"source"`
	RelatedAnime    []edge  `json:"related_anime"`
	RelatedManga    []edge  `json:"related_manga"`
	Recommendations []edge  `json:"recommendations"`
}

// Anime returns the metadata for a MyAnimeList anime.
func (c *Client) Anime(ctx context.Context, mediaID string) (providers.Metadata, error) {
	return c.metadata(ctx, "anime", mediaID)
}

// Manga returns the metadata for a MyAnimeList manga.
func (c *Client) Manga(ctx context.Context, mediaID string) (providers.Metadata, error) {
	return c.metadata(ctx, "manga", mediaID)
}

// Metadata is services.get_media_metadata for MAL: mediaType is "anime" or "manga".
func (c *Client) Metadata(ctx context.Context, mediaType, mediaID string) (providers.Metadata, error) {
	switch mediaType {
	case "anime", "manga":
		return c.metadata(ctx, mediaType, mediaID)
	}
	return nil, fmt.Errorf("mal: unsupported media type %q", mediaType)
}

func (c *Client) metadata(ctx context.Context, mediaType, mediaID string) (providers.Metadata, error) {
	var out providers.Metadata
	err := c.base.Cached(ctx, Source+"2_"+mediaType+"_"+mediaID, cacheTTL, &out, func() (any, error) {
		fields := baseFields + ",num_chapters,related_manga,recommendations"
		if mediaType == "anime" {
			fields = baseFields + ",num_episodes,average_episode_duration,studios,start_season,broadcast,source,related_anime"
		}
		body, err := c.get(ctx, "/"+mediaType+"/"+url.PathEscape(mediaID), url.Values{"fields": {fields}})
		if err != nil {
			_, herr := handleError(err)
			return nil, herr
		}
		var r response
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, &providers.Error{Provider: Label, Status: http.StatusOK, Details: "invalid JSON response", Err: err}
		}
		count := numberOfEpisodes(r)
		title, native := displayTitles(r.Title, r.AltTitles)
		data := providers.Metadata{
			"media_id":     mediaID,
			"source":       Source,
			"source_url":   fmt.Sprintf("https://myanimelist.net/%s/%s", mediaType, mediaID),
			"media_type":   mediaType,
			"title":        title,
			"native_title": native,
			"max_progress": count,
			"image":        imageURL(node{MainPicture: r.MainPicture}),
			"synopsis":     synopsis(r),
			"genres":       genres(r),
			"score":        score(r),
			"score_count":  scoreCount(r),
		}
		if mediaType == "anime" {
			data["details"] = map[string]any{
				"format":     format(r),
				"start_date": strOrNil(r.StartDate),
				"end_date":   strOrNil(r.EndDate),
				"status":     readableStatus(r),
				"episodes":   count,
				"runtime":    runtime(r),
				"studios":    studios(r),
				"season":     season(r),
				"broadcast":  c.broadcast(r),
				"source":     sourceName(r),
			}
			data["related"] = map[string]any{
				"related_anime":   relatedList(r.RelatedAnime, "anime"),
				"recommendations": relatedList(r.Recommendations, "anime"),
			}
		} else {
			data["details"] = map[string]any{
				"format":             format(r),
				"start_date":         strOrNil(r.StartDate),
				"end_date":           strOrNil(r.EndDate),
				"status":             readableStatus(r),
				"number_of_chapters": count,
			}
			data["related"] = map[string]any{
				"related_manga":   relatedList(r.RelatedManga, "manga"),
				"recommendations": relatedList(r.Recommendations, "manga"),
			}
		}
		return data, nil
	})
	return out, err
}

func format(r response) any {
	if r.MediaType == nil {
		return nil
	}
	switch t := *r.MediaType; t {
	case "tv":
		return "Anime" // MAL returns tv in metadata for anime
	case "ova", "ona":
		return strings.ToUpper(t)
	default:
		return pyTitle(strings.ReplaceAll(t, "_", " "))
	}
}

func readableStatus(r response) any {
	if r.Status == nil {
		return nil
	}
	switch s := *r.Status; s {
	case "finished_airing", "finished":
		return "Finished"
	case "currently_airing":
		return "Airing"
	case "not_yet_aired", "not_yet_published":
		return "Upcoming"
	case "currently_publishing":
		return "Publishing"
	case "on_hiatus":
		return "On Hiatus"
	case "discontinued":
		return "Discontinued"
	default:
		return pyTitle(strings.ReplaceAll(s, "_", " "))
	}
}

// synopsis: MAL returns an empty string when there is none.
func synopsis(r response) string {
	if r.Synopsis == nil || *r.Synopsis == "" {
		return "No synopsis available."
	}
	return *r.Synopsis
}

// numberOfEpisodes returns the episode (or chapter) count; MAL sends 0 when unknown.
func numberOfEpisodes(r response) any {
	n := r.NumEpisodes
	if n == nil {
		n = r.NumChapters
	}
	if n == nil || *n == 0 {
		return nil
	}
	return *n
}

// runtime is the average episode duration; MAL sends seconds, and 0 when unknown.
func runtime(r response) any {
	if r.AvgEpisodeDur == nil || *r.AvgEpisodeDur == 0 {
		return nil
	}
	hours, minutes := int(*r.AvgEpisodeDur/60)/60, int(*r.AvgEpisodeDur/60)%60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%d min", minutes)
}

func genres(r response) any {
	if len(r.Genres) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.Genres))
	for _, g := range r.Genres {
		names = append(names, g.Name)
	}
	return names
}

func studios(r response) any {
	if len(r.Studios) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.Studios))
	for _, s := range r.Studios {
		names = append(names, s.Name)
	}
	return names
}

func season(r response) any {
	if r.StartSeason == nil {
		return nil
	}
	return fmt.Sprintf("%s %d", pyTitle(r.StartSeason.Season), r.StartSeason.Year)
}

// broadcast converts the Japanese broadcast time of the premiere day to the
// configured zone and renders "Monday 15:04".
func (c *Client) broadcast(r response) any {
	if r.StartDate == nil || *r.StartDate == "" || r.Broadcast == nil ||
		r.Broadcast.StartTime == nil || *r.Broadcast.StartTime == "" {
		return nil
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return nil
	}
	day, err := time.ParseInLocation("2006-01-02", *r.StartDate, tokyo)
	if err != nil {
		if day, err = time.ParseInLocation("2006-01", *r.StartDate, tokyo); err != nil {
			return nil
		}
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", day.Format("2006-01-02")+" "+*r.Broadcast.StartTime, tokyo)
	if err != nil {
		return nil
	}
	return at.In(c.loc).Format("Monday 15:04")
}

func sourceName(r response) any {
	if r.Source == nil {
		return nil
	}
	return pyTitle(strings.ReplaceAll(*r.Source, "_", " "))
}

// score is the mean score; MAL leaves it out when there are few ratings.
func score(r response) any {
	if r.Mean == nil {
		return nil
	}
	return round1(*r.Mean)
}

func scoreCount(r response) int {
	if r.Mean == nil || round1(*r.Mean) == 0 {
		return 0
	}
	return r.NumScoringUsers
}

func relatedList(edges []edge, mediaType string) []providers.Metadata {
	out := []providers.Metadata{}
	for _, e := range edges {
		out = append(out, providers.Metadata{
			"media_id":   e.Node.ID,
			"source":     Source,
			"title":      e.Node.Title,
			"media_type": mediaType,
			"image":      imageURL(e.Node),
		})
	}
	return out
}

func strOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
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

// pyTitle mimics Python's str.title().
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

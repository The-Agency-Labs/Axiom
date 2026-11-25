// Package tmdb ports src/app/providers/tmdb.py (The Movie Database) and the
// TMDB parts of src/app/providers/services.py. Results are providers.Metadata
// maps with the Django app's exact keys.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Label is the provider's display name in error messages (Sources.TMDB.label).
const Label = "The Movie Database"

// cacheTTL is Django's default CACHE_TIMEOUT (24 hours).
const cacheTTL = 24 * time.Hour

const maxSeasonsPerRequest = 8

// Client talks to TMDB through the shared provider client.
type Client struct {
	base *providers.Client
	cfg  config.Config
	// BaseURL is the API root; overridable for tests.
	BaseURL string
	// Location is used for "today" in change feeds; defaults to cfg.TimeZone.
	Now func() time.Time
}

// New returns a TMDB client.
func New(base *providers.Client, cfg config.Config) *Client {
	c := &Client{base: base, cfg: cfg, BaseURL: "https://api.themoviedb.org/3"}
	c.Now = func() time.Time {
		if loc, err := time.LoadLocation(cfg.TimeZone); err == nil {
			return time.Now().In(loc)
		}
		return time.Now()
	}
	return c
}

func (c *Client) lang() string {
	if c.cfg.TMDBLang == "" {
		return "en"
	}
	return c.cfg.TMDBLang
}

// get performs a GET with the base params (api_key, language) and decodes JSON.
func (c *Client) get(ctx context.Context, path string, extra url.Values, dst any) error {
	q := url.Values{}
	q.Set("api_key", c.cfg.TMDBKey)
	q.Set("language", c.lang())
	for k, v := range extra {
		q[k] = v
	}
	err := c.base.JSON(ctx, providers.Request{
		Provider: Label,
		URL:      c.BaseURL + path + "?" + q.Encode(),
	}, dst)
	return handleError(err)
}

// handleError mirrors tmdb.handle_error: a 401 carries TMDB's status_message
// as details; everything else is the bare provider error.
func handleError(err error) error {
	var pe *providers.Error
	if err == nil || !errors.As(err, &pe) || pe.Status == 0 {
		return err
	}
	out := &providers.Error{Provider: Label, Status: pe.Status, Err: pe.Err}
	if pe.Status == http.StatusUnauthorized && pe.Err != nil {
		var body struct {
			Message string `json:"status_message"`
		}
		if json.Unmarshal([]byte(pe.Err.Error()), &body) == nil && body.Message != "" {
			out.Details = strings.TrimRight(body.Message, ".")
		}
	}
	return out
}

func notFound(details string) *providers.Error {
	return &providers.Error{Provider: Label, Status: http.StatusNotFound, Details: details}
}

func (c *Client) cacheGet(ctx context.Context, key string) (obj, bool) {
	if c.base.Cache == nil {
		return nil, false
	}
	var out obj
	ok, err := c.base.Cache.Get(ctx, key, &out)
	if err != nil || !ok || out == nil {
		return nil, false
	}
	return out, true
}

func (c *Client) cacheSet(ctx context.Context, key string, v any) {
	if c.base.Cache == nil {
		return
	}
	if err := c.base.Cache.Set(ctx, key, v, cacheTTL); err != nil {
		slog.WarnContext(ctx, "tmdb cache write failed", "key", key, "err", err)
	}
}

// Search ports tmdb.search plus the TMDB routing of services.search: media
// type "tv" or "movie"; "season" and "episode" search TV.
func (c *Client) Search(ctx context.Context, mediaType, query string, page int, showAdultTitles bool) (providers.SearchPage, error) {
	switch mediaType {
	case "tv", "movie":
	case "season", "episode":
		mediaType = "tv"
	default:
		return nil, ErrUnsupported
	}
	key := fmt.Sprintf("search_tmdb_%s_%s_%d_%s", mediaType, query, page, pyBool(showAdultTitles))
	var data providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &data, func() (any, error) {
		params := url.Values{"query": {query}, "page": {strconv.Itoa(page)}}
		if c.cfg.TMDBNSFW && showAdultTitles {
			params.Set("include_adult", "true")
		}
		var resp obj
		if err := c.get(ctx, "/search/"+mediaType, params, &resp); err != nil {
			return nil, err
		}
		results := []providers.Metadata{}
		for _, e := range asList(resp["results"]) {
			m := asObj(e)
			results = append(results, providers.Metadata{
				"media_id":     m["id"],
				"source":       "tmdb",
				"media_type":   mediaType,
				"title":        getTitle(m),
				"image":        GetImageURL(m["poster_path"]),
				"release_year": getReleaseYear(m),
			})
		}
		total, _ := num(resp["total_results"])
		const perPage = 20 // TMDB always returns 20 results per page
		return providers.SearchPage{
			"page":          page,
			"total_results": int(total),
			"total_pages":   int(total)/perPage + 1,
			"results":       results,
		}, nil
	})
	return data, err
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// Find ports find(): looks up media by an external id (e.g. "imdb_id", "tvdb_id")
// and returns TMDB's raw response (movie_results, tv_results, ...).
func (c *Client) Find(ctx context.Context, externalID, externalSource string) (map[string]any, error) {
	key := fmt.Sprintf("find_tmdb_%s_%s", externalID, externalSource)
	var data map[string]any
	err := c.base.Cached(ctx, key, cacheTTL, &data, func() (any, error) {
		var resp obj
		err := c.get(ctx, "/find/"+url.PathEscape(externalID), url.Values{"external_source": {externalSource}}, &resp)
		return resp, err
	})
	return data, err
}

// Movie ports movie().
func (c *Client) Movie(ctx context.Context, mediaID string) (providers.Metadata, error) {
	var data providers.Metadata
	err := c.base.Cached(ctx, "tmdb_movie_"+mediaID, cacheTTL, &data, func() (any, error) {
		return c.fetchMovie(ctx, mediaID)
	})
	return data, err
}

func (c *Client) fetchMovie(ctx context.Context, mediaID string) (any, error) {
	var resp obj
	appends := "recommendations,external_ids,credits,watch/providers"
	if err := c.get(ctx, "/movie/"+mediaID, url.Values{"append_to_response": {appends}}, &resp); err != nil {
		return nil, err
	}

	collection := obj{}
	if bc := asObj(resp["belongs_to_collection"]); bc != nil && truthy(bc["id"]) != "" {
		var cr obj
		if err := c.get(ctx, "/collection/"+idString(bc["id"]), nil, &cr); err != nil {
			slog.WarnContext(ctx, "tmdb: failed to get collection", "err", err)
		} else {
			collection = cr
		}
	}

	// Filter collection items out of recommendations, to avoid duplicates.
	collectionItems := getCollection(collection)
	inCollection := map[string]bool{}
	for _, it := range collectionItems {
		inCollection[idString(asObj(it)["media_id"])] = true
	}
	var recommended []any
	for _, e := range asList(asObj(resp["recommendations"])["results"]) {
		if !inCollection[idString(asObj(e)["id"])] {
			recommended = append(recommended, e)
		}
	}

	cast := asList(asObj(resp["credits"])["cast"])
	filteredCast := []any{}
	for i, e := range cast {
		if i >= 30 {
			break
		}
		m := asObj(e)
		filteredCast = append(filteredCast, obj{
			"id":        m["id"],
			"name":      m["name"],
			"character": m["character"],
			"image":     GetImageURL(m["profile_path"]),
		})
	}

	collectionName := "collection"
	if n, ok := collection["name"].(string); ok {
		collectionName = n
	}
	return obj{
		"media_id":     mediaID,
		"source":       "tmdb",
		"source_url":   "https://www.themoviedb.org/movie/" + mediaID,
		"media_type":   "movie",
		"title":        resp["title"],
		"max_progress": 1,
		"image":        GetImageURL(resp["poster_path"]),
		"synopsis":     getSynopsis(resp["overview"]),
		"genres":       getGenres(resp["genres"]),
		"score":        getScore(resp["vote_average"]),
		"score_count":  resp["vote_count"],
		"details": obj{
			"format":       "Movie",
			"release_date": getStartDate(resp["release_date"]),
			"status":       resp["status"],
			"runtime":      getReadableDuration(resp["runtime"]),
			"studios":      getCompanies(resp["production_companies"]),
			"country":      getCountry(resp["production_countries"]),
			"languages":    getLanguages(resp["spoken_languages"]),
		},
		"cast":             filteredCast,
		"total_cast_count": len(cast),
		"related": obj{
			collectionName:    collectionItems,
			"recommendations": getRelated(recommended, "movie", nil),
		},
		"external_links": getExternalLinks(asObj(resp["external_ids"]), mediaID),
		"providers":      orEmptyObj(asObj(resp["watch/providers"])["results"]),
	}, nil
}

func orEmptyObj(v any) any {
	if v == nil {
		return obj{}
	}
	return v
}

// getCollection formats collection parts like related media (get_collection).
func getCollection(collection obj) []any {
	parts := append([]any(nil), asList(collection["parts"])...)
	dateKey := func(e any) string {
		m := asObj(e)
		if d, ok := m["release_date"].(string); ok && d != "" {
			return d
		}
		// Unknown release date: sort by title after known releases.
		return fmt.Sprintf("9999-99-99-%v", getTitle(m))
	}
	sort.SliceStable(parts, func(i, j int) bool { return dateKey(parts[i]) < dateKey(parts[j]) })
	out := []any{}
	for _, e := range parts {
		m := asObj(e)
		out = append(out, obj{
			"source":     "tmdb",
			"media_type": "movie",
			"image":      GetImageURL(m["poster_path"]),
			"media_id":   m["id"],
			"title":      getTitle(m),
		})
	}
	return out
}

func tvCacheKey(id string) string { return "tmdb_tv_" + id }

func seasonCacheKey(id string, n int) string { return fmt.Sprintf("tmdb_season_%s_%d", id, n) }

// TV ports tv().
func (c *Client) TV(ctx context.Context, mediaID string) (providers.Metadata, error) {
	var data providers.Metadata
	err := c.base.Cached(ctx, tvCacheKey(mediaID), cacheTTL, &data, func() (any, error) {
		var resp obj
		err := c.get(ctx, "/tv/"+mediaID, url.Values{"append_to_response": {"recommendations,external_ids,watch/providers"}}, &resp)
		if err != nil {
			return nil, err
		}
		return processTV(resp), nil
	})
	return data, err
}

func processTV(resp obj) obj {
	numEpisodes := resp["number_of_episodes"]
	next := asObj(resp["next_episode_to_air"])
	last := asObj(resp["last_episode_to_air"])
	var lastSeason, nextSeason any
	if last != nil {
		lastSeason = last["season_number"]
	}
	if next != nil {
		nextSeason = next["season_number"]
	}
	ids := asObj(resp["external_ids"])
	return obj{
		"media_id":     resp["id"],
		"source":       "tmdb",
		"source_url":   "https://www.themoviedb.org/tv/" + idString(resp["id"]),
		"media_type":   "tv",
		"title":        resp["name"],
		"max_progress": numEpisodes,
		"image":        GetImageURL(resp["poster_path"]),
		"synopsis":     getSynopsis(resp["overview"]),
		"genres":       getGenres(resp["genres"]),
		"score":        getScore(resp["vote_average"]),
		"score_count":  resp["vote_count"],
		"details": obj{
			"format":         "TV",
			"first_air_date": getStartDate(resp["first_air_date"]),
			"last_air_date":  resp["last_air_date"],
			"status":         resp["status"],
			"seasons":        resp["number_of_seasons"],
			"episodes":       numEpisodes,
			"runtime":        getRuntimeTV(resp["episode_run_time"]),
			"studios":        getCompanies(resp["production_companies"]),
			"country":        getCountry(resp["production_countries"]),
			"languages":      getLanguages(resp["spoken_languages"]),
		},
		"related": obj{
			"seasons":         getRelated(resp["seasons"], "season", resp),
			"recommendations": getRelated(asObj(resp["recommendations"])["results"], "tv", nil),
		},
		"tvdb_id":             ids["tvdb_id"],
		"external_links":      getExternalLinks(ids, ""),
		"last_episode_season": lastSeason,
		"next_episode_season": nextSeason,
		"providers":           orEmptyObj(asObj(resp["watch/providers"])["results"]),
	}
}

func processSeason(resp obj, providersResp obj) obj {
	episodes := asList(resp["episodes"])
	var runtimes []float64
	totalRuntime := 0.0
	scoreCount := 0.0
	for _, e := range episodes {
		ep := asObj(e)
		if rt, ok := num(ep["runtime"]); ok {
			runtimes = append(runtimes, rt)
			totalRuntime += rt
		}
		vc, _ := num(ep["vote_count"])
		scoreCount += vc
	}
	var avg, total any
	if len(runtimes) > 0 {
		sum := 0.0
		for _, r := range runtimes {
			sum += r
		}
		avg = getReadableDuration(sum / float64(len(runtimes)))
	}
	if totalRuntime != 0 {
		total = getReadableDuration(totalRuntime)
	}
	var maxProgress any = 0
	if len(episodes) > 0 {
		maxProgress = asObj(episodes[len(episodes)-1])["episode_number"]
	}
	return obj{
		"source":        "tmdb",
		"media_type":    "season",
		"season_title":  resp["name"],
		"max_progress":  maxProgress,
		"image":         GetImageURL(resp["poster_path"]),
		"season_number": resp["season_number"],
		"synopsis":      getSynopsis(resp["overview"]),
		"score":         getScore(resp["vote_average"]),
		"score_count":   int(scoreCount),
		"details": obj{
			"first_air_date": getStartDate(resp["air_date"]),
			"last_air_date":  getEndDate(resp),
			"episodes":       len(episodes),
			"runtime":        avg,
			"total_runtime":  total,
		},
		"episodes":  orEmptyList(resp["episodes"]),
		"providers": orEmptyObj(providersResp["results"]),
	}
}

func orEmptyList(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

func enrichSeason(season, tv obj, mediaID string) obj {
	n := idString(season["season_number"])
	season["media_id"] = mediaID
	season["source_url"] = "https://www.themoviedb.org/tv/" + mediaID + "/season/" + n
	season["title"] = tv["title"]
	season["tvdb_id"] = tv["tvdb_id"]
	season["external_links"] = tv["external_links"]
	season["genres"] = tv["genres"]
	if season["synopsis"] == "No synopsis available." {
		season["synopsis"] = tv["synopsis"]
	}
	// Seasons often have no poster of their own; use the show's.
	if season["image"] == imgNone {
		season["image"] = tv["image"]
	}
	return season
}

func (c *Client) cachedSeasons(ctx context.Context, mediaID string, numbers []int) (obj, []int) {
	cached := obj{}
	var uncached []int
	for _, n := range numbers {
		if data, ok := c.cacheGet(ctx, seasonCacheKey(mediaID, n)); ok && len(data) > 0 {
			cached[fmt.Sprintf("season/%d", n)] = data
		} else {
			uncached = append(uncached, n)
		}
	}
	return cached, uncached
}

func (c *Client) fetchAndCacheSeasons(ctx context.Context, mediaID string, numbers []int, tv obj) (obj, obj, error) {
	result := obj{}
	for i := 0; i < len(numbers); i += maxSeasonsPerRequest {
		subset := numbers[i:min(i+maxSeasonsPerRequest, len(numbers))]
		parts := make([]string, 0, len(subset))
		for _, n := range subset {
			parts = append(parts, fmt.Sprintf("season/%d,season/%d/watch/providers", n, n))
		}
		appendTo := "recommendations,external_ids,watch/providers," + strings.Join(parts, ",")
		var resp obj
		if err := c.get(ctx, "/tv/"+mediaID, url.Values{"append_to_response": {appendTo}}, &resp); err != nil {
			return nil, nil, err
		}

		// Cache TV metadata if we haven't fetched it yet.
		if tv == nil {
			tv = normalize(processTV(resp)).(obj)
			c.cacheSet(ctx, tvCacheKey(mediaID), tv)
		}

		for _, n := range subset {
			sk := fmt.Sprintf("season/%d", n)
			sr, ok := resp[sk]
			if !ok {
				return nil, nil, notFound(fmt.Sprintf("Season %d not found for %s with ID %s", n, Label, mediaID))
			}
			season := processSeason(asObj(sr), asObj(resp[sk+"/watch/providers"]))
			season = normalize(enrichSeason(season, tv, mediaID)).(obj)
			c.cacheSet(ctx, seasonCacheKey(mediaID, n), season)
			result[sk] = season
		}
	}
	return result, tv, nil
}

// TVWithSeasons ports tv_with_seasons(): the show's metadata plus a
// "season/<n>" entry for each requested season.
func (c *Client) TVWithSeasons(ctx context.Context, mediaID string, seasonNumbers []int) (providers.Metadata, error) {
	if len(seasonNumbers) == 0 {
		return c.TV(ctx, mediaID)
	}
	tv, _ := c.cacheGet(ctx, tvCacheKey(mediaID))
	cached, uncached := c.cachedSeasons(ctx, mediaID, seasonNumbers)

	if tv == nil && len(uncached) == 0 {
		var err error
		if tv, err = c.TV(ctx, mediaID); err != nil {
			return nil, err
		}
	}
	if len(uncached) > 0 {
		fetched, fetchedTV, err := c.fetchAndCacheSeasons(ctx, mediaID, uncached, tv)
		if err != nil {
			return nil, err
		}
		if tv == nil {
			tv = fetchedTV
		}
		for k, v := range fetched {
			cached[k] = v
		}
	}
	out := make(providers.Metadata, len(tv)+len(cached))
	for k, v := range tv {
		out[k] = v
	}
	for k, v := range cached {
		out[k] = v
	}
	return out, nil
}

// Season returns the metadata for one season, as get_media_metadata does for
// media_type "season": tv_with_seasons(...)["season/<n>"].
func (c *Client) Season(ctx context.Context, mediaID string, seasonNumber int) (providers.Metadata, error) {
	all, err := c.TVWithSeasons(ctx, mediaID, []int{seasonNumber})
	if err != nil {
		return nil, err
	}
	season, ok := all[fmt.Sprintf("season/%d", seasonNumber)].(map[string]any)
	if !ok {
		return nil, notFound(fmt.Sprintf("Season %d not found for %s with ID %s", seasonNumber, Label, mediaID))
	}
	return season, nil
}

// Seasons returns the metadata for several seasons keyed "season/<n>".
func (c *Client) Seasons(ctx context.Context, mediaID string, seasonNumbers []int) (map[string]providers.Metadata, error) {
	all, err := c.TVWithSeasons(ctx, mediaID, seasonNumbers)
	if err != nil {
		return nil, err
	}
	out := map[string]providers.Metadata{}
	for _, n := range seasonNumbers {
		k := fmt.Sprintf("season/%d", n)
		if s, ok := all[k].(map[string]any); ok {
			out[k] = s
		}
	}
	return out, nil
}

// Episode ports episode(): titles and still image for one episode. A missing
// episode is a 404 provider error.
func (c *Client) Episode(ctx context.Context, mediaID string, seasonNumber, episodeNumber int) (providers.Metadata, error) {
	season, err := c.Season(ctx, mediaID, seasonNumber)
	if err != nil {
		return nil, err
	}
	for _, e := range asList(season["episodes"]) {
		ep := asObj(e)
		if n, ok := num(ep["episode_number"]); ok && int(n) == episodeNumber {
			return providers.Metadata{
				"title":         season["title"],
				"season_title":  season["season_title"],
				"episode_title": ep["name"],
				"image":         GetImageURL(ep["still_path"]),
			}, nil
		}
	}
	return nil, notFound(fmt.Sprintf("Episode %d not found in season %d for %s with ID %s", episodeNumber, seasonNumber, Label, mediaID))
}

// ProcessEpisodes ports process_episodes(): per-episode rows for a season,
// with history[episodeNumber] attached (tracked episodes from the DB).
func ProcessEpisodes(season providers.Metadata, history map[int][]any) []providers.Metadata {
	out := []providers.Metadata{}
	for _, e := range asList(season["episodes"]) {
		ep := asObj(e)
		n, _ := num(ep["episode_number"])
		h := history[int(n)]
		if h == nil {
			h = []any{}
		}
		out = append(out, providers.Metadata{
			"media_id":        season["media_id"],
			"media_type":      "episode",
			"source":          "tmdb",
			"season_number":   season["season_number"],
			"episode_number":  ep["episode_number"],
			"air_date":        ep["air_date"], // null when unknown
			"image":           GetImageURL(ep["still_path"]),
			"title":           ep["name"],
			"overview":        ep["overview"],
			"history":         h,
			"runtime":         getReadableDuration(ep["runtime"]),
			"runtime_minutes": ep["runtime"],
		})
	}
	return out
}

// FindNextEpisode ports find_next_episode(): the episode number after
// episodeNumber, or ok=false when it is unknown or last.
func FindNextEpisode(episodeNumber int, episodes []providers.Metadata) (int, bool) {
	for i, ep := range episodes {
		if providers.Int(ep, "episode_number") == episodeNumber {
			if i+1 >= len(episodes) {
				return 0, false
			}
			return providers.Int(episodes[i+1], "episode_number"), true
		}
	}
	return 0, false
}

// FilterProviders ports filter_providers(): flatrate+free watch providers for
// a region, de-duplicated, with image URLs, sorted by display_priority. A
// blank region returns nil (feature disabled).
func FilterProviders(all map[string]any, region string) []map[string]any {
	if region == "" {
		return nil
	}
	if len(all) == 0 {
		return []map[string]any{}
	}
	reg := asObj(all[region])
	byID := map[string]map[string]any{}
	var order []string
	for _, kind := range []string{"flatrate", "free"} {
		for _, e := range asList(reg[kind]) {
			p := asObj(e)
			id := fmt.Sprint(p["provider_id"])
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			byID[id] = p
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		p := byID[id]
		p["image"] = GetImageURL(p["logo_path"])
		out = append(out, p)
	}
	prio := func(p map[string]any) float64 {
		if n, ok := num(p["display_priority"]); ok {
			return n
		}
		return 999
	}
	sort.SliceStable(out, func(i, j int) bool { return prio(out[i]) < prio(out[j]) })
	return out
}

// Region is one watch-provider region.
type Region struct{ Code, Name string }

// WatchProviderRegions ports watch_provider_regions(), without the leading
// "Disabled" choice the Django form adds.
func (c *Client) WatchProviderRegions(ctx context.Context) ([]Region, error) {
	var data []Region
	err := c.base.Cached(ctx, "tmdb_watch_provider_region_list", cacheTTL, &data, func() (any, error) {
		var resp obj
		if err := c.get(ctx, "/watch/providers/regions", nil, &resp); err != nil {
			return nil, err
		}
		regions := asList(resp["results"])
		sort.SliceStable(regions, func(i, j int) bool {
			a, _ := asObj(regions[i])["english_name"].(string)
			b, _ := asObj(regions[j])["english_name"].(string)
			return a < b
		})
		out := []Region{}
		for _, e := range regions {
			r := asObj(e)
			code, _ := r["iso_3166_1"].(string)
			name, _ := r["english_name"].(string)
			if code == "" {
				continue
			}
			if name == "" {
				name = code
			}
			out = append(out, Region{code, name})
		}
		return out, nil
	})
	return data, err
}

// ChangedIDs ports get_changed_ids(): ids changed over the last 3 days.
func (c *Client) ChangedIDs(ctx context.Context, mediaType string) (map[string]struct{}, error) {
	end := c.Now()
	start := end.AddDate(0, 0, -3)
	ids := map[string]struct{}{}
	for page := 1; ; page++ {
		var resp obj
		err := c.get(ctx, "/"+mediaType+"/changes", url.Values{
			"start_date": {start.Format("2006-01-02")},
			"end_date":   {end.Format("2006-01-02")},
			"page":       {strconv.Itoa(page)},
		}, &resp)
		if err != nil {
			return nil, err
		}
		for _, e := range asList(resp["results"]) {
			ids[idString(asObj(e)["id"])] = struct{}{}
		}
		total := 1.0
		if n, ok := num(resp["total_pages"]); ok {
			total = n
		}
		if float64(page) >= total {
			return ids, nil
		}
	}
}

// TVChanges ports tv_changes().
func (c *Client) TVChanges(ctx context.Context) (map[string]struct{}, error) {
	return c.ChangedIDs(ctx, "tv")
}

// MovieChanges ports movie_changes().
func (c *Client) MovieChanges(ctx context.Context) (map[string]struct{}, error) {
	return c.ChangedIDs(ctx, "movie")
}

// ErrUnsupported is returned for media types TMDB does not serve.
var ErrUnsupported = errors.New("tmdb: unsupported media type")

// GetMediaMetadata is the TMDB part of services.get_media_metadata. mediaType
// is "tv", "tv_with_seasons", "season", "episode" or "movie".
func (c *Client) GetMediaMetadata(ctx context.Context, mediaType, mediaID string, seasonNumbers []int, episodeNumber int) (providers.Metadata, error) {
	needSeason := func() error {
		if len(seasonNumbers) == 0 {
			return fmt.Errorf("tmdb: %s needs a season number", mediaType)
		}
		return nil
	}
	switch mediaType {
	case "tv":
		return c.TV(ctx, mediaID)
	case "tv_with_seasons":
		return c.TVWithSeasons(ctx, mediaID, seasonNumbers)
	case "season":
		if err := needSeason(); err != nil {
			return nil, err
		}
		return c.Season(ctx, mediaID, seasonNumbers[0])
	case "episode":
		if err := needSeason(); err != nil {
			return nil, err
		}
		return c.Episode(ctx, mediaID, seasonNumbers[0], episodeNumber)
	case "movie":
		return c.Movie(ctx, mediaID)
	}
	return nil, ErrUnsupported
}

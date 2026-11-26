// Package igdb ports src/app/providers/igdb.py: game search and metadata from
// the Internet Game Database, authenticated with a Twitch client-credentials
// token that is cached until shortly before it expires.
package igdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// Label is the provider name used in error messages.
	Label = "Internet Game Database"
	// Source is the value of the "source" key.
	Source = "igdb"
	// MediaType is the value of the "media_type" key.
	MediaType = "game"

	// ImgNone is the placeholder image (settings.IMG_NONE).
	ImgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

	perPage  = 24 // settings.PER_PAGE
	cacheTTL = 24 * time.Hour

	defaultBaseURL  = "https://api.igdb.com/v4"
	defaultTokenURL = "https://id.twitch.tv/oauth2/token"
)

// SourceSteam is the IGDB external game source id for Steam
// (ExternalGameSource.STEAM in the Python).
const SourceSteam = 1

// Client talks to IGDB.
type Client struct {
	base *providers.Client
	cfg  config.Config
	loc  *time.Location

	// BaseURL and TokenURL can be overridden (tests).
	BaseURL  string
	TokenURL string

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// New returns an IGDB client.
func New(base *providers.Client, cfg config.Config) *Client {
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil || cfg.TimeZone == "" {
		loc = time.UTC
	}
	return &Client{base: base, cfg: cfg, loc: loc, BaseURL: defaultBaseURL, TokenURL: defaultTokenURL}
}

// accessToken returns the cached Twitch token, fetching one when needed.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp) {
		return c.token, nil
	}
	var resp struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	err := c.base.JSON(ctx, providers.Request{
		Provider: Label,
		Method:   http.MethodPost,
		URL:      c.TokenURL,
		Body: map[string]string{
			"client_id":     c.cfg.IGDBID,
			"client_secret": c.cfg.IGDBSecret,
			"grant_type":    "client_credentials",
		},
	}, &resp)
	if err != nil {
		return "", handleError(err)
	}
	if resp.AccessToken == "" {
		return "", &providers.Error{Provider: Label, Status: http.StatusOK, Details: "missing access token"}
	}
	c.token = resp.AccessToken
	// 1 min buffer to avoid using an expired token.
	c.tokenExp = time.Now().Add(time.Duration(resp.ExpiresIn-60) * time.Second)
	return c.token, nil
}

func (c *Client) clearToken() {
	c.mu.Lock()
	c.token, c.tokenExp = "", time.Time{}
	c.mu.Unlock()
}

// handleError mirrors handle_error for non-401 failures: 400/403 carry the
// API's message (capitalised) as details.
func handleError(err error) error {
	var pe *providers.Error
	if !errors.As(err, &pe) || pe.Status == 0 || pe.Details != "" {
		return err
	}
	if pe.Status == http.StatusBadRequest || pe.Status == http.StatusForbidden {
		var body struct {
			Message string `json:"message"`
		}
		if pe.Err != nil && json.Unmarshal([]byte(pe.Err.Error()), &body) == nil && body.Message != "" {
			// Python's str.capitalize: first letter upper, rest lower.
			m := strings.ToLower(body.Message)
			return &providers.Error{Provider: Label, Status: pe.Status, Details: strings.ToUpper(m[:1]) + m[1:], Err: pe.Err}
		}
	}
	return err
}

// post sends an Apicalypse query, refreshing the token once on a 401.
func (c *Client) post(ctx context.Context, endpoint, query string, dst any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		err = c.base.JSON(ctx, providers.Request{
			Provider: Label,
			Method:   http.MethodPost,
			URL:      c.BaseURL + endpoint,
			Header:   http.Header{"Client-Id": {c.cfg.IGDBID}, "Authorization": {"Bearer " + token}},
			Body:     query,
		}, dst)
		var pe *providers.Error
		if errors.As(err, &pe) && pe.Status == http.StatusUnauthorized && attempt == 0 {
			c.clearToken() // invalid access token, expired or revoked
			continue
		}
		if err != nil {
			return handleError(err)
		}
		return nil
	}
}

// ExternalGame finds the IGDB game id for an external id (igdb.external_game).
// It returns 0 when there is no match.
func (c *Client) ExternalGame(ctx context.Context, externalID string, source int) (int, error) {
	var id int
	key := fmt.Sprintf("external_game_igdb_%d_%s", source, externalID)
	err := c.base.Cached(ctx, key, cacheTTL, &id, func() (any, error) {
		query := fmt.Sprintf(`fields game; where uid = "%s" & external_game_source = %d;`, escape(externalID), source)
		var resp []map[string]any
		if err := c.post(ctx, "/external_games", query, &resp); err != nil {
			return nil, err
		}
		if len(resp) > 0 {
			return toInt(resp[0]["game"]), nil
		}
		return 0, nil
	})
	return id, err
}

// Search ports igdb.search. showAdult is show_adult_titles.
func (c *Client) Search(ctx context.Context, query string, page int, showAdult bool) (providers.SearchPage, error) {
	key := fmt.Sprintf("search_igdb_game_%s_%d_%t", query, page, showAdult)
	var out providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		cond := fmt.Sprintf(`where name ~ *"%s"* & game_type = (0,1,2,3,4,5,6,7,8,9,10)`, escape(query))
		if !c.cfg.IGDBNSFW || !showAdult {
			cond += " & themes != (42)"
		}
		offset := (page - 1) * perPage
		mq := `query games "SearchResults" {` +
			`fields name,cover.image_id;` +
			`sort total_rating_count desc;` +
			fmt.Sprintf("limit %d;", perPage) +
			fmt.Sprintf("offset %d;", offset) +
			cond + ";" +
			`};` +
			`query games/count "TotalCount" {` +
			cond + ";" +
			`};`
		var resp []map[string]any
		if err := c.post(ctx, "/multiquery", mq, &resp); err != nil {
			return nil, err
		}
		var found []any
		total := 0
		for _, item := range resp {
			switch item["name"] {
			case "SearchResults":
				if found == nil {
					found, _ = item["result"].([]any)
				}
			case "TotalCount":
				total = toInt(item["count"])
			}
		}
		results := make([]providers.Metadata, 0, len(found))
		for _, e := range found {
			m, _ := e.(map[string]any)
			results = append(results, providers.Metadata{
				"media_id":   toInt(m["id"]),
				"source":     Source,
				"media_type": MediaType,
				"title":      m["name"],
				"image":      imageURL(m),
			})
		}
		return providers.SearchPage{
			"page":          page,
			"total_results": total,
			"total_pages":   total/perPage + 1,
			"results":       results,
		}, nil
	})
	return out, err
}

const gameFields = "fields name,cover.image_id,artworks.image_id,artworks.width,artworks.height," +
	"screenshots.image_id,screenshots.width,screenshots.height," +
	"url,summary,game_type,first_release_date,total_rating,total_rating_count," +
	"genres.name,themes.name,platforms.name,involved_companies.company.name," +
	"parent_game.name,parent_game.cover.image_id," +
	"remasters.name,remasters.cover.image_id," +
	"remakes.name,remakes.cover.image_id," +
	"expansions.name,expansions.cover.image_id," +
	"standalone_expansions.name,standalone_expansions.cover.image_id," +
	"expanded_games.name,expanded_games.cover.image_id," +
	"similar_games.name,similar_games.cover.image_id," +
	"dlcs.name,dlcs.cover.image_id;"

// Game ports igdb.game: metadata for one game. A missing game is a provider
// 404 ("No result found with ID ...").
func (c *Client) Game(ctx context.Context, mediaID string) (providers.Metadata, error) {
	var out providers.Metadata
	key := "igdb_game3_" + mediaID // 3: backdrop from artworks/screenshots
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		id, err := strconv.Atoi(strings.TrimSpace(mediaID))
		if err != nil {
			return nil, notFound(mediaID)
		}
		mq := `query games "GameData" {` + gameFields +
			fmt.Sprintf("where id = %d;", id) + `};` +
			`query game_time_to_beats "TTBData" {` +
			`fields hastily,normally,completely;` +
			fmt.Sprintf("where game_id = %d;", id) + `};`
		var resp []map[string]any
		if err := c.post(ctx, "/multiquery", mq, &resp); err != nil {
			return nil, err
		}
		results := map[string][]any{}
		for _, item := range resp {
			name, _ := item["name"].(string)
			r, _ := item["result"].([]any)
			results[name] = r
		}
		if len(results["GameData"]) == 0 {
			return nil, notFound(mediaID)
		}
		g, _ := results["GameData"][0].(map[string]any)

		var timeToBeat any
		if ttb := results["TTBData"]; len(ttb) > 0 {
			entry, _ := ttb[0].(map[string]any)
			data := map[string]any{}
			for k, v := range entry {
				if k != "id" && v != nil {
					data[k] = v
				}
			}
			if len(data) > 0 {
				timeToBeat = data
			}
		}

		var synopsis any = "No synopsis available."
		if s, ok := g["summary"]; ok {
			synopsis = s
		}
		var scoreCount any
		if v, ok := g["total_rating_count"]; ok {
			scoreCount = v
		}
		return providers.Metadata{
			"media_id":     toInt(g["id"]),
			"source":       Source,
			"source_url":   g["url"],
			"media_type":   MediaType,
			"title":        g["name"],
			"max_progress": nil,
			"image":        imageURL(g),
			"backdrop":     artworkURL(g),
			"synopsis":     synopsis,
			"genres":       nameList(g, "genres"),
			"score":        score(g),
			"score_count":  scoreCount,
			"details": providers.Metadata{
				"format":       gameType(g["game_type"]),
				"release_date": c.startDate(g),
				"themes":       nameList(g, "themes"),
				"platforms":    nameList(g, "platforms"),
				"companies":    companies(g),
			},
			"related": providers.Metadata{
				"parent_game":           parent(g["parent_game"]),
				"remasters":             related(g["remasters"]),
				"remakes":               related(g["remakes"]),
				"expansions":            related(g["expansions"]),
				"dlcs":                  related(g["dlcs"]),
				"standalone_expansions": related(g["standalone_expansions"]),
				"expanded_games":        related(g["expanded_games"]),
				"recommendations":       related(g["similar_games"]),
			},
			"time_to_beat": timeToBeat,
		}, nil
	})
	return out, err
}

func notFound(id string) error {
	return &providers.Error{Provider: Label, Status: http.StatusNotFound, Details: "No result found with ID " + id}
}

// escape keeps a quote in the user's query from breaking the Apicalypse string.
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func imageURL(m map[string]any) string {
	if cover, ok := m["cover"].(map[string]any); ok {
		if id, ok := cover["image_id"].(string); ok {
			return "https://images.igdb.com/igdb/image/upload/t_original/" + id + ".jpg"
		}
	}
	return ImgNone
}

// artworkURL is the game's banner for the title page: the largest wide key
// art, else the first wide screenshot. Small or square art (logos, icons) is
// skipped; nil when nothing fits.
func artworkURL(m map[string]any) any {
	pick := func(list []any) string {
		best, bestW := "", 0
		for _, a := range list {
			art, _ := a.(map[string]any)
			id, _ := art["image_id"].(string)
			w, h := toInt(art["width"]), toInt(art["height"])
			if id == "" || w < 1280 || h == 0 || float64(w)/float64(h) < 1.5 {
				continue
			}
			if w > bestW {
				best, bestW = id, w
			}
		}
		return best
	}
	arts, _ := m["artworks"].([]any)
	id := pick(arts)
	if id == "" {
		shots, _ := m["screenshots"].([]any)
		id = pick(shots)
	}
	if id == "" {
		return nil
	}
	return "https://images.igdb.com/igdb/image/upload/t_1080p/" + id + ".jpg"
}

var gameTypes = map[int]string{
	0: "Main game", 1: "DLC", 2: "Expansion", 3: "Bundle", 4: "Standalone expansion",
	5: "Mod", 6: "Episode", 7: "Season", 8: "Remake", 9: "Remaster",
	10: "Expanded game", 11: "Port", 12: "Fork", 13: "Pack", 14: "Update",
}

func gameType(v any) any {
	if f, ok := v.(float64); ok {
		if s, ok := gameTypes[int(f)]; ok {
			return s
		}
	}
	return nil
}

func (c *Client) startDate(g map[string]any) any {
	ts, ok := g["first_release_date"].(float64)
	if !ok {
		return nil
	}
	return time.Unix(int64(ts), 0).In(c.loc).Format("2006-01-02")
}

func nameList(g map[string]any, field string) any {
	items, ok := g[field].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		out = append(out, m["name"])
	}
	return out
}

func companies(g map[string]any) any {
	items, ok := g["involved_companies"].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		co, _ := m["company"].(map[string]any)
		out = append(out, co["name"])
	}
	return out
}

func score(g map[string]any) any {
	f, ok := g["total_rating"].(float64)
	if !ok {
		return nil
	}
	return round1(f / 10)
}

// round1 rounds like Python's round(x, 1).
func round1(f float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 1, 64), 64)
	return r
}

func entry(g map[string]any) providers.Metadata {
	return providers.Metadata{
		"source":     Source,
		"media_id":   toInt(g["id"]),
		"media_type": MediaType,
		"title":      g["name"],
		"image":      imageURL(g),
	}
}

func parent(v any) []any {
	if g, ok := v.(map[string]any); ok && len(g) > 0 {
		return []any{entry(g)}
	}
	return []any{}
}

func related(v any) []any {
	items, _ := v.([]any)
	out := make([]any, 0, len(items))
	for _, it := range items {
		g, _ := it.(map[string]any)
		out = append(out, entry(g))
	}
	return out
}

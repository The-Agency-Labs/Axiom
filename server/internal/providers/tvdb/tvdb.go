// Package tvdb ports src/app/providers/tvdb.py: TVDB login, episode lookup
// and series -> TMDB id resolution. A TVDB 404 on an episode is a miss
// (nil, nil), not an error, so callers fall back to other id sources.
package tvdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

// Label is the provider name in error messages. Django's Sources enum has no
// TVDB entry, so ProviderAPIError falls back to "TVDB".title().
const Label = "Tvdb"

const (
	accessTokenTTL        = 28 * 24 * time.Hour // tokens are valid for 1 month
	cacheTTL              = 24 * time.Hour      // Django's default CACHE_TIMEOUT
	tmdbRemoteIDType      = 12
	tmdbRemoteIDSource    = "TheMovieDB.com"
	defaultBaseURL        = "https://api4.thetvdb.com/v4"
	episodeCachePrefix    = "tvdb_episode_"
	seriesTMDBCachePrefix = "tvdb_series_tmdb_id_"
	tokenCacheKey         = "tvdb_access_token"
)

// Client talks to TVDB through the shared provider client.
type Client struct {
	base *providers.Client
	cfg  config.Config
	// BaseURL is the API root; overridable for tests.
	BaseURL string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// New returns a TVDB client.
func New(base *providers.Client, cfg config.Config) *Client {
	return &Client{base: base, cfg: cfg, BaseURL: defaultBaseURL}
}

// handleError mirrors handle_error: a 401 invalidates the token and asks the
// caller to retry (retry=true, nil error); other errors are returned.
func (c *Client) handleError(ctx context.Context, err error) (retry bool, out error) {
	var pe *providers.Error
	if errors.As(err, &pe) && pe.Status == http.StatusUnauthorized {
		slog.WarnContext(ctx, "TVDB access token is invalid or expired, refreshing")
		c.mu.Lock()
		c.token, c.expires = "", time.Time{}
		c.mu.Unlock()
		c.cacheSetTTL(ctx, tokenCacheKey, "", time.Second) // forget the cached token too
		return true, nil
	}
	if errors.As(err, &pe) && pe.Status != 0 {
		return false, &providers.Error{Provider: Label, Status: pe.Status, Err: pe.Err}
	}
	return false, err
}

// AccessToken ports get_access_token(): "" (nil error) when TVDB_API is not
// configured or login returned no token. The token is kept in memory for 29 days.
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	if c.cfg.TVDBKey == "" {
		slog.DebugContext(ctx, "Skipping TVDB lookup because TVDB_API is not configured")
		return "", nil
	}
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.expires) {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()
	// A token cached by an earlier process (the CLI, a restart) saves a login.
	var stored string
	if c.cacheGet(ctx, tokenCacheKey, &stored) && stored != "" {
		c.mu.Lock()
		c.token, c.expires = stored, time.Now().Add(time.Hour)
		c.mu.Unlock()
		return stored, nil
	}

	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	err := c.base.JSON(ctx, providers.Request{
		Provider: Label,
		Method:   http.MethodPost,
		URL:      c.BaseURL + "/login",
		Body:     map[string]string{"apikey": c.cfg.TVDBKey},
	}, &resp)
	if err != nil {
		_, err = c.handleError(ctx, err)
		if err != nil {
			return "", err
		}
		return "", nil // login itself answered 401: no token
	}
	if resp.Data.Token == "" {
		return "", nil
	}
	c.mu.Lock()
	c.token, c.expires = resp.Data.Token, time.Now().Add(accessTokenTTL)
	c.mu.Unlock()
	c.cacheSetTTL(ctx, tokenCacheKey, resp.Data.Token, accessTokenTTL)
	return resp.Data.Token, nil
}

// authedGet GETs path with the bearer token, refreshing it once on a 401.
func (c *Client) authedGet(ctx context.Context, token, path string, dst any) error {
	do := func(tok string) error {
		return c.base.JSON(ctx, providers.Request{
			Provider: Label,
			URL:      c.BaseURL + path,
			Header:   http.Header{"Authorization": {"Bearer " + tok}},
		}, dst)
	}
	err := do(token)
	if err == nil {
		return nil
	}
	var pe *providers.Error
	if errors.As(err, &pe) && pe.Status == http.StatusNotFound {
		return err
	}
	retry, herr := c.handleError(ctx, err)
	if herr != nil {
		return herr
	}
	if !retry {
		return err
	}
	fresh, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	if err := do(fresh); err != nil {
		_, herr := c.handleError(ctx, err)
		if herr == nil {
			herr = err
		}
		return herr
	}
	return nil
}

func (c *Client) cacheGet(ctx context.Context, key string, dst any) bool {
	if c.base.Cache == nil {
		return false
	}
	ok, err := c.base.Cache.Get(ctx, key, dst)
	return err == nil && ok
}

func (c *Client) cacheSet(ctx context.Context, key string, v any) {
	c.cacheSetTTL(ctx, key, v, cacheTTL)
}

func (c *Client) cacheSetTTL(ctx context.Context, key string, v any, ttl time.Duration) {
	if c.base.Cache == nil {
		return
	}
	if err := c.base.Cache.Set(ctx, key, v, ttl); err != nil {
		slog.WarnContext(ctx, "tvdb cache write failed", "key", key, "err", err)
	}
}

// Episode ports episode(): base metadata (episode_id, series_id,
// season_number, episode_number) for a TVDB episode id. Returns (nil, nil)
// when TVDB is not configured, the episode does not exist (404: deleted or
// renumbered) or its metadata is incomplete.
func (c *Client) Episode(ctx context.Context, episodeID string) (providers.Metadata, error) {
	key := episodeCachePrefix + episodeID
	var cached providers.Metadata
	if c.cacheGet(ctx, key, &cached) && cached != nil {
		return cached, nil
	}

	token, err := c.AccessToken(ctx)
	if err != nil || token == "" {
		return nil, err
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := c.authedGet(ctx, token, "/episodes/"+episodeID, &resp); err != nil {
		if providers.NotFound(err) {
			// Deleted or renumbered on TVDB; let callers try the other IDs.
			slog.InfoContext(ctx, "TVDB has no episode", "episode_id", episodeID)
			return nil, nil
		}
		return nil, err
	}

	data := providers.Metadata{
		"episode_id":     resp.Data["id"],
		"series_id":      resp.Data["seriesId"],
		"season_number":  resp.Data["seasonNumber"],
		"episode_number": resp.Data["number"],
	}
	for _, k := range []string{"series_id", "season_number", "episode_number"} {
		if data[k] == nil {
			slog.DebugContext(ctx, "TVDB episode metadata incomplete", "episode_id", episodeID)
			return nil, nil
		}
	}
	c.cacheSet(ctx, key, data)
	return data, nil
}

// SeriesTMDBID ports series_tmdb_id(): the TMDB id (as a string) from a TVDB
// series' remote ids, or "" when TVDB is not configured or has none.
func (c *Client) SeriesTMDBID(ctx context.Context, seriesID string) (string, error) {
	key := seriesTMDBCachePrefix + seriesID
	var cached string
	if c.cacheGet(ctx, key, &cached) && cached != "" {
		return cached, nil
	}

	token, err := c.AccessToken(ctx)
	if err != nil || token == "" {
		return "", err
	}

	var resp struct {
		Data struct {
			RemoteIDs []map[string]any `json:"remoteIds"`
		} `json:"data"`
	}
	if err := c.authedGet(ctx, token, "/series/"+seriesID+"/extended", &resp); err != nil {
		return "", err
	}

	for _, r := range resp.Data.RemoteIDs {
		typ, _ := r["type"].(float64)
		src, _ := r["sourceName"].(string)
		if int(typ) != tmdbRemoteIDType && src != tmdbRemoteIDSource {
			continue
		}
		id := idString(r["id"])
		if id == "" {
			// Python's next() takes the first match even when its id is falsy.
			break
		}
		c.cacheSet(ctx, key, id)
		return id, nil
	}
	slog.DebugContext(ctx, "TVDB series metadata has no TMDB ID", "series_id", seriesID)
	return "", nil
}

func idString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == 0 {
			return ""
		}
		return strconv.FormatInt(int64(x), 10)
	}
	return fmt.Sprint(v)
}

package tmdb

import (
	"context"
	"fmt"
	"strconv"
)

// EpisodeTVDBID returns the TVDB episode id TMDB records for one episode of a
// show ("" when it has none). It is how the show conversion links a TMDB
// episode to its TVDB twin exactly; nothing else reads shows from TMDB.
func (c *Client) EpisodeTVDBID(ctx context.Context, showID string, season, episode int) (string, error) {
	key := fmt.Sprintf("tmdb_episode_tvdb_%s_%d_%d", showID, season, episode)
	var data obj
	err := c.base.Cached(ctx, key, cacheTTL, &data, func() (any, error) {
		var resp obj
		err := c.get(ctx, fmt.Sprintf("/tv/%s/season/%d/episode/%d/external_ids", showID, season, episode), nil, &resp)
		return resp, err
	})
	if err != nil {
		return "", err
	}
	n, ok := num(data["tvdb_id"])
	if !ok || n == 0 {
		return "", nil
	}
	return strconv.FormatInt(int64(n), 10), nil
}

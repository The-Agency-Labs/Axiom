package anilist

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
)

const episodesQuery = `
query ($id: Int, $idMal: Int) {
  Media(id: $id, idMal: $idMal, type: ANIME) {
    id
    idMal
    episodes
    streamingEpisodes { title thumbnail site }
  }
}
`

// StreamingEpisode is one entry of AniList's streamingEpisodes (Crunchyroll etc.).
type StreamingEpisode struct {
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	Site      string `json:"site"`
}

// EpisodeMedia is what AniList knows about an anime's episodes. The zero
// value means AniList had nothing.
type EpisodeMedia struct {
	ID                int                `json:"id"`
	IDMal             int                `json:"idMal"`
	Episodes          int                `json:"episodes"` // 0 when unknown
	StreamingEpisodes []StreamingEpisode `json:"streamingEpisodes"`
}

// EpisodeInfo is one numbered episode taken from the streaming links.
// Title and Image are empty when unknown (null in the Python).
type EpisodeInfo struct {
	Title string
	Image string
}

var streamingTitle = regexp.MustCompile(`(?i)^\s*(?:Episode|Ep\.?)\s*(\d+)\s*[-:–]?\s*(.*)$`)

// EpisodeMedia fetches AniList's episode data for a MAL or AniList anime id
// (source is "anilist" or "mal"), cached for a day. Like the Python it
// never fails: when AniList is unavailable it logs and returns the zero value.
func (c *Client) EpisodeMedia(ctx context.Context, source, mediaID string) EpisodeMedia {
	var out EpisodeMedia
	key := fmt.Sprintf("catalog:anilist_episodes:%s:%s", source, mediaID)
	err := c.base.Cached(ctx, key, cacheTTL, &out, func() (any, error) {
		id, err := strconv.Atoi(strings.TrimSpace(mediaID))
		if err != nil {
			return nil, err
		}
		variables := map[string]any{"idMal": id}
		if source == Source {
			variables = map[string]any{"id": id}
		}
		data, err := c.GraphQL(ctx, episodesQuery, variables)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Media *struct {
				ID                int                `json:"id"`
				IDMal             *int               `json:"idMal"`
				Episodes          *int               `json:"episodes"`
				StreamingEpisodes []StreamingEpisode `json:"streamingEpisodes"`
			} `json:"Media"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		m := EpisodeMedia{}
		if resp.Media != nil {
			m.ID = resp.Media.ID
			if resp.Media.IDMal != nil {
				m.IDMal = *resp.Media.IDMal
			}
			if resp.Media.Episodes != nil {
				m.Episodes = *resp.Media.Episodes
			}
			m.StreamingEpisodes = resp.Media.StreamingEpisodes
		}
		return m, nil
	})
	if err != nil {
		slog.WarnContext(ctx, "AniList episodes unavailable", "source", source, "id", mediaID, "err", err)
		return EpisodeMedia{}
	}
	return out
}

// Streaming maps episode number to title and thumbnail from the streaming
// links. Titles like "Episode 3 - Name" give the number; others take their
// position. Several sites can list the same episode; the first wins.
func (m EpisodeMedia) Streaming() map[int]EpisodeInfo {
	episodes := map[int]EpisodeInfo{}
	for i, ep := range m.StreamingEpisodes {
		number, title := i+1, ep.Title
		if match := streamingTitle.FindStringSubmatch(ep.Title); match != nil {
			number, _ = strconv.Atoi(match[1])
			title = strings.TrimSpace(match[2])
		}
		if _, ok := episodes[number]; !ok {
			episodes[number] = EpisodeInfo{Title: title, Image: ep.Thumbnail}
		}
	}
	return episodes
}

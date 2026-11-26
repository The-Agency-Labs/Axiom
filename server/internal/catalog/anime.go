package catalog

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"

	"github.com/the-agency-labs/axiom/server/internal/catalog/mappings"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

// ErrNotCountBased means AnimeEpisodes was asked about an anime that has TMDB
// seasons; its episodes are in item_episodes (SyncSeasons).
var ErrNotCountBased = errors.New("catalog: anime has TMDB episodes")

// AnimeEpisode is one numbered episode of a count-based anime. Empty strings
// and nil pointers are the Python's None.
type AnimeEpisode struct {
	Number   int
	Title    string
	Overview string
	Image    string // "" when unknown
	AirDate  string // "" when unknown
	Runtime  *int
	Score    *float64
	Kind     string
}

// TMDBLink says which TMDB season an anime's episodes were taken from.
type TMDBLink struct {
	TMDBID        int64
	SeasonNumber  int64
	EpisodeOffset int64
}

// AnimeInfo names the providers that contributed to AnimeEpisodes.
type AnimeInfo struct {
	Providers []string
	TMDB      *TMDBLink
	AniListID int64
}

// AnimeEpisodes ports catalog.anime.anime_episodes for a count-based anime
// (source mal or anilist), merged per episode number from:
//
//  1. TMDB, through the anime mapping (show, season and episode offset):
//     titles, synopses, stills, air dates, runtimes and ratings, renumbered
//     from 1;
//  2. AniList streaming episodes (Crunchyroll etc.): titles and thumbnails;
//  3. plain numbered episodes up to the known episode count.
//
// Anime with TMDB seasons use SyncSeasons instead (ErrNotCountBased).
func (c *Catalog) AnimeEpisodes(ctx context.Context, item Item) ([]AnimeEpisode, AnimeInfo, error) {
	if item.Source != SourceMAL && item.Source != SourceAniList {
		return nil, AnimeInfo{}, ErrNotCountBased
	}
	var mapping *mappings.Mapping
	if c.Reg.Mappings != nil {
		m, err := c.Reg.Mappings.Lookup(ctx, item.Source, item.ExternalID)
		if err != nil {
			return nil, AnimeInfo{}, err
		}
		mapping = m
	}
	count := int(item.Total.Int64)
	tmdb, link := c.tmdbEpisodes(ctx, mapping, count)
	var media aniMedia
	if c.Reg.P.AniList != nil {
		em := c.Reg.P.AniList.EpisodeMedia(ctx, item.Source, item.ExternalID)
		media = aniMedia{id: em.ID, episodes: em.Episodes, streaming: map[int]aniStream{}}
		for k, v := range em.Streaming() {
			media.streaming[k] = aniStream{Title: v.Title, Image: v.Image}
		}
	}

	n := count
	if n == 0 {
		n = media.episodes
	}
	if n == 0 {
		for k := range tmdb {
			n = max(n, k)
		}
		for k := range media.streaming {
			n = max(n, k)
		}
	}
	var numbers []int
	if n > 0 {
		for i := 1; i <= n; i++ {
			numbers = append(numbers, i)
		}
	} else {
		seen := map[int]bool{}
		for k := range tmdb {
			seen[k] = true
		}
		for k := range media.streaming {
			seen[k] = true
		}
		for k := range seen {
			numbers = append(numbers, k)
		}
		sort.Ints(numbers)
	}

	out := make([]AnimeEpisode, 0, len(numbers))
	for _, number := range numbers {
		base, hasBase := tmdb[number]
		extra := media.streaming[number]
		ep := AnimeEpisode{Number: number, Kind: "standard"}
		if hasBase {
			ep = base
			ep.Number = number
		}
		switch {
		case ep.Title != "":
		case extra.Title != "":
			ep.Title = extra.Title
		default:
			ep.Title = "Episode " + strconv.Itoa(number)
		}
		if ep.Image == "" {
			ep.Image = extra.Image
		}
		if ep.Kind == "" {
			ep.Kind = "standard"
		}
		out = append(out, ep)
	}

	info := AnimeInfo{TMDB: link, AniListID: int64(media.id)}
	if len(tmdb) > 0 {
		info.Providers = append(info.Providers, SourceTMDB)
	}
	if len(media.streaming) > 0 {
		info.Providers = append(info.Providers, SourceAniList)
	}
	if info.AniListID == 0 && mapping != nil && mapping.AnilistID.Valid {
		info.AniListID = mapping.AnilistID.Int64
	}
	return out, info, nil
}

type aniMedia struct {
	id        int
	episodes  int
	streaming map[int]aniStream
}

type aniStream struct{ Title, Image string }

// tmdbEpisodes is _tmdb_episodes: the TMDB episodes of the mapped season past
// the offset, renumbered from 1 and cut to the episode count.
func (c *Catalog) tmdbEpisodes(ctx context.Context, m *mappings.Mapping, count int) (map[int]AnimeEpisode, *TMDBLink) {
	if m == nil || !m.TmdbTv.Valid || !m.TmdbSeason.Valid || toUpper(m.Kind) == "MOVIE" || c.Reg.P.TMDB == nil {
		return nil, nil
	}
	season, err := c.Reg.P.TMDB.Season(ctx, strconv.FormatInt(m.TmdbTv.Int64, 10), int(m.TmdbSeason.Int64))
	if err != nil {
		slog.WarnContext(ctx, "catalog: TMDB season unavailable for mapping", "mapping", m.ID, "err", err)
		return nil, nil
	}
	var raw []map[string]any
	for _, ep := range episodesOf(season) {
		if int64(providers.Int(ep, "episode_number")) > m.TmdbOffset {
			raw = append(raw, ep)
		}
	}
	if count > 0 && len(raw) > count {
		raw = raw[:count]
	}
	out := map[int]AnimeEpisode{}
	for i, ep := range raw {
		e := AnimeEpisode{
			Title:    providers.Str(ep, "name"),
			Overview: providers.Str(ep, "overview"),
			AirDate:  providers.Str(ep, "air_date"),
			Kind:     "standard",
		}
		if still := providers.Str(ep, "still_path"); still != "" {
			e.Image = tmdbStill + still
		}
		if rt := providers.Int(ep, "runtime"); rt > 0 {
			e.Runtime = &rt
		}
		if v, ok := ep["vote_average"].(float64); ok && v > 0 {
			e.Score = &v
		}
		if k := providers.Str(ep, "episode_type"); k != "" {
			e.Kind = k
		}
		out[i+1] = e
	}
	return out, &TMDBLink{TMDBID: m.TmdbTv.Int64, SeasonNumber: m.TmdbSeason.Int64, EpisodeOffset: m.TmdbOffset}
}

func toUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

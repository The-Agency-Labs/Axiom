package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/the-agency-labs/encompass/server/internal/catalog/mappings"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Registry dispatches metadata and search by (kind, source) to the provider
// clients. It replaces services.get_media_metadata and services.search.
type Registry struct {
	P        Providers
	Mappings Mappings
}

// NewRegistry returns a Registry. mappings may be nil (no anime enrichment or
// classification).
func NewRegistry(p Providers, m Mappings) *Registry { return &Registry{P: p, Mappings: m} }

func unsupported(kind, source string) error {
	return fmt.Errorf("%w: %s from %s", ErrUnsupported, kind, source)
}

// Metadata returns the provider metadata for one title.
//
//   - tv, movie: TMDB.
//   - anime, anime_movie from TMDB: the whole show (seasons, specials,
//     episodes) or the film, enriched with AniList data (native title,
//     studios) when a mapping exists. With source mal or anilist (a title
//     with no TMDB match) the MAL/AniList entry itself, a count-based anime.
//   - manga: MangaUpdates, AniList or MAL (the default); comic: Comic Vine;
//     book: Hardcover or Open Library; game: IGDB; boardgame: BoardGameGeek.
func (r *Registry) Metadata(ctx context.Context, kind, source, id string) (Metadata, error) {
	p := r.P
	switch kind {
	case KindTV:
		if source == SourceTMDB && p.TMDB != nil {
			return p.TMDB.TV(ctx, id)
		}
	case KindMovie:
		if source == SourceTMDB && p.TMDB != nil {
			return p.TMDB.Movie(ctx, id)
		}
	case KindAnime, KindAnimeMovie:
		switch source {
		case SourceTMDB:
			if p.TMDB == nil {
				break
			}
			var meta Metadata
			var err error
			if kind == KindAnime {
				meta, err = p.TMDB.TV(ctx, id)
			} else {
				meta, err = p.TMDB.Movie(ctx, id)
			}
			if err != nil {
				return nil, err
			}
			return r.enrichAnime(ctx, kind, id, meta), nil
		case SourceAniList:
			if p.AniList != nil {
				return p.AniList.Anime(ctx, id)
			}
		case SourceMAL:
			if p.MAL != nil {
				return p.MAL.Anime(ctx, id)
			}
		}
	case KindManga:
		switch source {
		case SourceMangaUpdates:
			if p.MangaUpdates != nil {
				return p.MangaUpdates.Manga(ctx, id)
			}
		case SourceAniList:
			if p.AniList != nil {
				return p.AniList.Manga(ctx, id)
			}
		case SourceMAL:
			if p.MAL != nil {
				return p.MAL.Manga(ctx, id)
			}
		}
	case KindComic:
		if source == SourceComicVine && p.ComicVine != nil {
			return p.ComicVine.Comic(ctx, id)
		}
	case KindBook:
		switch source {
		case SourceHardcover:
			if p.Hardcover != nil {
				return p.Hardcover.Book(ctx, id)
			}
		case SourceOpenLibrary:
			if p.OpenLibrary != nil {
				return p.OpenLibrary.Book(ctx, id)
			}
		}
	case KindGame:
		if source == SourceIGDB && p.IGDB != nil {
			return p.IGDB.Game(ctx, id)
		}
	case KindBoardgame:
		if source == SourceBGG && p.BGG != nil {
			return p.BGG.Boardgame(ctx, id)
		}
	}
	return nil, unsupported(kind, source)
}

// Search searches a provider. An empty source picks the default for the kind
// (TMDB for tv/movie; for anime a TMDB-placed search, see searchAnime;
// MyAnimeList for manga; Hardcover for books).
func (r *Registry) Search(ctx context.Context, kind, source, query string, page int, showAdult bool) (SearchPage, error) {
	p := r.P
	if page < 1 {
		page = 1
	}
	switch kind {
	case KindTV:
		if p.TMDB != nil {
			return p.TMDB.Search(ctx, "tv", query, page, showAdult)
		}
	case KindMovie:
		if p.TMDB != nil {
			return p.TMDB.Search(ctx, "movie", query, page, showAdult)
		}
	case KindAnime, KindAnimeMovie:
		switch source {
		case SourceAniList:
			if p.AniList != nil {
				return p.AniList.Search(ctx, "anime", query, page, showAdult)
			}
		case SourceMAL:
			if p.MAL != nil {
				return p.MAL.Search(ctx, "anime", query, page, showAdult)
			}
		default:
			return r.searchAnime(ctx, kind, query, page, showAdult)
		}
	case KindManga:
		switch source {
		case SourceMangaUpdates:
			if p.MangaUpdates != nil {
				return p.MangaUpdates.Search(ctx, query, page, showAdult)
			}
		case SourceAniList:
			if p.AniList != nil {
				return p.AniList.Search(ctx, "manga", query, page, showAdult)
			}
		default:
			if p.MAL != nil {
				return p.MAL.Search(ctx, "manga", query, page, showAdult)
			}
		}
	case KindComic:
		if p.ComicVine != nil {
			return p.ComicVine.Search(ctx, query, page)
		}
	case KindBook:
		if source == SourceOpenLibrary {
			if p.OpenLibrary != nil {
				return p.OpenLibrary.Search(ctx, query, page)
			}
		} else if p.Hardcover != nil {
			return p.Hardcover.Search(ctx, query, page)
		}
	case KindGame:
		if p.IGDB != nil {
			return p.IGDB.Search(ctx, query, page, showAdult)
		}
	case KindBoardgame:
		if p.BGG != nil {
			return p.BGG.Search(ctx, query, page)
		}
	}
	return nil, unsupported(kind, source)
}

// searchAnime searches AniList (the best anime search there is) and places
// every hit in TMDB through the mapping: a hit with a TMDB show (or film, for
// anime_movie) becomes a TMDB result, so adding it creates the whole
// TMDB-structured anime; several cours of one show collapse into one result.
// Hits with no TMDB match stay AniList results (count-based anime).
func (r *Registry) searchAnime(ctx context.Context, kind, query string, page int, showAdult bool) (SearchPage, error) {
	if r.P.AniList == nil {
		return nil, unsupported(kind, "")
	}
	res, err := r.P.AniList.Search(ctx, "anime", query, page, showAdult)
	if err != nil || r.Mappings == nil {
		return res, err
	}
	seen := map[string]bool{}
	out := []providers.Metadata{}
	for _, hit := range providers.List(res, "results") {
		hit = copyMap(hit)
		m, _ := r.Mappings.Lookup(ctx, SourceAniList, idString(hit["media_id"]))
		var tmdbID int64
		hitKind := KindAnime
		if m != nil {
			switch {
			case m.TmdbMovie.Valid && (kind == KindAnimeMovie || !m.TmdbTv.Valid):
				tmdbID, hitKind = m.TmdbMovie.Int64, KindAnimeMovie
			case m.TmdbTv.Valid:
				tmdbID = m.TmdbTv.Int64
			}
		}
		if tmdbID != 0 {
			hit["source"] = SourceTMDB
			hit["media_id"] = tmdbID
			key := hitKind + ":" + strconv.FormatInt(tmdbID, 10)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		hit["media_type"] = hitKind
		out = append(out, hit)
	}
	page2 := copyMap(res)
	page2["results"] = out
	return page2, nil
}

const anilistEnrichQuery = `
query ($id: Int) {
  Media(id: $id, type: ANIME) {
    id idMal
    title { native romaji }
    studios(isMain: true) { nodes { name } }
  }
}`

// enrichAnime adds AniList data to TMDB metadata of an anime: the native
// title, the studios (TMDB only lists production companies) and the AniList
// and MAL ids. Failures are logged and the TMDB metadata is returned as is.
func (r *Registry) enrichAnime(ctx context.Context, kind, id string, meta Metadata) Metadata {
	out := copyMap(meta)
	m := r.pickMapping(ctx, kind, id)
	if m == nil {
		return out
	}
	if m.AnilistID.Valid {
		out["anilist_id"] = m.AnilistID.Int64
	}
	if m.MalID.Valid {
		out["mal_id"] = m.MalID.Int64
	}
	if !m.AnilistID.Valid || r.P.AniList == nil {
		return out
	}
	raw, err := r.P.AniList.GraphQL(ctx, anilistEnrichQuery, map[string]any{"id": m.AnilistID.Int64})
	if err != nil {
		slog.WarnContext(ctx, "catalog: AniList enrichment unavailable", "anilist_id", m.AnilistID.Int64, "err", err)
		return out
	}
	var resp struct {
		Media *struct {
			Title struct {
				Native string `json:"native"`
			} `json:"title"`
			Studios struct {
				Nodes []struct {
					Name string `json:"name"`
				} `json:"nodes"`
			} `json:"studios"`
		} `json:"Media"`
	}
	if json.Unmarshal(raw, &resp) != nil || resp.Media == nil {
		return out
	}
	if n := resp.Media.Title.Native; n != "" {
		out["native_title"] = n
	}
	if len(resp.Media.Studios.Nodes) > 0 {
		names := make([]any, 0, len(resp.Media.Studios.Nodes))
		for _, s := range resp.Media.Studios.Nodes {
			names = append(names, s.Name)
		}
		details := copyMap(asMap(out["details"]))
		details["studios"] = names
		out["details"] = details
	}
	return out
}

// pickMapping chooses the mapping that stands for a whole TMDB show or film:
// the first cour (season 1, no offset) when there is one, else the first row.
func (r *Registry) pickMapping(ctx context.Context, kind, id string) *mappings.Mapping {
	if r.Mappings == nil {
		return nil
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	var rows []mappings.Mapping
	if kind == KindAnimeMovie || kind == KindMovie {
		rows, err = r.Mappings.ByTMDBMovie(ctx, n)
	} else {
		rows, err = r.Mappings.ByTMDBTV(ctx, n)
	}
	if err != nil || len(rows) == 0 {
		return nil
	}
	best := &rows[0]
	for i := range rows {
		if rows[i].TmdbSeason.Valid && rows[i].TmdbSeason.Int64 == 1 && rows[i].TmdbOffset == 0 {
			return &rows[i]
		}
	}
	return best
}

// IsAnime classifies a TMDB title with the mapping: whether a show (or film)
// is anime. Without a mapping store nothing is anime.
func (r *Registry) IsAnime(ctx context.Context, isMovie bool, tmdbID string) bool {
	if r.Mappings == nil {
		return false
	}
	ids, err := r.Mappings.AnimeTMDBIDs(ctx)
	if err != nil {
		return false
	}
	set := ids.Shows
	if isMovie {
		set = ids.Films
	}
	_, ok := set[tmdbID]
	return ok
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	switch l := v.(type) {
	case []any:
		return l
	case []map[string]any:
		out := make([]any, len(l))
		for i, e := range l {
			out[i] = e
		}
		return out
	}
	return nil
}

// idString renders a metadata id (string or JSON number) without a decimal point.
func idString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

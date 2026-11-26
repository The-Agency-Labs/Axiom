package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/the-agency-labs/axiom/server/internal/catalog/mappings"
	"github.com/the-agency-labs/axiom/server/internal/providers"
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
//   - tv, anime from TVDB: the series with its aired seasons, entirely from
//     TheTVDB (no TMDB or AniList data is mixed in; the anilist/mal ids of the
//     mapping are added for matching only).
//   - movie: TMDB. anime_movie from TMDB: the film, enriched with AniList data
//     (native title, studios) when a mapping exists.
//   - tv, anime from TMDB: a show with no TVDB match that was never converted.
//   - anime with source mal or anilist (a title with no TVDB or TMDB match) is
//     the MAL/AniList entry itself, a count-based anime.
//   - manga: MangaUpdates, AniList or MAL (the default); comic: Comic Vine;
//     book: Hardcover or Open Library; game: IGDB; boardgame: BoardGameGeek.
func (r *Registry) Metadata(ctx context.Context, kind, source, id string) (Metadata, error) {
	p := r.P
	switch kind {
	case KindTV:
		if source == SourceTVDB && p.TVDB != nil {
			return p.TVDB.Series(ctx, id)
		}
		if source == SourceTMDB && p.TMDB != nil {
			return p.TMDB.TV(ctx, id)
		}
	case KindMovie:
		if source == SourceTMDB && p.TMDB != nil {
			return p.TMDB.Movie(ctx, id)
		}
	case KindAnime, KindAnimeMovie:
		switch source {
		case SourceTVDB:
			if p.TVDB == nil || kind != KindAnime {
				break
			}
			meta, err := p.TVDB.Series(ctx, id)
			if err != nil {
				return nil, err
			}
			return r.withTVDBMappingIDs(ctx, id, meta), nil
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
		if p.TVDB != nil {
			return r.searchShows(ctx, KindTV, query, page, showAdult)
		}
		if p.TMDB != nil { // no TVDB key: the old TMDB behaviour
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
			if kind == KindAnime && p.TVDB != nil {
				return r.searchShows(ctx, KindAnime, query, page, showAdult)
			}
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
				return r.searchManga(ctx, query, page, showAdult)
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

// searchShows is the show search: TVDB's own search (anime and live action
// alike), split by the Fribb mapping into tv and anime, so "tv" lists the live
// action and "anime" the anime. The anime search also asks AniList (the best
// anime search there is) and places its hits in TVDB, so romaji titles that
// TVDB's aliases miss are found; AniList hits come first.
func (r *Registry) searchShows(ctx context.Context, kind, query string, page int, showAdult bool) (SearchPage, error) {
	res, err := r.P.TVDB.Search(ctx, query, page)
	if err != nil && kind != KindAnime {
		return nil, err
	}
	var anime map[string]struct{}
	if r.Mappings != nil {
		if ids, ierr := r.Mappings.AnimeTMDBIDs(ctx); ierr == nil {
			anime = ids.TVDBShows
		}
	}
	out := []providers.Metadata{}
	seen := map[string]bool{}
	if kind == KindAnime && r.P.AniList != nil {
		if placed, perr := r.searchAnime(ctx, kind, query, page, showAdult); perr == nil {
			for _, hit := range providers.List(placed, "results") {
				out = append(out, hit)
				seen[providers.Str(hit, "source")+":"+idString(hit["media_id"])] = true
			}
		} else if err != nil {
			return nil, err
		}
	}
	for _, hit := range providers.List(res, "results") {
		id := idString(hit["media_id"])
		_, isAnime := anime[id]
		if isAnime != (kind == KindAnime) || seen["tvdb:"+id] {
			continue
		}
		hit = copyMap(hit)
		hit["media_type"] = kind
		out = append(out, hit)
	}
	page2 := copyMap(res)
	if page2 == nil {
		page2 = SearchPage{"page": page, "total_results": len(out), "total_pages": 1}
	}
	page2["results"] = out
	return page2, nil
}

// searchAnime searches AniList (the best anime search there is) and places
// every hit through the mapping: a hit with a TVDB series becomes a TVDB
// result (adding it creates the whole show with TVDB's seasons), a hit that is
// a film (and for anime_movie any hit with a TMDB film) becomes a TMDB film;
// several cours of one show collapse into one result. Hits with no match stay
// AniList results (count-based anime).
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
		var placedID int64
		placedSource := SourceTMDB
		hitKind := KindAnime
		if m != nil {
			switch {
			case m.TmdbMovie.Valid && (kind == KindAnimeMovie || toUpper(m.Kind) == "MOVIE" || (!m.TvdbID.Valid && !m.TmdbTv.Valid)):
				placedID, hitKind = m.TmdbMovie.Int64, KindAnimeMovie
			case m.TvdbID.Valid && r.P.TVDB != nil:
				placedID, placedSource = m.TvdbID.Int64, SourceTVDB
			case m.TmdbTv.Valid && r.P.TVDB == nil: // no TVDB key: the old TMDB behaviour
				placedID = m.TmdbTv.Int64
			}
		}
		if placedID != 0 {
			hit["source"] = placedSource
			hit["media_id"] = placedID
			key := hitKind + ":" + placedSource + ":" + strconv.FormatInt(placedID, 10)
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

// withTVDBMappingIDs adds the AniList and MAL ids of the series' first cour
// to its metadata, for matching (they are never a metadata source).
func (r *Registry) withTVDBMappingIDs(ctx context.Context, tvdbID string, meta Metadata) Metadata {
	out := copyMap(meta)
	if r.Mappings == nil {
		return out
	}
	n, err := strconv.ParseInt(tvdbID, 10, 64)
	if err != nil {
		return out
	}
	rows, err := r.Mappings.ByTVDB(ctx, n)
	if err != nil || len(rows) == 0 {
		return out
	}
	best := rows[0]
	for _, m := range rows {
		if m.TvdbSeason.Valid && m.TvdbSeason.Int64 == 1 && m.TvdbOffset == 0 {
			best = m
			break
		}
	}
	if best.AnilistID.Valid {
		out["anilist_id"] = best.AnilistID.Int64
	}
	if best.MalID.Valid {
		out["mal_id"] = best.MalID.Int64
	}
	return out
}

// IsAnimeTVDB classifies a TVDB series with the mapping: whether it is anime.
func (r *Registry) IsAnimeTVDB(ctx context.Context, tvdbID string) bool {
	if r.Mappings == nil {
		return false
	}
	ids, err := r.Mappings.AnimeTMDBIDs(ctx)
	if err != nil {
		return false
	}
	_, ok := ids.TVDBShows[tvdbID]
	return ok
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

// searchManga is the default manga search: MyAnimeList first, always. On the
// first page, MangaUpdates fills the gaps MAL doesn't track (many manhua and
// webcomics): a MangaUpdates hit is added after MAL's only when no MAL hit
// shares one of its names and it contains every word of the query (MU's own
// search is loose). Either provider failing leaves the other's results.
func (r *Registry) searchManga(ctx context.Context, query string, page int, showAdult bool) (SearchPage, error) {
	if r.P.MangaUpdates == nil || page > 1 {
		return r.P.MAL.Search(ctx, "manga", query, page, showAdult)
	}
	var (
		mu    SearchPage
		muErr error
		wg    sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		mu, muErr = r.P.MangaUpdates.Search(ctx, query, 1, showAdult)
	}()
	mal, malErr := r.P.MAL.Search(ctx, "manga", query, page, showAdult)
	wg.Wait()
	if malErr != nil {
		if muErr != nil {
			return nil, malErr
		}
		return mu, nil
	}
	if muErr != nil {
		slog.Warn("manga search: MangaUpdates failed", "err", muErr)
		return mal, nil
	}

	known := map[string]bool{}
	malHits := providers.List(mal, "results")
	out := make([]providers.Metadata, 0, len(malHits))
	for _, hit := range malHits {
		for _, n := range hitNames(hit) {
			known[n] = true
		}
		out = append(out, hit)
	}
	words := strings.Fields(mangaKey(query))
	for _, hit := range providers.List(mu, "results") {
		names := hitNames(hit)
		dup, relevant := false, false
		for _, n := range names {
			dup = dup || known[n]
			relevant = relevant || hasAllWords(n, words)
		}
		if dup || !relevant {
			continue
		}
		for _, n := range names {
			known[n] = true
		}
		out = append(out, hit)
	}
	merged := copyMap(mal)
	merged["results"] = out
	return merged, nil
}

// hitNames is a search hit's title, native title and aliases as match keys.
func hitNames(hit providers.Metadata) []string {
	raw := []string{providers.Str(hit, "title"), providers.Str(hit, "native_title")}
	switch a := hit["aliases"].(type) {
	case []string:
		raw = append(raw, a...)
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok {
				raw = append(raw, s)
			}
		}
	}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if k := mangaKey(s); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// mangaKey normalises a title for matching across providers: lower case,
// letters and digits only, a trailing "(Novel)"-style note and a leading
// "the" dropped.
func mangaKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndex(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		s = s[:i]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	f := strings.Fields(b.String())
	if len(f) > 1 && f[0] == "the" {
		f = f[1:]
	}
	return strings.Join(f, " ")
}

// hasAllWords reports whether every query word appears as a word of name.
func hasAllWords(name string, words []string) bool {
	if len(words) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, w := range strings.Fields(name) {
		have[w] = true
	}
	for _, w := range words {
		if !have[w] {
			return false
		}
	}
	return true
}

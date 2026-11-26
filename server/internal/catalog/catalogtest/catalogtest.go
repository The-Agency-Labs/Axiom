// Package catalogtest has fakes for testing code built on the catalog: a
// scriptable TMDB, an in-memory anime mapping store, a temp database and a
// controllable clock. It is only imported by tests.
package catalogtest

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/mappings"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Episode describes one TMDB episode of a fake show.
type Episode struct {
	ID      int64 // TVDB episode id (TVDB fake only)
	Number  int
	AirDate string // "" = undated
	Name    string
	Runtime int
}

// Season describes one TMDB season of a fake show.
type Season struct {
	Number   int
	AirDate  string
	Episodes []Episode
}

// Eps builds n episodes numbered from first, aired on date ("" = undated).
func Eps(first, n int, date string) []Episode {
	out := make([]Episode, n)
	for i := range out {
		out[i] = Episode{Number: first + i, AirDate: date, Name: fmt.Sprintf("Episode %d", first+i), Runtime: 24}
	}
	return out
}

// TMDB is a scriptable fake of the TMDB client.
type TMDB struct {
	mu      sync.Mutex
	shows   map[string]Metadata
	seasons map[string]map[int]Metadata
	movies  map[string]Metadata
	// TVChangesSet and MovieChangesSet are what the change feeds return.
	TVChangesSet, MovieChangesSet map[string]struct{}
	// Calls counts client calls by name ("TV", "TVWithSeasons", ...).
	Calls map[string]int
	// Err, when set, is returned by every call.
	Err error
}

// Metadata is the provider metadata map.
type Metadata = providers.Metadata

// NewTMDB returns an empty fake.
func NewTMDB() *TMDB {
	return &TMDB{
		shows: map[string]Metadata{}, seasons: map[string]map[int]Metadata{}, movies: map[string]Metadata{},
		Calls: map[string]int{},
	}
}

// AddShow registers a show with its seasons, in the shape the real client returns.
func (f *TMDB) AddShow(id, title string, seasons ...Season) *TMDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	var related []any
	total := 0
	f.seasons[id] = map[int]Metadata{}
	for _, s := range seasons {
		var raw []any
		for _, e := range s.Episodes {
			ep := map[string]any{"episode_number": e.Number, "name": e.Name, "overview": "", "runtime": e.Runtime, "still_path": "/s.jpg"}
			if e.AirDate != "" {
				ep["air_date"] = e.AirDate
			} else {
				ep["air_date"] = nil
			}
			raw = append(raw, ep)
		}
		if s.Number != 0 {
			total += len(s.Episodes)
		}
		related = append(related, map[string]any{
			"source": "tmdb", "media_type": "season", "media_id": id, "title": title,
			"season_number": s.Number, "season_title": fmt.Sprintf("Season %d", s.Number),
			"first_air_date": nilIfEmpty(s.AirDate), "max_progress": len(s.Episodes), "image": "https://img/season.jpg",
		})
		f.seasons[id][s.Number] = Metadata{
			"media_id": id, "title": title, "season_number": s.Number, "season_title": fmt.Sprintf("Season %d", s.Number),
			"image": "https://img/season.jpg", "episodes": raw,
		}
	}
	f.shows[id] = Metadata{
		"media_id": id, "source": "tmdb", "media_type": "tv", "title": title, "max_progress": total,
		"image": "https://img/poster.jpg", "synopsis": "A show.", "genres": []any{"Drama"},
		"details":        map[string]any{"first_air_date": "2010-01-08", "seasons": len(seasons), "episodes": total},
		"related":        map[string]any{"seasons": related},
		"tvdb_id":        float64(7000),
		"external_links": map[string]any{"IMDb": "https://www.imdb.com/title/tt0042/", "TVDB": "https://www.thetvdb.com/dereferrer/series/7000"},
	}
	return f
}

// SetTVDBID sets the TVDB id TMDB lists for a show ("" = none, as for a show
// TMDB has not linked to TVDB).
func (f *TMDB) SetTVDBID(showID, tvdbID string) *TMDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.shows[showID]
	if tvdbID == "" {
		delete(m, "tvdb_id")
		m["external_links"] = map[string]any{}
	} else {
		m["tvdb_id"] = tvdbID
	}
	return f
}

// AddMovie registers a movie.
func (f *TMDB) AddMovie(id, title, releaseDate string) *TMDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.movies[id] = Metadata{
		"media_id": id, "source": "tmdb", "media_type": "movie", "title": title, "max_progress": 1,
		"image": "https://img/movie.jpg", "synopsis": "A movie.", "genres": []any{"Action"},
		"details":        map[string]any{"release_date": nilIfEmpty(releaseDate)},
		"external_links": map[string]any{"IMDb": "https://www.imdb.com/title/tt0099/"},
	}
	return f
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *TMDB) call(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls[name]++
	return f.Err
}

// TV implements catalog.TMDB.
func (f *TMDB) TV(_ context.Context, id string) (Metadata, error) {
	if err := f.call("TV"); err != nil {
		return nil, err
	}
	m, ok := f.shows[id]
	if !ok {
		return nil, providers.NotFoundError("The Movie Database", "tv", id)
	}
	return clone(m), nil
}

// Movie implements catalog.TMDB.
func (f *TMDB) Movie(_ context.Context, id string) (Metadata, error) {
	if err := f.call("Movie"); err != nil {
		return nil, err
	}
	m, ok := f.movies[id]
	if !ok {
		return nil, providers.NotFoundError("The Movie Database", "movie", id)
	}
	return clone(m), nil
}

// TVWithSeasons implements catalog.TMDB.
func (f *TMDB) TVWithSeasons(_ context.Context, id string, numbers []int) (Metadata, error) {
	if err := f.call("TVWithSeasons"); err != nil {
		return nil, err
	}
	show, ok := f.shows[id]
	if !ok {
		return nil, providers.NotFoundError("The Movie Database", "tv", id)
	}
	out := clone(show)
	for _, n := range numbers {
		if s, ok := f.seasons[id][n]; ok {
			out[fmt.Sprintf("season/%d", n)] = clone(s)
		}
	}
	return out, nil
}

// Season implements catalog.TMDB.
func (f *TMDB) Season(_ context.Context, id string, n int) (Metadata, error) {
	if err := f.call("Season"); err != nil {
		return nil, err
	}
	s, ok := f.seasons[id][n]
	if !ok {
		return nil, providers.NotFoundError("The Movie Database", "season", id)
	}
	return clone(s), nil
}

// Search implements catalog.TMDB.
// Movie searches match the fake's films by title (case-insensitive substring).
func (f *TMDB) Search(_ context.Context, mediaType, query string, _ int, _ bool) (providers.SearchPage, error) {
	results := []providers.Metadata{}
	if mediaType == "movie" {
		f.mu.Lock()
		for id, m := range f.movies {
			title, _ := m["title"].(string)
			if query == "" || !strings.Contains(strings.ToLower(title), strings.ToLower(query)) {
				continue
			}
			year := 0
			if d, ok := m["details"].(map[string]any); ok {
				if rd, _ := d["release_date"].(string); len(rd) >= 4 {
					year, _ = strconv.Atoi(rd[:4])
				}
			}
			n, _ := strconv.Atoi(id)
			results = append(results, providers.Metadata{"media_id": n, "source": "tmdb", "media_type": "movie", "title": title, "release_year": year})
		}
		f.mu.Unlock()
	}
	return providers.SearchPage{"results": results}, f.call("Search")
}

// Find implements catalog.TMDB.
func (f *TMDB) Find(context.Context, string, string) (map[string]any, error) {
	return map[string]any{}, f.call("Find")
}

// TVChanges implements catalog.TMDB.
func (f *TMDB) TVChanges(context.Context) (map[string]struct{}, error) {
	return f.TVChangesSet, f.call("TVChanges")
}

// MovieChanges implements catalog.TMDB.
func (f *TMDB) MovieChanges(context.Context) (map[string]struct{}, error) {
	return f.MovieChangesSet, f.call("MovieChanges")
}

// clone deep-copies through the same loose typing the real client returns.
func clone(m Metadata) Metadata {
	out := Metadata{}
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

// Mappings is an in-memory fake of the anime mapping store.
type Mappings struct {
	Rows []mappings.Mapping
}

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }

// AddShow registers an anime mapping row of a TMDB show.
func (m *Mappings) AddShow(tmdbTV, season, offset, anilistID, malID int64) {
	m.Rows = append(m.Rows, mappings.Mapping{
		ID: int64(len(m.Rows) + 1), AnilistID: nullInt(anilistID), MalID: nullInt(malID), Kind: "TV",
		TmdbTv: nullInt(tmdbTV), TmdbSeason: sql.NullInt64{Int64: season, Valid: true}, TmdbOffset: offset,
	})
}

// AddTVDBShow registers an anime mapping row of a TVDB series: the cour is
// season `season` of it, after `offset` episodes (season 0 = specials).
func (m *Mappings) AddTVDBShow(tvdbID, season, offset, anilistID, malID int64) {
	m.Rows = append(m.Rows, mappings.Mapping{
		ID: int64(len(m.Rows) + 1), AnilistID: nullInt(anilistID), MalID: nullInt(malID), Kind: "TV",
		TvdbID: nullInt(tvdbID), TvdbSeason: sql.NullInt64{Int64: season, Valid: true}, TvdbOffset: offset,
	})
}

// ByTVDB implements catalog.Mappings.
func (m *Mappings) ByTVDB(_ context.Context, id int64) ([]mappings.Mapping, error) {
	var out []mappings.Mapping
	for _, r := range m.Rows {
		if r.TvdbID.Valid && r.TvdbID.Int64 == id {
			out = append(out, r)
		}
	}
	return out, nil
}

// AddMovie registers an anime mapping row of a TMDB film.
func (m *Mappings) AddMovie(tmdbMovie, anilistID, malID int64) {
	m.Rows = append(m.Rows, mappings.Mapping{
		ID: int64(len(m.Rows) + 1), AnilistID: nullInt(anilistID), MalID: nullInt(malID), Kind: "MOVIE", TmdbMovie: nullInt(tmdbMovie),
	})
}

// Lookup implements catalog.Mappings.
func (m *Mappings) Lookup(_ context.Context, source, id string) (*mappings.Mapping, error) {
	for i := range m.Rows {
		r := m.Rows[i]
		v := r.MalID
		if source == "anilist" {
			v = r.AnilistID
		}
		if v.Valid && fmt.Sprint(v.Int64) == id {
			return &r, nil
		}
	}
	return nil, nil
}

// ByTMDBTV implements catalog.Mappings.
func (m *Mappings) ByTMDBTV(_ context.Context, id int64) ([]mappings.Mapping, error) {
	var out []mappings.Mapping
	for _, r := range m.Rows {
		if r.TmdbTv.Valid && r.TmdbTv.Int64 == id {
			out = append(out, r)
		}
	}
	return out, nil
}

// ByTMDBMovie implements catalog.Mappings.
func (m *Mappings) ByTMDBMovie(_ context.Context, id int64) ([]mappings.Mapping, error) {
	var out []mappings.Mapping
	for _, r := range m.Rows {
		if r.TmdbMovie.Valid && r.TmdbMovie.Int64 == id {
			out = append(out, r)
		}
	}
	return out, nil
}

// AnimeTMDBIDs implements catalog.Mappings.
func (m *Mappings) AnimeTMDBIDs(context.Context) (mappings.TMDBIDs, error) {
	ids := mappings.TMDBIDs{Shows: map[string]struct{}{}, Films: map[string]struct{}{}, TVDBShows: map[string]struct{}{}}
	for _, r := range m.Rows {
		if r.TvdbID.Valid {
			ids.TVDBShows[fmt.Sprint(r.TvdbID.Int64)] = struct{}{}
		}
		if r.TmdbTv.Valid {
			ids.Shows[fmt.Sprint(r.TmdbTv.Int64)] = struct{}{}
		}
		if r.TmdbMovie.Valid {
			ids.Films[fmt.Sprint(r.TmdbMovie.Int64)] = struct{}{}
		}
	}
	return ids, nil
}

// Env is a catalog on a temp database with fake providers and a fixed clock.
type Env struct {
	Conn     *sql.DB
	Q        *db.Queries
	Catalog  *catalog.Catalog
	TMDB     *TMDB
	TVDB     *TVDB
	Mappings *Mappings
	clock    time.Time
	mu       sync.Mutex

	tmdb catalog.TMDB
	tvdb catalog.TVDB
}

// NewEnv builds an Env whose clock starts at start (UTC).
func NewEnv(t testing.TB, start time.Time) *Env {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	e := &Env{Conn: conn, Q: db.New(conn), TMDB: NewTMDB(), TVDB: NewTVDB(), Mappings: &Mappings{}, clock: start.UTC()}
	e.tmdb = e.TMDB
	e.rebuild() // TMDB only until UseTVDB: the TMDB-show tests predate TVDB shows
	return e
}

// Now is the Env clock.
func (e *Env) Now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// Advance moves the clock forward.
func (e *Env) Advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clock = e.clock.Add(d)
}

// User inserts a user and returns its id.
func (e *Env) User(t testing.TB, name string) int64 {
	t.Helper()
	res, err := e.Conn.Exec(`INSERT INTO users (username, password_hash, webhook_token, created_at) VALUES (?, 'x', ?, '2020-01-01T00:00:00Z')`, name, "tok-"+name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

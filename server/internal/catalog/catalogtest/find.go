package catalogtest

import (
	"context"
	"sync"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
)

// FindTMDB wraps a TMDB fake with a scriptable Find (lookup by external id).
type FindTMDB struct {
	*TMDB
	mu      sync.Mutex
	results map[string]map[string]any
	// Finds records each call as "<source>:<id>".
	Finds []string
}

// NewFindTMDB wraps t.
func NewFindTMDB(t *TMDB) *FindTMDB {
	return &FindTMDB{TMDB: t, results: map[string]map[string]any{}}
}

// AddEpisodeResult scripts Find(id, source) to return one tv episode result.
func (f *FindTMDB) AddEpisodeResult(source, id string, showID, season, episode int) {
	f.set(source, id, map[string]any{"tv_episode_results": []any{
		map[string]any{"show_id": float64(showID), "season_number": float64(season), "episode_number": float64(episode)},
	}})
}

// AddMovieResult scripts Find(id, source) to return one movie result.
func (f *FindTMDB) AddMovieResult(source, id string, movieID int) {
	f.set(source, id, map[string]any{"movie_results": []any{map[string]any{"id": float64(movieID)}}})
}

func (f *FindTMDB) set(source, id string, resp map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[source+":"+id] = resp
}

// Find implements catalog.TMDB: the scripted response, else empty results.
func (f *FindTMDB) Find(_ context.Context, id, source string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Finds = append(f.Finds, source+":"+id)
	if r, ok := f.results[source+":"+id]; ok {
		return r, nil
	}
	return map[string]any{"tv_episode_results": []any{}, "movie_results": []any{}}, nil
}

// UseTMDB rebuilds the Env's catalog on another TMDB implementation (e.g. a
// FindTMDB); the Env keeps using its clock.
func (e *Env) UseTMDB(p catalog.TMDB) {
	e.tmdb = p
	e.rebuild()
}

// UseTVDB adds a TVDB client (normally the Env's TVDB fake) to the catalog:
// from then on shows are TVDB shows.
func (e *Env) UseTVDB(p catalog.TVDB) {
	e.tvdb = p
	e.rebuild()
}

func (e *Env) rebuild() {
	reg := catalog.NewRegistry(catalog.Providers{TMDB: e.tmdb, TVDB: e.tvdb}, e.Mappings)
	e.Catalog = catalog.New(e.Conn, e.Q, reg)
	e.Catalog.Now = e.Now
}

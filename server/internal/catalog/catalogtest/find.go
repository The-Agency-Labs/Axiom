package catalogtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/providers"
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
	reg := catalog.NewRegistry(catalog.Providers{TMDB: p}, e.Mappings)
	e.Catalog = catalog.New(e.Conn, e.Q, reg)
	e.Catalog.Now = e.Now
}

// TVDB is a scriptable fake of the TVDB client's Episode and SeriesTMDBID.
type TVDB struct {
	mu       sync.Mutex
	episodes map[string]providers.Metadata
	series   map[string]string
	// Calls records "episode:<id>" and "series:<id>".
	Calls []string
}

// NewTVDB returns an empty fake: every episode is a 404 (a miss).
func NewTVDB() *TVDB {
	return &TVDB{episodes: map[string]providers.Metadata{}, series: map[string]string{}}
}

// AddEpisode scripts a TVDB episode.
func (f *TVDB) AddEpisode(id string, seriesID, season, episode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.episodes[id] = providers.Metadata{
		"episode_id": id, "series_id": float64(seriesID), "season_number": float64(season), "episode_number": float64(episode),
	}
}

// AddSeries scripts the TMDB id of a TVDB series.
func (f *TVDB) AddSeries(seriesID int, tmdbID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.series[fmt.Sprint(seriesID)] = tmdbID
}

// Episode returns (nil, nil) for unknown episodes, like the real client on a 404.
func (f *TVDB) Episode(_ context.Context, id string) (providers.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "episode:"+id)
	return f.episodes[id], nil
}

// SeriesTMDBID returns "" for unknown series.
func (f *TVDB) SeriesTMDBID(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "series:"+id)
	return f.series[id], nil
}

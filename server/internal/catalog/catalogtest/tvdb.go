package catalogtest

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/the-agency-labs/axiom/server/internal/providers"
	"github.com/the-agency-labs/axiom/server/internal/providers/tvdb"
)

// TVDB is a scriptable fake of the TVDB client: series with aired seasons,
// episodes by id, characters and people, search and remote-id lookups.
type TVDB struct {
	mu       sync.Mutex
	shows    map[string]*tvdbShow
	episodes map[string]epRef // by id
	remote   map[string]tvdb.Hit
	people   map[string]map[string]string
	chars    map[string][]tvdb.Character
	// Calls records "series:<id>", "episode:<id>", "search:<q>", "remote:<id>", "person:<id>".
	Calls []string
	// Err, when set, is returned by every call.
	Err error
}

type tvdbShow struct {
	title    string
	native   string
	lang     string
	seasons  []tvdb.Season
	tmdb     string
	imdb     string
	status   string
	overview string
}

// NewTVDB returns an empty fake: every series is a 404.
func NewTVDB() *TVDB {
	return &TVDB{
		shows: map[string]*tvdbShow{}, episodes: map[string]epRef{}, remote: map[string]tvdb.Hit{},
		people: map[string]map[string]string{}, chars: map[string][]tvdb.Character{},
	}
}

// AddShow registers a series with its aired seasons. Episodes with an ID are
// also findable by it (Episode, FindByRemoteID aside).
func (f *TVDB) AddShow(id, title string, seasons ...Season) *TVDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	show := &tvdbShow{title: title, lang: "eng", status: "Continuing", overview: "A show."}
	for _, s := range seasons {
		out := tvdb.Season{Number: s.Number, AirDate: s.AirDate, Image: "https://img/season.jpg"}
		for _, e := range s.Episodes {
			ep := tvdb.Episode{
				ID: e.ID, Season: s.Number, Number: e.Number, Name: e.Name, Aired: e.AirDate, Runtime: e.Runtime,
				Image: "https://img/ep.jpg",
			}
			out.Episodes = append(out.Episodes, ep)
			if e.ID != 0 {
				f.episodes[strconv.FormatInt(e.ID, 10)] = epRef{ep: ep, series: id}
			}
		}
		show.seasons = append(show.seasons, out)
	}
	f.shows[id] = show
	return f
}

type epRef struct {
	ep     tvdb.Episode
	series string
}

// SetIDs sets the TMDB and IMDb ids the series lists (cross ids, for matching).
func (f *TVDB) SetIDs(id, tmdbID, imdbID string) *TVDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.shows[id]; s != nil {
		s.tmdb, s.imdb = tmdbID, imdbID
	}
	return f
}

// SetStatus sets the series status ("Ended", "Continuing").
func (f *TVDB) SetStatus(id, status string) *TVDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.shows[id]; s != nil {
		s.status = status
	}
	return f
}

// AddCharacters scripts the cast and crew rows of a series.
func (f *TVDB) AddCharacters(seriesID string, chars ...tvdb.Character) *TVDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chars[seriesID] = append(f.chars[seriesID], chars...)
	return f
}

// AddPerson scripts the other-database ids of a TVDB person ("tmdb", "imdb").
func (f *TVDB) AddPerson(personID string, ids map[string]string) *TVDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.people[personID] = ids
	return f
}

// AddRemote scripts FindByRemoteID.
func (f *TVDB) AddRemote(remoteID string, hit tvdb.Hit) *TVDB {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remote[remoteID] = hit
	return f
}

func (f *TVDB) call(s string) error {
	f.Calls = append(f.Calls, s)
	return f.Err
}

func (f *TVDB) show(id string) (*tvdbShow, error) {
	s := f.shows[id]
	if s == nil {
		return nil, providers.NotFoundError(tvdb.Label, "series", id)
	}
	return s, nil
}

// Series implements catalog.TVDB.
func (f *TVDB) Series(_ context.Context, id string) (providers.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("series:" + id); err != nil {
		return nil, err
	}
	s, err := f.show(id)
	if err != nil {
		return nil, err
	}
	var related []any
	total, regularSeasons := 0, 0
	for _, se := range s.seasons {
		title := fmt.Sprintf("Season %d", se.Number)
		if se.Number == 0 {
			title = "Specials"
		} else {
			total += len(se.Episodes)
			regularSeasons++
		}
		related = append(related, map[string]any{
			"source": "tvdb", "media_type": "season", "media_id": id, "title": s.title,
			"season_number": se.Number, "season_title": title, "image": se.Image,
			"first_air_date": nilIfEmpty(se.AirDate), "max_progress": len(se.Episodes),
		})
	}
	remote := map[string]any{}
	if s.tmdb != "" {
		remote["tmdb"] = s.tmdb
	}
	if s.imdb != "" {
		remote["imdb"] = s.imdb
	}
	return providers.Metadata{
		"media_id": id, "source": "tvdb", "media_type": "tv", "title": s.title, "native_title": s.native,
		"max_progress": total, "image": "https://img/poster.jpg", "synopsis": s.overview, "genres": []any{"Drama"},
		"details": map[string]any{
			"format": "TV", "first_air_date": "2010-01-08", "status": s.status, "seasons": regularSeasons, "episodes": total,
		},
		"related": map[string]any{"seasons": related}, "tvdb_id": id, "remote_ids": remote,
		"external_links": map[string]any{"TVDB": "https://thetvdb.com/dereferrer/series/" + id},
	}, nil
}

// Seasons implements catalog.TVDB.
func (f *TVDB) Seasons(_ context.Context, id string) ([]tvdb.Season, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("seasons:" + id); err != nil {
		return nil, err
	}
	s, err := f.show(id)
	if err != nil {
		return nil, err
	}
	return s.seasons, nil
}

// Season implements catalog.TVDB.
func (f *TVDB) Season(ctx context.Context, id string, number int) (providers.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.show(id)
	if err != nil {
		return nil, err
	}
	for _, se := range s.seasons {
		if se.Number == number {
			return providers.Metadata{"source": "tvdb", "season_number": number, "title": s.title, "max_progress": len(se.Episodes)}, nil
		}
	}
	return nil, providers.NotFoundError(tvdb.Label, "season", fmt.Sprint(number))
}

// Search implements catalog.TVDB: shows whose title contains the query.
func (f *TVDB) Search(_ context.Context, query string, page int) (providers.SearchPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("search:" + query); err != nil {
		return nil, err
	}
	results := []providers.Metadata{}
	for id, s := range f.shows {
		if strings.Contains(strings.ToLower(s.title), strings.ToLower(query)) {
			results = append(results, providers.Metadata{
				"media_id": id, "source": "tvdb", "media_type": "tv", "title": s.title, "image": "https://img/poster.jpg",
			})
		}
	}
	sortResults(results)
	return providers.SearchPage{"page": page, "total_results": len(results), "total_pages": 1, "results": results}, nil
}

func sortResults(r []providers.Metadata) {
	for i := 1; i < len(r); i++ {
		for j := i; j > 0 && fmt.Sprint(r[j]["media_id"]) < fmt.Sprint(r[j-1]["media_id"]); j-- {
			r[j], r[j-1] = r[j-1], r[j]
		}
	}
}

// Characters implements catalog.TVDB.
func (f *TVDB) Characters(_ context.Context, id string) ([]tvdb.Character, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("characters:" + id); err != nil {
		return nil, err
	}
	return f.chars[id], nil
}

// PersonRemoteIDs implements catalog.TVDB.
func (f *TVDB) PersonRemoteIDs(_ context.Context, personID string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("person:" + personID); err != nil {
		return nil, err
	}
	return f.people[personID], nil
}

// FindByRemoteID implements catalog.TVDB.
func (f *TVDB) FindByRemoteID(_ context.Context, remoteID string) (*tvdb.Hit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("remote:" + remoteID); err != nil {
		return nil, err
	}
	if h, ok := f.remote[remoteID]; ok {
		return &h, nil
	}
	return nil, nil
}

// Episode implements catalog.TVDB: (nil, nil) for an unknown episode, like the
// real client on a 404.
func (f *TVDB) Episode(_ context.Context, id string) (providers.Metadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("episode:" + id); err != nil {
		return nil, err
	}
	r, ok := f.episodes[id]
	if !ok {
		return nil, nil
	}
	sid, _ := strconv.ParseFloat(r.series, 64)
	return providers.Metadata{
		"episode_id": id, "series_id": sid, "season_number": float64(r.ep.Season), "episode_number": float64(r.ep.Number),
	}, nil
}

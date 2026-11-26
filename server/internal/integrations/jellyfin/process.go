// Package jellyfin is the Jellyfin integration: the playback webhook, the
// watch-history import and its 6-hourly sync job. It ports
// src/integrations/webhooks/{base,jellyfin,tv,movie}.py and
// src/integrations/imports/jellyfin.py.
//
// Every play is recorded with source library.SourceJellyfin, so the library
// rules apply: nothing is ever undone or lowered, imports are additive, and a
// show first seen at season N assumes seasons 1..N-1 watched. Shows (anime
// too) are TVDB shows and Jellyfin numbers by TVDB, so episodes map directly;
// movies are TMDB. The AniDB/MAL routing helpers are not ported, and
// MarkPlayed/MarkUnplayed events are ignored (they could only delete).
package jellyfin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/cache"
	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/providers"
	"github.com/the-agency-labs/axiom/server/internal/providers/tvdb"
)

// processMu serializes event processing across Service instances (the
// webhook and the sync job each hold one), so a Play and a Stop arriving
// together cannot both create the same entry.
var processMu sync.Mutex

// TVDB is the slice of the TVDB client the matcher uses.
type TVDB interface {
	Episode(ctx context.Context, episodeID string) (providers.Metadata, error)
	FindByRemoteID(ctx context.Context, remoteID string) (*tvdb.Hit, error)
}

// Deps are what a Service needs.
type Deps struct {
	Conn    *sql.DB
	Q       *db.Queries
	Catalog *catalog.Catalog
	Library *library.Service
	TMDB    catalog.TMDB // Find, for movies
	TVDB    TVDB         // nil: episodes cannot be matched
	Jobs    *jobs.Manager
	Config  config.Config
	HTTP    *http.Client
	Now     func() time.Time
}

// Service processes Jellyfin events and runs the history import.
type Service struct {
	Deps
}

// New returns a Service; Now defaults to the wall clock.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{Deps: d}
}

// NewService builds a Service on the app's shared parts, with the catalog's
// TVDB client (a client of its own when the catalog has none).
func NewService(conn *sql.DB, q *db.Queries, cfg config.Config, cat *catalog.Catalog, lib *library.Service, c *cache.Cache, m *jobs.Manager) *Service {
	d := Deps{Conn: conn, Q: q, Catalog: cat, Library: lib, Jobs: m, Config: cfg,
		TVDB: tvdb.New(providers.NewClient(c), cfg)}
	if cat != nil && cat.Reg != nil {
		d.TMDB = cat.Reg.P.TMDB
		if cat.Reg.P.TVDB != nil { // the catalog's client: one login, one cache
			d.TVDB = cat.Reg.P.TVDB
		}
	}
	return New(d)
}

// Outcome says what Process did with an event.
type Outcome int

const (
	// Ignored: an event or media type the processor does not act on.
	Ignored Outcome = iota
	// Matched: the item was found and recorded.
	Matched
	// Unmatched: no provider id led to a title (logged; not an error).
	Unmatched
)

// Result is what Process reports.
type Result struct {
	Label   string
	Outcome Outcome
}

// Options tune Process.
type Options struct {
	// WatchedAt dates the play; zero means now.
	WatchedAt time.Time
	// Import is a backfill: what is already watched is left alone.
	Import bool
}

// Percent is the share of the runtime that counts as watched for a user.
func (s *Service) Percent(u db.User) int {
	if u.WatchedPercent >= 1 && u.WatchedPercent <= 100 {
		return int(u.WatchedPercent)
	}
	if p := s.Config.JellyfinWatchedPercent; p >= 1 && p <= 100 {
		return p
	}
	return 75
}

// Process records one playback event for a user. Play marks the show or
// movie In progress (forward only); a Stop that counts as played records a
// watch. Unmatched items are logged, not errors.
func (s *Service) Process(ctx context.Context, u db.User, ev Event, opt Options) (Result, error) {
	res := Result{Label: ev.Label()}
	if ev.Kind != EventPlay && ev.Kind != EventStop {
		slog.DebugContext(ctx, "jellyfin: ignoring event", "event", ev.Kind)
		return res, nil
	}
	if ev.Type != "Episode" && ev.Type != "Movie" {
		slog.DebugContext(ctx, "jellyfin: ignoring media type", "type", ev.Type)
		return res, nil
	}
	if ev.Tmdb == "" && ev.Imdb == "" && ev.Tvdb == "" && ev.SeriesTvdb == "" {
		slog.WarnContext(ctx, "jellyfin: no provider id on item", "item", res.Label)
		res.Outcome = Unmatched
		return res, nil
	}
	at := opt.WatchedAt
	if at.IsZero() {
		at = s.Now().UTC()
	}
	processMu.Lock()
	defer processMu.Unlock()
	var ok bool
	var err error
	if ev.Type == "Episode" {
		ok, err = s.processEpisode(ctx, u, ev, at, opt)
	} else {
		ok, err = s.processMovie(ctx, u, ev, at, opt)
	}
	if err != nil {
		return res, err
	}
	if ok {
		res.Outcome = Matched
	} else {
		slog.WarnContext(ctx, "jellyfin: no matching title", "item", res.Label)
		res.Outcome = Unmatched
	}
	return res, nil
}

// episodeRef is an episode located on TVDB: the series, and the episode by
// its aired season and number (itemID is set when the catalog already knew
// the episode's TVDB id).
type episodeRef struct {
	showID          string
	season, episode int
	itemID          int64
}

// processEpisode matches the episode against TheTVDB, which Jellyfin numbers
// by: the TVDB episode id (found in the catalog, else asked from TVDB), the
// TVDB series id with Jellyfin's season and episode numbers, then the
// episode's IMDb id through TVDB. There is no TMDB step: shows are TVDB only.
func (s *Service) processEpisode(ctx context.Context, u db.User, ev Event, at time.Time, opt Options) (bool, error) {
	ref, ok, err := s.resolveEpisode(ctx, ev)
	if err != nil || !ok {
		return false, err
	}
	var item catalog.Item
	if ref.itemID != 0 {
		item, err = s.Catalog.GetItem(ctx, ref.itemID)
	} else {
		item, err = s.Catalog.EnsureItem(ctx, catalog.KindTV, catalog.SourceTVDB, ref.showID)
	}
	if err != nil {
		return false, fmt.Errorf("tvdb show %s: %w", ref.showID, err)
	}
	if ref.itemID == 0 && ev.Tvdb != "" {
		// A renumbered episode keeps its TVDB id: trust the id over the numbers.
		if err := s.Catalog.SyncSeasons(ctx, item); err == nil {
			if e, err := s.Q.FindEpisodeByExtID(ctx, ev.Tvdb); err == nil && e.ItemID == item.ID {
				ref.season, ref.episode = int(e.Season), int(e.Number)
			}
		}
	}
	s.link(ctx, u.ID, item.ID)
	if !ev.Played {
		return true, s.startShow(ctx, u.ID, item.ID, ref.season, at)
	}
	_, err = s.Library.WatchEpisode(ctx, u.ID, item.ID, ref.season, ref.episode, at, library.SourceJellyfin, library.WatchOptions{Import: opt.Import})
	return true, err
}

func (s *Service) resolveEpisode(ctx context.Context, ev Event) (episodeRef, bool, error) {
	if ev.Tvdb != "" {
		// The catalog knows the episodes of every show it has synced.
		if e, err := s.Q.FindEpisodeByExtID(ctx, ev.Tvdb); err == nil {
			return episodeRef{showID: strconv.FormatInt(e.ItemID, 10), season: int(e.Season), episode: int(e.Number), itemID: e.ItemID}, true, nil
		}
		ref, ok, err := s.viaTVDB(ctx, ev.Tvdb)
		if err != nil {
			// TVDB trouble must not block the series/IMDb fallbacks.
			slog.WarnContext(ctx, "jellyfin: TVDB lookup failed", "tvdb_episode", ev.Tvdb, "err", err)
		} else if ok {
			return ref, true, nil
		}
	}
	if ev.SeriesTvdb != "" {
		if ev.Season == nil || ev.Episode == nil {
			slog.WarnContext(ctx, "jellyfin: no season or episode number", "show", ev.SeriesTvdb)
		} else {
			return episodeRef{showID: ev.SeriesTvdb, season: *ev.Season, episode: *ev.Episode}, true, nil
		}
	}
	if ev.Imdb != "" && s.TVDB != nil {
		hit, err := s.TVDB.FindByRemoteID(ctx, ev.Imdb)
		if err != nil {
			slog.WarnContext(ctx, "jellyfin: TVDB lookup by IMDb id failed", "imdb", ev.Imdb, "err", err)
		} else if hit != nil && hit.Episode != nil {
			return episodeRef{showID: strconv.FormatInt(hit.SeriesID, 10), season: hit.Episode.Season, episode: hit.Episode.Number}, true, nil
		}
		slog.WarnContext(ctx, "jellyfin: no TVDB episode for IMDb id", "imdb", ev.Imdb)
	}
	return episodeRef{}, false, nil
}

// viaTVDB asks TVDB where an episode id belongs. A TVDB 404 (or no TVDB) is a miss.
func (s *Service) viaTVDB(ctx context.Context, episodeID string) (episodeRef, bool, error) {
	if s.TVDB == nil {
		return episodeRef{}, false, nil
	}
	ep, err := s.TVDB.Episode(ctx, episodeID)
	if err != nil || ep == nil {
		return episodeRef{}, false, err
	}
	seriesID := idString(ep["series_id"])
	season, episode := asInt(ep["season_number"]), asInt(ep["episode_number"])
	if seriesID == "" || season == nil || episode == nil {
		return episodeRef{}, false, nil
	}
	return episodeRef{showID: seriesID, season: *season, episode: *episode}, true, nil
}

// idString renders a JSON id (number or string) as a string; 0 and null are "".
func idString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x != 0 {
			return strconv.FormatInt(int64(x), 10)
		}
	case int:
		if x != 0 {
			return strconv.Itoa(x)
		}
	case int64:
		if x != 0 {
			return strconv.FormatInt(x, 10)
		}
	}
	return ""
}

// startShow is a Play (or an unfinished Stop): the show and the season move
// to In progress, forward only. It never reopens a finished or user-paused
// show (a finished play at a new season does that), and it does not create a
// show first seen mid-series: the first finished play does, so that earlier
// seasons can be assumed watched.
func (s *Service) startShow(ctx context.Context, userID, itemID int64, season int, at time.Time) error {
	entry, err := s.Library.CurrentEntry(ctx, userID, itemID)
	switch {
	case errors.Is(err, library.ErrNoEntry):
		if season > 1 {
			return nil
		}
	case err != nil:
		return err
	case entry.Status != library.Planning && entry.Status != library.InProgress:
		return nil
	}
	if err := s.Library.SetSeasonStatus(ctx, userID, itemID, season, library.InProgress, library.SourceJellyfin, at); err != nil {
		return err
	}
	return s.Library.SetStatus(ctx, userID, itemID, library.InProgress, library.SourceJellyfin, at)
}

// processMovie matches a movie by TMDB id, else by IMDb id (the catalog
// first, then TMDB find).
func (s *Service) processMovie(ctx context.Context, u db.User, ev Event, at time.Time, opt Options) (bool, error) {
	tmdbID := ev.Tmdb
	if tmdbID == "" && ev.Imdb != "" {
		if it, err := s.Catalog.FindByExternalID(ctx, "imdb", ev.Imdb, catalog.KindMovie, catalog.KindAnimeMovie); err == nil {
			tmdbID = it.ExternalID
		} else if s.TMDB != nil {
			resp, err := s.TMDB.Find(ctx, ev.Imdb, "imdb_id")
			if err != nil {
				return false, fmt.Errorf("tmdb find imdb_id: %w", err)
			}
			if list, _ := resp["movie_results"].([]any); len(list) > 0 {
				tmdbID = idString(asMap(list[0])["id"])
			}
		}
	}
	if tmdbID == "" {
		return false, nil
	}
	item, err := s.Catalog.EnsureItem(ctx, catalog.KindMovie, catalog.SourceTMDB, tmdbID)
	if err != nil {
		return false, fmt.Errorf("tmdb movie %s: %w", tmdbID, err)
	}
	s.link(ctx, u.ID, item.ID)
	if !ev.Played {
		return true, s.startMovie(ctx, u.ID, item.ID, at)
	}
	_, err = s.Library.WatchMovie(ctx, u.ID, item.ID, at, library.SourceJellyfin, library.WatchOptions{Import: opt.Import})
	return true, err
}

// startMovie marks a movie In progress when it is new or Planning; a
// finished movie is left alone (skimming it is not a rewatch).
func (s *Service) startMovie(ctx context.Context, userID, itemID int64, at time.Time) error {
	entry, err := s.Library.CurrentEntry(ctx, userID, itemID)
	switch {
	case errors.Is(err, library.ErrNoEntry):
		_, err = s.Library.Track(ctx, userID, itemID, library.InProgress, library.SourceJellyfin, at)
		return err
	case err != nil:
		return err
	case entry.Status == library.Planning:
		return s.Library.SetStatus(ctx, userID, itemID, library.InProgress, library.SourceJellyfin, at)
	}
	return nil
}

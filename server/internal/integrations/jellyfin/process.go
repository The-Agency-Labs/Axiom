// Package jellyfin is the Jellyfin integration: the playback webhook, the
// watch-history import and its 6-hourly sync job. It ports
// src/integrations/webhooks/{base,jellyfin,tv,movie}.py and
// src/integrations/imports/jellyfin.py.
//
// Every play is recorded with source library.SourceJellyfin, so the library
// rules apply: nothing is ever undone or lowered, imports are additive, and a
// show first seen at season N assumes seasons 1..N-1 watched. Anime are
// TMDB shows (ANIME_AS_TV), so the AniDB/MAL routing helpers are not ported,
// and MarkPlayed/MarkUnplayed events are ignored (they could only delete).
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

	"github.com/the-agency-labs/encompass/server/internal/cache"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers"
	"github.com/the-agency-labs/encompass/server/internal/providers/tvdb"
)

// processMu serializes event processing across Service instances (the
// webhook and the sync job each hold one), so a Play and a Stop arriving
// together cannot both create the same entry.
var processMu sync.Mutex

// TVDB is the slice of the TVDB client the matcher uses.
type TVDB interface {
	Episode(ctx context.Context, episodeID string) (providers.Metadata, error)
	SeriesTMDBID(ctx context.Context, seriesID string) (string, error)
}

// Deps are what a Service needs.
type Deps struct {
	Conn    *sql.DB
	Q       *db.Queries
	Catalog *catalog.Catalog
	Library *library.Service
	TMDB    catalog.TMDB // Find
	TVDB    TVDB         // nil: TVDB matching is skipped
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

// NewService builds a Service on the app's shared parts, with the real TVDB
// client (the token lives in memory per client, so a second one just logs in again).
func NewService(conn *sql.DB, q *db.Queries, cfg config.Config, cat *catalog.Catalog, lib *library.Service, c *cache.Cache, m *jobs.Manager) *Service {
	d := Deps{Conn: conn, Q: q, Catalog: cat, Library: lib, Jobs: m, Config: cfg,
		TVDB: tvdb.New(providers.NewClient(c), cfg)}
	if cat != nil && cat.Reg != nil {
		d.TMDB = cat.Reg.P.TMDB
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
	if ev.Tmdb == "" && ev.Imdb == "" && ev.Tvdb == "" && ev.SeriesTmdb == "" {
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

// episodeRef is an episode located on TMDB.
type episodeRef struct {
	showID          string
	season, episode int
}

// processEpisode matches the episode like Django's TV mixin: TVDB episode id
// (TMDB find, then the TVDB series' TMDB id with TVDB numbering), then the
// IMDb id via TMDB find, then the series TMDB id with Jellyfin's numbering.
func (s *Service) processEpisode(ctx context.Context, u db.User, ev Event, at time.Time, opt Options) (bool, error) {
	ref, ok, err := s.resolveEpisode(ctx, ev)
	if err != nil || !ok {
		return false, err
	}
	item, err := s.Catalog.EnsureItem(ctx, catalog.KindTV, catalog.SourceTMDB, ref.showID)
	if err != nil {
		return false, fmt.Errorf("tmdb show %s: %w", ref.showID, err)
	}
	if !ev.Played {
		return true, s.startShow(ctx, u.ID, item.ID, ref.season, at)
	}
	_, err = s.Library.WatchEpisode(ctx, u.ID, item.ID, ref.season, ref.episode, at, library.SourceJellyfin, library.WatchOptions{Import: opt.Import})
	return true, err
}

func (s *Service) resolveEpisode(ctx context.Context, ev Event) (episodeRef, bool, error) {
	if ev.Tvdb != "" {
		ref, ok, err := s.viaTVDB(ctx, ev.Tvdb)
		if err != nil {
			// TVDB trouble must not block the IMDb/TMDB fallbacks.
			slog.WarnContext(ctx, "jellyfin: TVDB lookup failed", "tvdb_episode", ev.Tvdb, "err", err)
		} else if ok {
			return ref, true, nil
		}
	}
	if ev.Imdb != "" {
		ref, ok, err := s.findEpisode(ctx, ev.Imdb, "imdb_id")
		if err != nil {
			return ref, false, err
		}
		if ok {
			return ref, true, nil
		}
		slog.WarnContext(ctx, "jellyfin: no TMDB episode for IMDb id", "imdb", ev.Imdb)
	}
	if ev.SeriesTmdb != "" {
		if ev.Season == nil || ev.Episode == nil {
			slog.WarnContext(ctx, "jellyfin: no season or episode number", "show", ev.SeriesTmdb)
			return episodeRef{}, false, nil
		}
		return episodeRef{ev.SeriesTmdb, *ev.Season, *ev.Episode}, true, nil
	}
	return episodeRef{}, false, nil
}

// viaTVDB resolves a TVDB episode id. A TVDB 404 (or no TVDB) is a miss.
func (s *Service) viaTVDB(ctx context.Context, episodeID string) (episodeRef, bool, error) {
	if s.TVDB == nil {
		return episodeRef{}, false, nil
	}
	ep, err := s.TVDB.Episode(ctx, episodeID)
	if err != nil || ep == nil {
		return episodeRef{}, false, err
	}
	if ref, ok, err := s.findEpisode(ctx, episodeID, "tvdb_id"); err != nil || ok {
		return ref, ok, err
	}
	seriesID := idString(ep["series_id"])
	season, episode := asInt(ep["season_number"]), asInt(ep["episode_number"])
	if seriesID == "" || season == nil || episode == nil {
		return episodeRef{}, false, nil
	}
	// A show already in the catalog knows its TVDB series id; otherwise ask TVDB.
	showID := ""
	if it, err := s.Catalog.FindByExternalID(ctx, "tvdb", seriesID, catalog.KindTV, catalog.KindAnime); err == nil && it.Source == catalog.SourceTMDB {
		showID = it.ExternalID
	} else if showID, err = s.TVDB.SeriesTMDBID(ctx, seriesID); err != nil {
		return episodeRef{}, false, err
	}
	if showID == "" {
		slog.WarnContext(ctx, "jellyfin: no TMDB id for TVDB series", "tvdb_episode", episodeID)
		return episodeRef{}, false, nil
	}
	return episodeRef{showID, *season, *episode}, true, nil
}

// findEpisode asks TMDB which episode an external id belongs to.
func (s *Service) findEpisode(ctx context.Context, id, source string) (episodeRef, bool, error) {
	if s.TMDB == nil {
		return episodeRef{}, false, nil
	}
	resp, err := s.TMDB.Find(ctx, id, source)
	if err != nil {
		return episodeRef{}, false, fmt.Errorf("tmdb find %s: %w", source, err)
	}
	list, _ := resp["tv_episode_results"].([]any)
	if len(list) == 0 {
		return episodeRef{}, false, nil
	}
	r := asMap(list[0])
	show, season, episode := idString(r["show_id"]), asInt(r["season_number"]), asInt(r["episode_number"])
	if show == "" || season == nil || episode == nil {
		return episodeRef{}, false, nil
	}
	return episodeRef{show, *season, *episode}, true, nil
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

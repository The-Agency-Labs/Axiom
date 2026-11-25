package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// libraryShowItem finds or creates the catalog item of a TMDB show (tv or
// anime) and makes sure its seasons and episodes are synced. It works for
// shows the user doesn't track yet, so a show can be browsed from search.
func (s *Server) libraryShowItem(ctx context.Context, source, mediaID string) (db.Item, error) {
	if !libraryKnownSource(source) {
		return db.Item{}, Errorf(http.StatusBadRequest, "Unknown source")
	}
	item, err := s.Catalog.EnsureItem(ctx, catalog.KindTV, source, mediaID)
	if err != nil {
		return db.Item{}, libraryError(err, source)
	}
	if !catalog.HasEpisodes(item) {
		return db.Item{}, Errorf(http.StatusBadRequest, "Only TMDB shows have seasons and episodes")
	}
	if err := s.Catalog.SyncSeasons(ctx, item); err != nil {
		if !item.SeasonsSyncedAt.Valid {
			return db.Item{}, libraryError(err, source)
		}
		slog.WarnContext(ctx, "library: season sync failed", "item", item.ID, "err", err)
	}
	return s.Catalog.GetItem(ctx, item.ID)
}

func libraryPercent(p *float64) any { return libraryFloatPtr(p) }

func librarySeasonEntryJSON(e *library.SeasonEntry, showEntryID int64) any {
	if e == nil {
		return nil
	}
	// Seasons have no entry of their own any more; id is the show's entry.
	return map[string]any{"id": showEntryID, "status": e.Status, "score": libraryFloatPtr(e.Score)}
}

func (s *Server) libraryShowJSON(ctx context.Context, userID int64, item db.Item) (map[string]any, error) {
	v, err := s.Library.GetShowView(ctx, userID, item)
	if err != nil {
		return nil, err
	}
	var entry any
	var entryID int64
	if v.Entry != nil {
		entry = libraryEntryJSON(*v.Entry, item)
		entryID = v.Entry.ID
	}
	seasons := make([]map[string]any, 0, len(v.Seasons))
	for _, ss := range v.Seasons {
		seasons = append(seasons, map[string]any{
			"season_number": ss.Number, "title": ss.Title, "is_special": ss.Number == 0,
			"image": libraryStrOrNil(ss.Image), "air_date": libraryStrOrNil(ss.AirDate),
			"episode_count": ss.EpisodeCount, "watched": ss.Watched, "percent": libraryPercent(ss.Percent),
			"entry": librarySeasonEntryJSON(ss.Entry, entryID),
		})
	}
	var status any
	if details, ok := library.ItemMeta(item)["details"].(map[string]any); ok {
		status = details["status"]
	}
	c := v.Completion
	return map[string]any{
		"source": item.Source, "media_id": item.ExternalID, "title": item.Title, "image": item.Image,
		"entry": entry,
		"completion": map[string]any{
			"watched": c.Watched, "total": c.Total, "percent": libraryPercent(c.Percent),
			"seasons_completed": c.SeasonsCompleted, "seasons": c.Seasons, "specials_watched": c.SpecialsWatched,
		},
		"status":  status,
		"seasons": seasons,
	}, nil
}

// librarySeasonJSON is api.shows._season_payload.
func (s *Server) librarySeasonJSON(ctx context.Context, userID int64, item db.Item, season int) (map[string]any, error) {
	v, err := s.Library.GetSeasonView(ctx, userID, item, season)
	if err != nil {
		return nil, err
	}
	var synopsis any
	if s.Catalog.Reg.P.TMDB != nil {
		if meta, err := s.Catalog.Reg.P.TMDB.Season(ctx, item.ExternalID, season); err == nil {
			if txt, ok := meta["synopsis"].(string); ok && txt != "" {
				synopsis = txt
			}
		}
	}
	var entry any
	if v.Entry != nil {
		progress, latest, latestAt := 0, "", ""
		for _, ep := range v.Episodes {
			if ep.Watched && (ep.WatchedAt > latestAt || (ep.WatchedAt == latestAt && ep.Number > progress)) {
				progress, latestAt = ep.Number, ep.WatchedAt
			}
			latest = max(latest, ep.WatchedAt)
		}
		var created any
		if e, err := s.Library.GetEntry(ctx, userID, v.EntryID); err == nil {
			created = e.CreatedAt
		}
		entry = map[string]any{
			"id": v.EntryID, "media_type": "season", "item": libraryItemJSON(item, &season),
			"status": v.Entry.Status, "score": libraryFloatPtr(v.Entry.Score), "progress": progress,
			"progressed_at": libraryStrOrNil(latest), "start_date": nil, "end_date": nil,
			"notes": v.Entry.Notes, "created_at": created,
		}
	}
	eps := make([]map[string]any, 0, len(v.Episodes))
	for _, ep := range v.Episodes {
		eps = append(eps, map[string]any{
			"episode_number": ep.Number, "title": ep.Title, "overview": ep.Overview,
			"air_date": libraryStrOrNil(ep.AirDate), "aired": ep.Aired, "runtime": libraryIntPtr(ep.Runtime),
			"image": libraryStrOrNil(ep.Image), "kind": ep.Kind, "score": libraryFloatPtr(ep.Score),
			"watched": ep.Watched, "watch_count": ep.WatchCount, "watched_at": libraryStrOrNil(ep.WatchedAt),
		})
	}
	return map[string]any{
		"source": item.Source, "media_id": item.ExternalID, "season_number": season,
		"title": v.Title, "image": libraryStrOrNil(v.Image), "synopsis": synopsis,
		"entry": entry, "watched": v.Watched, "total": v.Total, "runtime_minutes": v.RuntimeMinutes,
		"episodes": eps,
	}, nil
}

type libraryEpisodesIn struct {
	Episodes []int   `json:"episodes"`
	UpTo     *int    `json:"up_to"`
	All      bool    `json:"all"`
	Watched  *bool   `json:"watched"`
	Rewatch  bool    `json:"rewatch"`
	Date     *string `json:"date"`
}

func (s *Server) libraryRegisterShows(mux *http.ServeMux) {
	// GET /shows/{source}/{media_id} (api/shows.py: show)
	mux.Handle("GET /api/v1/shows/{source}/{media_id}", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		source := r.PathValue("source")
		item, err := s.libraryShowItem(r.Context(), source, r.PathValue("media_id"))
		if err != nil {
			return nil, err
		}
		out, err := s.libraryShowJSON(r.Context(), auth.User(r.Context()).ID, item)
		return out, libraryError(err, source)
	})))

	// GET /shows/{source}/{media_id}/seasons/{season_number} (api/shows.py: season)
	mux.Handle("GET /api/v1/shows/{source}/{media_id}/seasons/{season_number}", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		source := r.PathValue("source")
		n, err := libraryPathInt(r, "season_number")
		if err != nil {
			return nil, err
		}
		item, err := s.libraryShowItem(r.Context(), source, r.PathValue("media_id"))
		if err != nil {
			return nil, err
		}
		out, err := s.librarySeasonJSON(r.Context(), auth.User(r.Context()).ID, item, int(n))
		return out, libraryError(err, source)
	})))

	// POST /shows/{source}/{media_id}/seasons/{season_number}/episodes (api/shows.py: mark_episodes)
	mux.Handle("POST /api/v1/shows/{source}/{media_id}/seasons/{season_number}/episodes", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		ctx := r.Context()
		source := r.PathValue("source")
		n64, err := libraryPathInt(r, "season_number")
		if err != nil {
			return nil, err
		}
		season := int(n64)
		in := libraryEpisodesIn{}
		if err := DecodeJSON(r, &in); err != nil {
			return nil, err
		}
		watched := in.Watched == nil || *in.Watched
		at := time.Time{}
		if in.Date != nil {
			if at, err = libraryParseTime(*in.Date); err != nil {
				return nil, Errorf(http.StatusUnprocessableEntity, err.Error())
			}
		}
		item, err := s.libraryShowItem(ctx, source, r.PathValue("media_id"))
		if err != nil {
			return nil, err
		}
		userID := auth.User(ctx).ID
		view, err := s.Library.GetSeasonView(ctx, userID, item, season)
		if err != nil {
			return nil, libraryError(err, source)
		}
		var available []int
		isWatched := map[int]bool{}
		for _, ep := range view.Episodes {
			available = append(available, ep.Number)
			isWatched[ep.Number] = ep.Watched
		}
		var numbers []int
		switch {
		case in.All:
			numbers = available
		case in.UpTo != nil:
			for _, n := range available {
				if n <= *in.UpTo {
					numbers = append(numbers, n)
				}
			}
		default:
			for _, n := range in.Episodes {
				if _, ok := isWatched[n]; ok {
					numbers = append(numbers, n)
				}
			}
		}
		if len(numbers) == 0 {
			return nil, Errorf(http.StatusBadRequest, "No matching episodes")
		}
		if watched {
			if at.IsZero() {
				at = time.Now().UTC().Truncate(time.Minute)
				if s.Library.Now != nil {
					at = s.Library.Now().UTC().Truncate(time.Minute)
				}
			}
			sort.Ints(numbers)
			for _, n := range numbers {
				if isWatched[n] && !in.Rewatch {
					continue
				}
				// Manual plays: the app may do what the Django UI did. Import skips an
				// episode already watched; rewatch records another play.
				if _, err := s.Library.WatchEpisode(ctx, userID, item.ID, season, n, at, library.SourceManual,
					library.WatchOptions{Import: !in.Rewatch}); err != nil {
					return nil, libraryError(err, source)
				}
			}
		} else {
			sort.Sort(sort.Reverse(sort.IntSlice(numbers)))
			for _, n := range numbers {
				if !isWatched[n] {
					continue
				}
				if err := s.Library.UnwatchEpisode(ctx, userID, item.ID, season, n); err != nil && !errors.Is(err, library.ErrNoEntry) {
					return nil, libraryError(err, source)
				}
			}
		}
		out, err := s.librarySeasonJSON(ctx, userID, item, season)
		return out, libraryError(err, source)
	})))
}

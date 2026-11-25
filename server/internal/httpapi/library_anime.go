package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// libraryAnimeCheck is api.anime._check: only MAL/AniList anime are count-based.
func libraryAnimeCheck(source string) error {
	if source != catalog.SourceMAL && source != catalog.SourceAniList {
		return Errorf(http.StatusBadRequest, "Anime episodes need a MyAnimeList or AniList id")
	}
	return nil
}

// libraryAnimeItem finds or creates the item of a count-based anime.
func (s *Server) libraryAnimeItem(ctx context.Context, source, mediaID string) (db.Item, error) {
	item, err := s.Catalog.EnsureItem(ctx, catalog.KindAnime, source, mediaID)
	if err != nil {
		return db.Item{}, libraryError(err, source)
	}
	if catalog.HasEpisodes(item) {
		return db.Item{}, Errorf(http.StatusBadRequest, "This anime is tracked with TMDB seasons; use /shows/tmdb/{id}")
	}
	return item, nil
}

// libraryAnimePayload is api.anime._payload.
func (s *Server) libraryAnimePayload(ctx context.Context, userID int64, item db.Item) (map[string]any, error) {
	eps, info, err := s.Catalog.AnimeEpisodes(ctx, item)
	if err != nil {
		return nil, libraryError(err, item.Source)
	}
	var entryJSON any
	progress := 0
	dates := map[int]string{}
	if e, err := s.Library.CurrentEntry(ctx, userID, item.ID); err == nil {
		entryJSON = libraryEntryJSON(e, item)
		progress = int(e.Progress)
		if dates, err = s.Library.ProgressDates(ctx, e.ID); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, library.ErrNoEntry) {
		return nil, err
	}
	today := s.Library.Today()
	out := make([]map[string]any, 0, len(eps))
	runtime := 0
	for _, ep := range eps {
		watched := ep.Number <= progress
		count := 0
		var watchedAt any
		if watched {
			count = 1
			watchedAt = libraryStrOrNil(dates[ep.Number])
		}
		if ep.Runtime != nil {
			runtime += *ep.Runtime
		}
		out = append(out, map[string]any{
			"episode_number": ep.Number, "title": ep.Title, "overview": ep.Overview,
			"air_date": libraryStrOrNil(ep.AirDate), "aired": ep.AirDate == "" || ep.AirDate <= today,
			"runtime": libraryIntPtr(ep.Runtime), "image": libraryStrOrNil(ep.Image), "kind": ep.Kind,
			"score": libraryFloatPtr(ep.Score), "watched": watched, "watch_count": count, "watched_at": watchedAt,
		})
	}
	total := int(item.Total.Int64)
	if total == 0 {
		total = len(eps)
	}
	var tmdb, anilistID any
	if info.TMDB != nil {
		tmdb = map[string]any{
			"tmdb_id": info.TMDB.TMDBID, "season_number": info.TMDB.SeasonNumber, "episode_offset": info.TMDB.EpisodeOffset,
		}
	}
	if info.AniListID != 0 {
		anilistID = info.AniListID
	}
	providerNames := info.Providers
	if providerNames == nil {
		providerNames = []string{}
	}
	return map[string]any{
		"source": item.Source, "media_id": item.ExternalID, "title": item.Title, "entry": entryJSON,
		"progress": progress, "total": total, "runtime_minutes": runtime, "episodes": out,
		"providers": providerNames, "tmdb": tmdb, "anilist_id": anilistID,
	}, nil
}

func (s *Server) libraryRegisterAnime(mux *http.ServeMux) {
	// GET /anime/{source}/{media_id}/episodes (api/anime.py: episodes)
	mux.Handle("GET /api/v1/anime/{source}/{media_id}/episodes", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		source := r.PathValue("source")
		if err := libraryAnimeCheck(source); err != nil {
			return nil, err
		}
		item, err := s.libraryAnimeItem(r.Context(), source, r.PathValue("media_id"))
		if err != nil {
			return nil, err
		}
		out, err := s.libraryAnimePayload(r.Context(), auth.User(r.Context()).ID, item)
		return out, err
	})))

	// POST /anime/{source}/{media_id}/episodes (api/anime.py: mark_episode)
	mux.Handle("POST /api/v1/anime/{source}/{media_id}/episodes", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		ctx := r.Context()
		source := r.PathValue("source")
		if err := libraryAnimeCheck(source); err != nil {
			return nil, err
		}
		var in struct {
			Episode *int  `json:"episode"`
			Watched *bool `json:"watched"`
		}
		if err := DecodeJSON(r, &in); err != nil {
			return nil, err
		}
		if in.Episode == nil {
			return nil, Errorf(http.StatusUnprocessableEntity, "episode is required")
		}
		if *in.Episode < 1 {
			return nil, Errorf(http.StatusBadRequest, "Episodes start at 1")
		}
		watched := in.Watched == nil || *in.Watched
		item, err := s.libraryAnimeItem(ctx, source, r.PathValue("media_id"))
		if err != nil {
			return nil, err
		}
		userID := auth.User(ctx).ID
		_, err = s.Library.CurrentEntry(ctx, userID, item.ID)
		switch {
		case errors.Is(err, library.ErrNoEntry) && !watched:
			// Nothing tracked and nothing to mark: just show the episodes.
		case err != nil && !errors.Is(err, library.ErrNoEntry):
			return nil, err
		default:
			target := *in.Episode
			if !watched {
				target--
			}
			// Manual: progress may move down as well as up (Python set it to N or N-1).
			if err := s.Library.SetProgress(ctx, userID, item.ID, target, library.SourceManual, time.Time{}); err != nil {
				return nil, libraryError(err, source)
			}
		}
		return s.libraryAnimePayload(ctx, userID, item)
	})))
}

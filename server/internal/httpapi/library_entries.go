package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// libraryMetadata fetches provider metadata for an item or a bare (kind,
// source, id) triple, with the optional season of a show.
func (s *Server) libraryMetadata(ctx context.Context, kind, source, id string, season *int) (catalog.Metadata, error) {
	if season != nil && catalog.IsShow(kind) && source == catalog.SourceTMDB && s.Catalog.Reg.P.TMDB != nil {
		return s.Catalog.Reg.P.TMDB.TVWithSeasons(ctx, id, []int{*season})
	}
	return s.Catalog.Reg.Metadata(ctx, kind, source, id)
}

// libraryEntryOf loads an entry of the user and checks it is of the media type in the URL.
func (s *Server) libraryEntryOf(ctx context.Context, userID int64, kind string, id int64) (db.Entry, db.Item, error) {
	row, err := s.Queries.GetEntryWithItem(ctx, db.GetEntryWithItemParams{ID: id, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row.Item.Kind != kind) {
		return db.Entry{}, db.Item{}, Errorf(http.StatusNotFound, "Not Found")
	}
	if err != nil {
		return db.Entry{}, db.Item{}, err
	}
	return row.Entry, row.Item, nil
}

func (s *Server) libraryRegisterEntries(mux *http.ServeMux) {
	// GET /search (api/library.py: search)
	mux.Handle("GET /api/v1/search", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		q := r.URL.Query()
		kind := q.Get("media_type")
		if !libraryValidKind(kind) {
			return nil, Errorf(http.StatusUnprocessableEntity, "media_type must be one of the library media types")
		}
		if !q.Has("q") {
			return nil, Errorf(http.StatusUnprocessableEntity, "q is required")
		}
		page, err := libraryQueryInt(r, "page", 1)
		if err != nil {
			return nil, err
		}
		res, err := s.Catalog.Reg.Search(r.Context(), kind, q.Get("source"), q.Get("q"), page, true)
		if err != nil {
			if errors.Is(err, catalog.ErrUnsupported) {
				return nil, Errorf(http.StatusBadRequest, err.Error())
			}
			var apiErr *Error
			if errors.As(err, &apiErr) {
				return nil, err
			}
			return nil, libraryError(err, "Search provider")
		}
		return res, nil
	})))

	// GET /metadata/{media_type}/{source}/{media_id} (api/library.py: metadata)
	mux.Handle("GET /api/v1/metadata/{media_type}/{source}/{media_id}", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		kind, err := libraryPathKind(r)
		if err != nil {
			return nil, err
		}
		source := r.PathValue("source")
		if !libraryKnownSource(source) {
			return nil, Errorf(http.StatusBadRequest, "Unknown source")
		}
		var season *int
		if r.URL.Query().Has("season_number") {
			n, err := libraryQueryInt(r, "season_number", 0)
			if err != nil {
				return nil, err
			}
			season = &n
		}
		meta, err := s.libraryMetadata(r.Context(), kind, source, r.PathValue("media_id"), season)
		if err != nil {
			return nil, libraryError(err, source)
		}
		return meta, nil
	})))

	// GET /library (api/library.py: list_library)
	mux.Handle("GET /api/v1/library", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		q := r.URL.Query()
		f := library.LibraryFilter{Status: q.Get("status"), Search: q.Get("search")}
		if kind := q.Get("media_type"); kind != "" {
			if !libraryValidKind(kind) {
				return nil, Errorf(http.StatusUnprocessableEntity, "media_type must be one of the library media types")
			}
			f.Kinds = []string{kind}
		}
		var err error
		if q.Has("year") {
			year, err := libraryQueryInt(r, "year", 0)
			if err != nil {
				return nil, err
			}
			f.Year = &year
		}
		if f.Limit, err = libraryQueryInt(r, "limit", 200); err != nil {
			return nil, err
		}
		if f.Offset, err = libraryQueryInt(r, "offset", 0); err != nil {
			return nil, err
		}
		empty := f.Limit <= 0 // Python slices results[offset:offset+limit]
		f.Limit = max(f.Limit, 1)
		page, err := s.Library.ListLibrary(r.Context(), auth.User(r.Context()).ID, f)
		if err != nil {
			return nil, err
		}
		results := make([]map[string]any, 0, len(page.Results))
		if !empty {
			for _, row := range page.Results {
				data := libraryEntryJSON(row.Entry, row.Item)
				if catalog.IsShow(row.Item.Kind) && row.Item.Source == catalog.SourceTMDB {
					data["max_progress"] = libraryIntPtr(row.MaxProgress)
				}
				results = append(results, data)
			}
		}
		years := page.Years
		if years == nil {
			years = []int{}
		}
		return map[string]any{"count": page.Count, "years": years, "results": results}, nil
	})))

	// GET /library/find (api/library.py: find_entry)
	mux.Handle("GET /api/v1/library/find", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		q := r.URL.Query()
		kind := q.Get("media_type")
		if !libraryValidKind(kind) {
			return nil, Errorf(http.StatusUnprocessableEntity, "media_type must be one of the library media types")
		}
		if !q.Has("source") || !q.Has("media_id") {
			return nil, Errorf(http.StatusUnprocessableEntity, "source and media_id are required")
		}
		userID := auth.User(r.Context()).ID
		entry, _, ok, err := s.Library.FindEntry(r.Context(), userID, s.Catalog, q.Get("source"), q.Get("media_id"), kind)
		if err != nil {
			return nil, err
		}
		if ok && q.Has("season_number") {
			// Seasons are part of the show's entry now: it answers when that season is tracked.
			n, err := libraryQueryInt(r, "season_number", 0)
			if err != nil {
				return nil, err
			}
			if _, err := s.Queries.GetEntrySeason(r.Context(), db.GetEntrySeasonParams{EntryID: entry.ID, Season: int64(n)}); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				ok = false
			}
		}
		if !ok {
			return map[string]any{"id": nil}, nil
		}
		return map[string]any{"id": entry.ID}, nil
	})))

	// GET /library/{media_type}/{id} (api/library.py: get_entry)
	mux.Handle("GET /api/v1/library/{media_type}/{id}", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		kind, err := libraryPathKind(r)
		if err != nil {
			return nil, err
		}
		id, err := libraryPathInt(r, "id")
		if err != nil {
			return nil, err
		}
		includeMeta := true
		if v := r.URL.Query().Get("include_metadata"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, Errorf(http.StatusUnprocessableEntity, "include_metadata must be a boolean")
			}
			includeMeta = b
		}
		userID := auth.User(r.Context()).ID
		if _, _, err := s.libraryEntryOf(r.Context(), userID, kind, id); err != nil {
			return nil, err
		}
		d, err := s.Library.GetEntryDetail(r.Context(), userID, id)
		if err != nil {
			return nil, libraryError(err, "")
		}
		data := libraryEntryJSON(d.Entry, d.Item)
		data["history"] = libraryHistoryJSON(d.Item, d.History)
		if catalog.HasEpisodes(d.Item) {
			data["seasons"] = libraryDetailSeasons(d)
		}
		if includeMeta {
			meta, err := s.Catalog.Reg.Metadata(r.Context(), d.Item.Kind, d.Item.Source, d.Item.ExternalID)
			if err != nil {
				data["metadata"] = nil
			} else {
				data["metadata"] = meta
			}
		}
		return data, nil
	})))

	// POST /library (api/library.py: save_entry)
	mux.Handle("POST /api/v1/library", auth.RequireWrite(JSON(s.librarySave)))

	// DELETE /library/{media_type}/{id} (api/library.py: delete_entry)
	mux.Handle("DELETE /api/v1/library/{media_type}/{id}", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		kind, err := libraryPathKind(r)
		if err != nil {
			return nil, err
		}
		id, err := libraryPathInt(r, "id")
		if err != nil {
			return nil, err
		}
		userID := auth.User(r.Context()).ID
		if _, _, err := s.libraryEntryOf(r.Context(), userID, kind, id); err != nil {
			return nil, err
		}
		if err := s.Library.Delete(r.Context(), userID, id); err != nil {
			return nil, libraryError(err, "")
		}
		return map[string]bool{"deleted": true}, nil
	})))

	// POST /library/{media_type}/{id}/step (api/library.py: step_progress)
	mux.Handle("POST /api/v1/library/{media_type}/{id}/step", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		kind, err := libraryPathKind(r)
		if err != nil {
			return nil, err
		}
		id, err := libraryPathInt(r, "id")
		if err != nil {
			return nil, err
		}
		in := struct {
			Direction string `json:"direction"`
		}{Direction: "up"}
		if err := libraryDecodeOptional(r, &in); err != nil {
			return nil, err
		}
		if in.Direction != "up" && in.Direction != "down" {
			return nil, Errorf(http.StatusUnprocessableEntity, "direction must be 'up' or 'down'")
		}
		userID := auth.User(r.Context()).ID
		entry, item, err := s.libraryEntryOf(r.Context(), userID, kind, id)
		if err != nil {
			return nil, err
		}
		if catalog.HasEpisodes(item) || catalog.IsMovie(item.Kind) {
			return nil, Errorf(http.StatusBadRequest, "Step a season, not a show or movie")
		}
		if err := s.Library.Step(r.Context(), userID, item.ID, in.Direction == "up"); err != nil {
			return nil, libraryError(err, item.Source)
		}
		entry, err = s.Library.GetEntry(r.Context(), userID, entry.ID)
		if err != nil {
			return nil, libraryError(err, "")
		}
		return libraryEntryJSON(entry, item), nil
	})))

	// POST /items/{item_id}/kind: move a TMDB title between tv/anime or movie/anime_movie.
	mux.Handle("POST /api/v1/items/{item_id}/kind", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		id, err := libraryPathInt(r, "item_id")
		if err != nil {
			return nil, err
		}
		var in struct {
			Kind string `json:"kind"`
		}
		if err := DecodeJSON(r, &in); err != nil {
			return nil, err
		}
		switch in.Kind {
		case catalog.KindTV, catalog.KindAnime, catalog.KindMovie, catalog.KindAnimeMovie:
		default:
			return nil, Errorf(http.StatusUnprocessableEntity, "kind must be one of tv, anime, movie, anime_movie")
		}
		item, err := s.Catalog.Reclassify(r.Context(), id, in.Kind)
		if err != nil {
			if errors.Is(err, catalog.ErrUnsupported) {
				return nil, Errorf(http.StatusUnprocessableEntity, err.Error())
			}
			return nil, libraryError(err, item.Source)
		}
		out := libraryItemJSON(item, nil)
		out["is_anime"] = libraryIsAnime(item.Kind)
		return out, nil
	})))
}

// libraryDetailSeasons lists the seasons of a tracked show with the episodes watched.
func libraryDetailSeasons(d library.EntryDetail) []map[string]any {
	out := make([]map[string]any, 0, len(d.Seasons))
	for _, sd := range d.Seasons {
		n := sd.Number
		var progressedAt any
		latest := ""
		eps := make([]map[string]any, 0, len(sd.Episodes))
		for _, ep := range sd.Episodes {
			title := ep.Title
			if title == "" {
				title = "Episode " + strconv.Itoa(ep.Number)
			}
			eps = append(eps, map[string]any{
				"episode_number": ep.Number, "title": title, "watched_at": ep.WatchedAt, "notes": ep.Notes,
			})
			latest = max(latest, ep.WatchedAt)
		}
		if latest != "" {
			progressedAt = latest
		}
		out = append(out, map[string]any{
			"id": nil, "media_type": "season", "item": libraryItemJSON(d.Item, &n), "season_number": n,
			"status": sd.Status, "score": libraryFloatPtr(sd.Score), "progress": sd.Progress,
			"progressed_at": progressedAt, "start_date": nil, "end_date": nil, "notes": sd.Notes,
			"created_at": d.Entry.CreatedAt, "max_progress": sd.EpisodeCount, "episodes": eps,
		})
	}
	return out
}

type librarySaveIn struct {
	MediaType    string   `json:"media_type"`
	Source       string   `json:"source"`
	MediaID      string   `json:"media_id"`
	SeasonNumber *int     `json:"season_number"`
	InstanceID   *int64   `json:"instance_id"`
	Status       *string  `json:"status"`
	Progress     *int     `json:"progress"`
	Score        *float64 `json:"score"`
	StartDate    *string  `json:"start_date"`
	EndDate      *string  `json:"end_date"`
	Notes        *string  `json:"notes"`
}

func libraryOptionalTime(v *string) (library.Optional[time.Time], error) {
	if v == nil {
		return library.Optional[time.Time]{}, nil
	}
	if *v == "" {
		return library.Clear[time.Time](), nil
	}
	t, err := libraryParseTime(*v)
	if err != nil {
		return library.Optional[time.Time]{}, Errorf(http.StatusUnprocessableEntity, err.Error())
	}
	return library.Some(t), nil
}

// librarySave tracks something new, or updates an entry when instance_id is given.
func (s *Server) librarySave(w http.ResponseWriter, r *http.Request) (any, error) {
	var in librarySaveIn
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if !libraryValidKind(in.MediaType) {
		return nil, Errorf(http.StatusUnprocessableEntity, "media_type must be one of the library media types")
	}
	if in.InstanceID == nil && (in.Source == "" || in.MediaID == "") {
		return nil, Errorf(http.StatusUnprocessableEntity, "source and media_id are required")
	}
	status := ""
	if in.Status != nil {
		status = *in.Status
		switch status {
		case library.Planning, library.InProgress, library.Paused, library.Dropped, library.Completed:
		default:
			return nil, Errorf(http.StatusUnprocessableEntity, "Unknown status "+strconv.Quote(status))
		}
	}
	if in.Score != nil && (*in.Score < 0 || *in.Score > 10) {
		return nil, Errorf(http.StatusUnprocessableEntity, "score must be between 0 and 10")
	}
	if in.Progress != nil && *in.Progress < 0 {
		return nil, Errorf(http.StatusUnprocessableEntity, "progress must not be negative")
	}
	started, err := libraryOptionalTime(in.StartDate)
	if err != nil {
		return nil, err
	}
	finished, err := libraryOptionalTime(in.EndDate)
	if err != nil {
		return nil, err
	}

	ctx := r.Context()
	userID := auth.User(ctx).ID
	var item db.Item
	existing := false
	if in.InstanceID != nil {
		_, it, err := s.libraryEntryOf(ctx, userID, in.MediaType, *in.InstanceID)
		if err != nil {
			return nil, err
		}
		item, existing = it, true
	} else {
		if !libraryKnownSource(in.Source) {
			return nil, Errorf(http.StatusBadRequest, "Unknown source")
		}
		it, err := s.Catalog.EnsureItem(ctx, in.MediaType, in.Source, in.MediaID)
		if err != nil {
			return nil, libraryError(err, in.Source)
		}
		item = it
	}
	if _, err := s.Library.CurrentEntry(ctx, userID, item.ID); err == nil {
		existing = true
	}

	manual := library.SourceManual
	bySeason := in.SeasonNumber != nil && catalog.HasEpisodes(item)
	switch {
	case bySeason:
		n := *in.SeasonNumber
		if status != "" {
			err = s.Library.SetSeasonStatus(ctx, userID, item.ID, n, status, manual, time.Time{})
		} else if !existing {
			_, err = s.Library.Track(ctx, userID, item.ID, library.Planning, manual, time.Time{})
		}
		if err == nil && in.Score != nil {
			err = s.Library.SetSeasonScore(ctx, userID, item.ID, n, in.Score)
		}
		if err == nil && in.Notes != nil {
			err = s.Library.SetSeasonNotes(ctx, userID, item.ID, n, *in.Notes)
		}
	default:
		switch {
		case status != "":
			err = s.Library.SetStatus(ctx, userID, item.ID, status, manual, time.Time{})
		case !existing:
			_, err = s.Library.Track(ctx, userID, item.ID, library.Planning, manual, time.Time{})
		}
		if err == nil && in.Progress != nil && !catalog.HasEpisodes(item) && !catalog.IsMovie(item.Kind) {
			err = s.Library.SetProgress(ctx, userID, item.ID, *in.Progress, manual, time.Time{})
		}
		if err == nil && in.Score != nil {
			err = s.Library.SetScore(ctx, userID, item.ID, in.Score, manual)
		}
		if err == nil && in.Notes != nil {
			err = s.Library.SetNotes(ctx, userID, item.ID, *in.Notes)
		}
		if err == nil && (started.Set || finished.Set) {
			err = s.Library.SetDates(ctx, userID, item.ID, started, finished, manual)
		}
	}
	if err != nil {
		return nil, libraryError(err, item.Source)
	}
	entry, err := s.Library.CurrentEntry(ctx, userID, item.ID)
	if err != nil {
		return nil, libraryError(err, item.Source)
	}
	if in.InstanceID != nil {
		if entry, err = s.Library.GetEntry(ctx, userID, *in.InstanceID); err != nil {
			return nil, libraryError(err, item.Source)
		}
	}
	// A reclassified item may have moved kind since the request; report the stored one.
	item, err = s.Catalog.GetItem(ctx, item.ID)
	if err != nil {
		return nil, libraryError(err, item.Source)
	}
	return libraryEntryJSON(entry, item), nil
}

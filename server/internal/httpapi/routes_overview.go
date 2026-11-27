package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/auth"
	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// overviewSummaryKinds are the kinds /summary lists (assistant.py SUMMARY_TYPES,
// plus anime_movie).
var overviewSummaryKinds = []string{
	catalog.KindTV, catalog.KindMovie, catalog.KindAnime, catalog.KindAnimeMovie,
	catalog.KindManga, catalog.KindBook, catalog.KindComic, catalog.KindGame,
}

// overviewRoutes registers me, summary, now, activity, years (src/api/assistant.py,
// now.py, years.py) and the settings and user management endpoints.
func (s *Server) overviewRoutes(mux *http.ServeMux) {
	read := func(h func(r *http.Request) (any, error)) http.Handler {
		return auth.Require(JSON(func(_ http.ResponseWriter, r *http.Request) (any, error) { return h(r) }))
	}
	mux.Handle("GET /api/v1/me", read(s.overviewMe))
	mux.Handle("GET /api/v1/summary", read(s.overviewSummary))
	mux.Handle("GET /api/v1/now", read(s.overviewNow))
	mux.Handle("GET /api/v1/now/finished", read(s.overviewFinished))
	mux.Handle("GET /api/v1/activity", read(s.overviewActivity))
	mux.Handle("GET /api/v1/years", read(s.overviewYears))
	mux.Handle("GET /api/v1/years/{year}", read(s.overviewSnapshotHandler))
	s.settingsRoutes(mux)
}

func (s *Server) overviewMe(r *http.Request) (any, error) {
	p := auth.FromContext(r.Context())
	kind, scope := "session", "write"
	if p.Token != nil {
		kind, scope = "token", p.Token.Scope
	}
	return map[string]any{"username": p.User.Username, "auth": kind, "scope": scope, "timezone": overviewLoc(&p.User).String()}, nil
}

func (s *Server) overviewSummary(r *http.Request) (any, error) {
	ctx := r.Context()
	u := overviewUser(ctx)
	loc := overviewLoc(&u)
	days, err := overviewIntParam(r.URL.Query(), "recent_days", 30)
	if err != nil {
		return nil, err
	}
	enabled, err := s.overviewEnabled(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now := s.Library.Now()
	since := now.AddDate(0, 0, -days)
	rows, err := s.Library.UserEntries(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	inProgress := map[string][]map[string]any{}
	for _, k := range overviewSummaryKinds {
		if enabled[k] {
			inProgress[k] = []map[string]any{}
		}
	}
	type done struct {
		end   time.Time
		entry map[string]any
	}
	var completed []done
	for _, row := range rows {
		kind := row.Item.Kind
		if _, ok := inProgress[kind]; !ok {
			continue
		}
		switch row.Entry.Status {
		case library.InProgress:
			inProgress[kind] = append(inProgress[kind], overviewEntry(row))
		case library.Completed:
			if t, ok := overviewTime(row.Entry.FinishedAt.String, loc); ok && !t.Before(since) {
				completed = append(completed, done{t, overviewEntry(row)})
			}
		}
	}
	sort.SliceStable(completed, func(i, j int) bool { return completed[i].end.After(completed[j].end) })
	recent := make([]map[string]any, len(completed))
	for i, c := range completed {
		recent[i] = c.entry
	}

	today := now.In(loc)
	week, err := s.overviewDailyStats(ctx, u.ID, today.AddDate(0, 0, -6), today, loc)
	if err != nil {
		return nil, err
	}
	current, longest, err := s.overviewStreaks(ctx, u.ID, today, loc)
	if err != nil {
		return nil, err
	}
	var lastSession any
	if ls, err := s.Queries.GetLastReadingSession(ctx, u.ID); err == nil {
		if lastSession, err = s.overviewSessionDict(ctx, ls); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	pages := 0
	for _, d := range week {
		pages += d.Pages
	}
	todayStats := week[len(week)-1]
	return map[string]any{
		"generated_at":       now.UTC().Format(time.RFC3339Nano),
		"in_progress":        inProgress,
		"recently_completed": recent,
		"reading": map[string]any{
			"read_today": todayStats.Pages > 0 || todayStats.Chapters > 0, "today": todayStats, "last_7_days": week,
			"pages_last_7_days": pages, "current_streak": current, "longest_streak": longest, "last_session": lastSession,
		},
	}, nil
}

// overviewNowEntry is api/now.py: now_entry.
func (s *Server) overviewNowEntry(r *http.Request, n library.NowEntry) (map[string]any, error) {
	item := n.Item
	mediaType := item.Kind
	var season *int
	if n.Season != nil {
		season = n.Season
		if item.Kind == catalog.KindTV {
			mediaType = "season"
		}
	}
	var percent any
	if n.Total != nil && *n.Total > 0 {
		percent = overviewRound(float64(n.Progress) / float64(*n.Total) * 100)
	}
	var total, synopsis any
	if n.Total != nil {
		total = *n.Total
	}
	if item.Synopsis != "" {
		synopsis = item.Synopsis
	}
	genres := overviewGenres(item)
	if genres == nil {
		genres = []string{}
	}
	var score any
	if n.Score != nil {
		score = *n.Score
	}
	data := map[string]any{
		"id": n.Entry.ID, "media_type": mediaType, "item": overviewItem(item, mediaType, season, nil), "title": item.Title,
		"image": item.Image, "progress": n.Progress, "total": total, "percent": percent, "score": score,
		"progressed_at": n.ProgressedAt, "start_date": overviewNullStr(n.Entry.StartedAt), "genres": genres,
		"synopsis": synopsis, "next": nil,
	}
	if n.Next != nil {
		next := map[string]any{"number": n.Next.Number, "title": nil}
		if n.Next.Title != "" {
			next["title"] = n.Next.Title
		}
		if n.Season != nil {
			next["air_date"] = nil
			if n.Next.AirDate != "" {
				next["air_date"] = n.Next.AirDate
			}
		}
		data["next"] = next
	}
	if n.Season != nil {
		data["tv_id"] = n.Entry.ID
		data["season_number"] = *n.Season
	}
	if item.Kind == catalog.KindBook {
		ls, err := s.Queries.GetLastItemReadingSession(r.Context(), db.GetLastItemReadingSessionParams{UserID: n.Entry.UserID, ItemID: sql.NullInt64{Int64: item.ID, Valid: true}})
		if err == nil {
			data["last_session"] = map[string]any{
				"started_at": ls.StartedAt, "pages_read": ls.PagesRead, "minutes": overviewRound(float64(ls.DurationSeconds) / 60),
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return data, nil
}

func (s *Server) overviewNow(r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	enabled, err := s.overviewEnabled(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	groups, err := s.Library.NowGroups(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	links, err := s.Library.Links(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, g := range groups {
		if !enabled[g.Kind] {
			continue
		}
		entries := make([]map[string]any, 0, len(g.Entries))
		for _, e := range g.Entries {
			m, err := s.overviewNowEntry(r, e)
			if err != nil {
				return nil, err
			}
			m["linked"] = links[e.Item.ID]
			entries = append(entries, m)
		}
		out = append(out, map[string]any{"media_type": g.Kind, "entries": entries})
	}
	return map[string]any{"groups": out}, nil
}

func (s *Server) overviewFinished(r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	limit, err := overviewIntParam(r.URL.Query(), "limit", 12)
	if err != nil {
		return nil, err
	}
	enabled, err := s.overviewEnabled(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	var kinds []string
	for _, k := range catalog.Kinds {
		if enabled[k] {
			kinds = append(kinds, k)
		}
	}
	out := []map[string]any{}
	if len(kinds) == 0 {
		return out, nil
	}
	list, err := s.Library.RecentlyFinished(r.Context(), u.ID, overviewClamp(limit, 1, 50), kinds...)
	if err != nil {
		return nil, err
	}
	since := s.Library.Now().UTC().Add(-library.NowWindow).Format(time.RFC3339)
	for _, f := range list {
		if f.Entry.FinishedAt.String < since { // "recently" is the same window as Now
			continue
		}
		out = append(out, map[string]any{
			"id": f.Entry.ID, "media_type": f.Item.Kind, "item": overviewItem(f.Item, "", nil, nil),
			"score": overviewNullFloat(f.Entry.Score), "end_date": f.Entry.FinishedAt.String,
		})
	}
	return out, nil
}

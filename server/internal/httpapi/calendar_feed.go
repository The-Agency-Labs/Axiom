package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/calendar"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

const (
	calendarPastDays   = 30
	calendarFutureDays = 183 // ~6 months
	calendarMaxSpan    = 400 * 24 * time.Hour
)

// calEvent is one release of what the user tracks, ready to render.
type calEvent struct {
	row    db.ListCalendarReleasesRow
	at     time.Time
	allDay bool
}

func (e calEvent) season() *int64 { return nullPtr(e.row.Season) }
func (e calEvent) number() *int64 { return nullPtr(e.row.Number) }

func nullPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

// isDateOnly tells releases that carry only a date (the 11:59:59 UTC sentinel).
func isDateOnly(t time.Time) bool {
	t = t.UTC()
	return t.Hour() == 11 && t.Minute() == 59 && t.Second() == 59
}

func calendarUnit(kind string) string {
	switch kind {
	case catalog.KindAnime:
		return "Ep "
	case catalog.KindManga:
		return "Ch "
	case catalog.KindComic:
		return "#"
	}
	return ""
}

// summary is "Show S01E05 – Title", "Anime Ep 12", "Manga Ch 40", or the bare title.
func (e calEvent) summary() string {
	r := e.row
	switch {
	case r.Season.Valid && r.Number.Valid:
		s := fmt.Sprintf("%s S%02dE%02d", r.Title, r.Season.Int64, r.Number.Int64)
		if r.EpisodeTitle != "" {
			s += " – " + r.EpisodeTitle
		}
		return s
	case r.Number.Valid:
		if u := calendarUnit(r.Kind); u != "" {
			return fmt.Sprintf("%s %s%d", r.Title, u, r.Number.Int64)
		}
	}
	return r.Title
}

// uid is stable across refreshes: <item>-<season>-<episode>@encompass ("n" = none).
func (e calEvent) uid() string {
	part := func(n *int64) string {
		if n == nil {
			return "n"
		}
		return fmt.Sprint(*n)
	}
	return fmt.Sprintf("%d-%s-%s@encompass", e.row.ItemID, part(e.season()), part(e.number()))
}

func (s *Server) calendarEvents(ctx context.Context, userID int64, from, to time.Time) ([]calEvent, error) {
	rows, err := s.Queries.ListCalendarReleases(ctx, db.ListCalendarReleasesParams{
		UserID: userID, FromAt: catalog.Stamp(from), ToAt: catalog.Stamp(to),
	})
	if err != nil {
		return nil, err
	}
	out := make([]calEvent, 0, len(rows))
	for _, r := range rows {
		at, err := time.Parse(time.RFC3339, r.At)
		if err != nil {
			continue
		}
		out = append(out, calEvent{row: r, at: at.UTC(), allDay: isDateOnly(at)})
	}
	return out, nil
}

func (s *Server) calendarNow() time.Time {
	if s.Catalog != nil && s.Catalog.Now != nil {
		return s.Catalog.Now()
	}
	return time.Now()
}

// calendarFeed is GET /calendar/{token}.ics: the token is the credential.
func (s *Server) calendarFeed(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutSuffix(r.PathValue("file"), ".ics")
	if !ok || token == "" {
		http.NotFound(w, r)
		return
	}
	u, err := s.Queries.GetUserByWebhookToken(r.Context(), token)
	if err != nil || u.IsActive == 0 {
		http.NotFound(w, r)
		return
	}
	now := s.calendarNow().UTC()
	day := now.Truncate(24 * time.Hour)
	events, err := s.calendarEvents(r.Context(), u.ID, day.AddDate(0, 0, -calendarPastDays), day.AddDate(0, 0, calendarFutureDays+1).Add(-time.Second))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	evs := make([]calendar.Event, 0, len(events))
	for _, e := range events {
		evs = append(evs, calendar.Event{UID: e.uid(), Summary: e.summary(), Description: e.row.Kind, Start: e.at, AllDay: e.allDay})
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="calendar.ics"`)
	w.Header().Set("Cache-Control", "private, max-age=0")
	_, _ = w.Write(calendar.Encode("-//Encompass//EN", "Encompass", now, evs))
}

// calendarJSON is GET /api/v1/calendar?start=YYYY-MM-DD&end=YYYY-MM-DD (inclusive;
// days are in the user's time zone, default window: 30 days back to 90 ahead).
func (s *Server) calendarJSON(_ http.ResponseWriter, r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	loc, err := time.LoadLocation(u.Timezone)
	if err != nil {
		loc = time.UTC
	}
	today := s.calendarNow().In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	parse := func(name string, def time.Time) (time.Time, error) {
		v := r.URL.Query().Get(name)
		if v == "" {
			return def, nil
		}
		t, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return t, Errorf(http.StatusUnprocessableEntity, name+" must be YYYY-MM-DD")
		}
		return t, nil
	}
	start, err := parse("start", today.AddDate(0, 0, -calendarPastDays))
	if err != nil {
		return nil, err
	}
	end, err := parse("end", today.AddDate(0, 0, 90))
	if err != nil {
		return nil, err
	}
	if end.Before(start) {
		return nil, Errorf(http.StatusUnprocessableEntity, "end must not be before start")
	}
	if end.Sub(start) > calendarMaxSpan {
		return nil, Errorf(http.StatusUnprocessableEntity, "range too large")
	}
	// Date-only releases belong to their UTC date whatever the zone, so widen the
	// query by a day each side and place them afterwards.
	events, err := s.calendarEvents(r.Context(), u.ID, start.UTC().AddDate(0, 0, -1), end.UTC().AddDate(0, 0, 2))
	if err != nil {
		return nil, err
	}
	first, last := start.Format("2006-01-02"), end.Format("2006-01-02")
	out := []map[string]any{}
	for _, e := range events {
		date, tm := e.at.Format("2006-01-02"), any(nil)
		if !e.allDay {
			l := e.at.In(loc)
			date, tm = l.Format("2006-01-02"), l.Format(time.RFC3339)
		}
		if date < first || date > last {
			continue
		}
		var season, episode any
		if p := e.season(); p != nil {
			season = *p
		}
		if p := e.number(); p != nil {
			episode = *p
		}
		out = append(out, map[string]any{
			"date": date, "time": tm, "all_day": e.allDay, "title": e.summary(), "media_type": e.row.Kind,
			"item_id": e.row.ItemID, "season": season, "episode": episode, "image": e.row.Image,
		})
	}
	return out, nil
}

// calendarRegenerateToken is POST /api/v1/settings/calendar-token. The calendar
// feed and the Jellyfin webhook share one token per user (as Django's users.token),
// so this rotates both URLs, exactly like POST /settings/webhook-token.
func (s *Server) calendarRegenerateToken(_ http.ResponseWriter, r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	u.WebhookToken = base64.RawURLEncoding.EncodeToString(b)
	if err := s.Queries.SetWebhookToken(r.Context(), db.SetWebhookTokenParams{WebhookToken: u.WebhookToken, ID: u.ID}); err != nil {
		return nil, err
	}
	return settingsPaths(u), nil
}

package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
)

// overviewDay is one day of reading (reading.services.daily_stats).
type overviewDay struct {
	Date     string `json:"date"`
	Pages    int    `json:"pages"`
	Chapters int    `json:"chapters"`
	Minutes  int    `json:"minutes"`
	Sessions int    `json:"sessions"`
}

// overviewDailyStats returns every day from first to last (inclusive, dates
// in loc) with the totals of the sessions that started on it.
func (s *Server) overviewDailyStats(ctx context.Context, userID int64, first, last time.Time, loc *time.Location) ([]overviewDay, error) {
	from := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	to := time.Date(last.Year(), last.Month(), last.Day()+1, 0, 0, 0, 0, loc)
	sessions, err := s.Queries.ListUserReadingSessions(ctx, db.ListUserReadingSessionsParams{
		UserID: userID, FromAt: overviewStamp(from), ToAt: overviewStamp(to),
	})
	if err != nil {
		return nil, err
	}
	type acc struct{ pages, chapters, seconds, sessions int }
	by := map[string]*acc{}
	for _, r := range sessions {
		t, ok := overviewTime(r.StartedAt, loc)
		if !ok {
			continue
		}
		a := by[t.Format("2006-01-02")]
		if a == nil {
			a = &acc{}
			by[t.Format("2006-01-02")] = a
		}
		a.pages += int(r.PagesRead)
		a.chapters += int(r.ChaptersRead)
		a.seconds += int(r.DurationSeconds)
		a.sessions++
	}
	days := []overviewDay{}
	for d := from; d.Before(to); d = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc) {
		key := d.Format("2006-01-02")
		day := overviewDay{Date: key}
		if a := by[key]; a != nil {
			day = overviewDay{Date: key, Pages: a.pages, Chapters: a.chapters, Minutes: overviewRound(float64(a.seconds) / 60), Sessions: a.sessions}
		}
		days = append(days, day)
	}
	return days, nil
}

// overviewStreaks returns the current and longest run of consecutive days with
// pages or chapters read (reading.services.streaks). The current streak runs
// up to yesterday when the user hasn't read today.
func (s *Server) overviewStreaks(ctx context.Context, userID int64, today time.Time, loc *time.Location) (current, longest int, err error) {
	sessions, err := s.Queries.ListUserReadingSessions(ctx, db.ListUserReadingSessionsParams{
		UserID: userID, FromAt: "0000-01-01T00:00:00Z", ToAt: "9999-12-31T23:59:59Z",
	})
	if err != nil {
		return 0, 0, err
	}
	read := map[string]bool{}
	var days []string
	for _, r := range sessions {
		if r.PagesRead <= 0 && r.ChaptersRead <= 0 {
			continue
		}
		if t, ok := overviewTime(r.StartedAt, loc); ok {
			key := t.Format("2006-01-02")
			if !read[key] {
				read[key] = true
				days = append(days, key)
			}
		}
	}
	if len(days) == 0 {
		return 0, 0, nil
	}
	// days follow session order, which is chronological
	longest, run := 1, 1
	for i := 1; i < len(days); i++ {
		prev, _ := time.ParseInLocation("2006-01-02", days[i-1], loc)
		cur, _ := time.ParseInLocation("2006-01-02", days[i], loc)
		if prev.AddDate(0, 0, 1).Format("2006-01-02") == cur.Format("2006-01-02") {
			run++
		} else {
			run = 1
		}
		longest = max(longest, run)
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	if days[len(days)-1] != day.Format("2006-01-02") {
		day = day.AddDate(0, 0, -1)
	}
	for read[day.Format("2006-01-02")] {
		current++
		day = day.AddDate(0, 0, -1)
	}
	return current, longest, nil
}

// overviewSessionDict is api.serializers.session_dict.
func (s *Server) overviewSessionDict(ctx context.Context, r db.ReadingSession) (map[string]any, error) {
	var item any
	if r.ItemID.Valid {
		it, err := s.Queries.GetItem(ctx, r.ItemID.Int64)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			item = overviewItem(it, "", nil, nil)
		}
	}
	nullInt := func(n sql.NullInt64) any {
		if !n.Valid {
			return nil
		}
		return n.Int64
	}
	return map[string]any{
		"id": r.ID, "source": r.Source, "title": r.Title, "item": item, "document_id": nullInt(r.DocumentID),
		"started_at": r.StartedAt, "ended_at": r.EndedAt, "minutes": overviewRound(float64(r.DurationSeconds) / 60),
		"pages_read": r.PagesRead, "chapters_read": r.ChaptersRead,
		"start_page": nullInt(r.StartPage), "end_page": nullInt(r.EndPage), "device": r.Device,
	}, nil
}

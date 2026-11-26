package reading

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

// MaxRangeDays bounds a stats or sessions range.
const MaxRangeDays = 3660

// Day is one day of reading. Days without reading are included with zeros.
type Day struct {
	Date     string `json:"date"`
	Pages    int64  `json:"pages"`
	Chapters int64  `json:"chapters"`
	Minutes  int64  `json:"minutes"`
	Sessions int64  `json:"sessions"`
	Seconds  int64  `json:"-"` // summed before rounding totals
}

// Today is the current date in the service's zone.
func (s *Service) Today() time.Time {
	n := s.now().In(s.Loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.Loc)
}

// ParseDate parses YYYY-MM-DD as midnight in the service's zone.
func (s *Service) ParseDate(v string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", v, s.Loc)
	if err != nil {
		return t, fmt.Errorf("%w: invalid date %q", ErrInvalid, v)
	}
	return t, nil
}

// DateRange applies the defaults and limits of src/api/reading.py: end is
// today, start is 29 days before end, start must not be after end and the
// range is capped.
func (s *Service) DateRange(start, end *time.Time) (time.Time, time.Time, error) {
	e := s.Today()
	if end != nil {
		e = *end
	}
	st := e.AddDate(0, 0, -29)
	if start != nil {
		st = *start
	}
	if st.After(e) {
		return st, e, fmt.Errorf("%w: start must be before end", ErrInvalid)
	}
	if int(math.Round(e.Sub(st).Hours()/24)) > MaxRangeDays {
		return st, e, fmt.Errorf("%w: Range too large", ErrInvalid)
	}
	return st, e, nil
}

// bounds returns the UTC stamps covering the local days start..end inclusive.
func bounds(start, end time.Time) (string, string) {
	return Stamp(start), Stamp(end.AddDate(0, 0, 1))
}

func (s *Service) localDay(stamp string) time.Time {
	t := parseStamp(stamp).In(s.Loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.Loc)
}

// DailyStats returns per-day totals for the local days start..end inclusive.
func (s *Service) DailyStats(ctx context.Context, userID int64, start, end time.Time) ([]Day, error) {
	from, to := bounds(start, end)
	rows, err := s.Q.ListSessionsForStats(ctx, db.ListSessionsForStatsParams{UserID: userID, StartedAt: from, StartedAt_2: to})
	if err != nil {
		return nil, err
	}
	type agg struct{ pages, chapters, seconds, sessions int64 }
	by := map[string]agg{}
	for _, r := range rows {
		k := s.localDay(r.StartedAt).Format("2006-01-02")
		a := by[k]
		by[k] = agg{a.pages + r.PagesRead, a.chapters + r.ChaptersRead, a.seconds + r.DurationSeconds, a.sessions + 1}
	}
	days := []Day{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		a := by[k]
		days = append(days, Day{Date: k, Pages: a.pages, Chapters: a.chapters, Minutes: int64(math.RoundToEven(float64(a.seconds) / 60)), Sessions: a.sessions, Seconds: a.seconds})
	}
	return days, nil
}

// Streaks returns the current and longest run of consecutive reading days.
// The current streak counts today if read today, otherwise it runs up to
// yesterday (a streak isn't broken until the day is over).
func (s *Service) Streaks(ctx context.Context, userID int64) (current, longest int, err error) {
	starts, err := s.Q.ListReadSessionStarts(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	set := map[string]bool{}
	for _, st := range starts {
		set[s.localDay(st).Format("2006-01-02")] = true
	}
	if len(set) == 0 {
		return 0, 0, nil
	}
	days := make([]string, 0, len(set))
	for d := range set {
		days = append(days, d)
	}
	sort.Strings(days)
	longest, run := 1, 1
	for i := 1; i < len(days); i++ {
		a, _ := time.Parse("2006-01-02", days[i-1])
		b, _ := time.Parse("2006-01-02", days[i])
		if b.Sub(a) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		longest = max(longest, run)
	}
	today := s.Today()
	day := today
	if days[len(days)-1] != today.Format("2006-01-02") {
		day = today.AddDate(0, 0, -1)
	}
	for set[day.Format("2006-01-02")] {
		current++
		day = day.AddDate(0, 0, -1)
	}
	return current, longest, nil
}

// SessionRow is a session with the document id it rolls up to.
type SessionRow struct {
	Session    Session
	DocumentID sql.NullInt64
}

// ListSessions returns the sessions that started on the local days
// start..end, newest first, at most limit.
func (s *Service) ListSessions(ctx context.Context, userID int64, start, end time.Time, limit int) ([]SessionRow, error) {
	limit = max(0, min(limit, 1000))
	from, to := bounds(start, end)
	rows, err := s.Q.ListReadingSessions(ctx, db.ListReadingSessionsParams{UserID: userID, FromTs: from, ToTs: to, MaxRows: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionRow{Session: r.ReadingSession, DocumentID: sql.NullInt64{Int64: r.EffectiveDocumentID, Valid: r.EffectiveDocumentID != 0}})
	}
	return out, nil
}

// NewSession is a session reported by a client other than KOReader.
type NewSession struct {
	Source       string
	Title        string
	ItemID       *int64
	StartedAt    time.Time
	EndedAt      time.Time
	PagesRead    int64
	ChaptersRead int64
	StartPage    *int64
	EndPage      *int64
	Device       string
}

// CreateSession logs a reading session manually or from another app.
func (s *Service) CreateSession(ctx context.Context, userID int64, in NewSession) (Session, error) {
	if !ValidSessionSource(in.Source) {
		return Session{}, fmt.Errorf("%w: Unknown source", ErrInvalid)
	}
	if in.EndedAt.Before(in.StartedAt) {
		return Session{}, fmt.Errorf("%w: ended_at must be after started_at", ErrInvalid)
	}
	var item sql.NullInt64
	title := in.Title
	if in.ItemID != nil {
		it, err := s.Catalog.GetItem(ctx, *in.ItemID)
		if err != nil {
			return Session{}, ErrNotFound
		}
		item = nullInt(it.ID, true)
		if title == "" {
			title = it.Title
		}
	}
	opt := func(v *int64) sql.NullInt64 {
		if v == nil {
			return sql.NullInt64{}
		}
		return nullInt(*v, true)
	}
	return s.Q.InsertReadingSession(ctx, db.InsertReadingSessionParams{
		UserID: userID, Source: in.Source, ItemID: item, Title: title,
		StartedAt: Stamp(in.StartedAt), EndedAt: Stamp(in.EndedAt),
		DurationSeconds: int64(in.EndedAt.Sub(in.StartedAt) / time.Second),
		PagesRead:       in.PagesRead, StartPage: opt(in.StartPage), EndPage: opt(in.EndPage),
		ChaptersRead: in.ChaptersRead, Device: in.Device,
	})
}

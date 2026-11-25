// Package library is the user's side of the catalog: entries (one run of one
// item), per-season state of shows, plays (watches), and the history behind
// the activity and year views. It ports the model logic of src/app/models.py
// (TV/Season/Episode status rules, completion of released episodes, next
// season handling) and the "never undo" rules the Jellyfin webhook and the
// importers follow.
//
// Every operation takes a source. "manual" is the app: it may do anything the
// Django UI allowed (unwatch, lower progress, reopen). Any other source
// ("jellyfin", "anilist", "import", ...) is automated and follows the owner
// rules: it never undoes a completion, never lowers progress, only reopens a
// completed show for a season never watched before, never duplicates an
// episode when importing, never creates an empty season 1 for a show first
// seen at season N, and only moves statuses forward (Planning -> In progress
// -> Completed; Paused and Dropped, set by the user, are left alone).
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// Statuses of an entry or season.
const (
	Planning   = "Planning"
	InProgress = "In progress"
	Paused     = "Paused"
	Dropped    = "Dropped"
	Completed  = "Completed"
)

// SourceManual is the source of actions made in the app.
const SourceManual = "manual"

// Errors returned by operations.
var (
	ErrNoEntry      = errors.New("library: no such entry")
	ErrNotShow      = errors.New("library: item has no TMDB episodes")
	ErrNotMovie     = errors.New("library: item is not a movie")
	ErrCountBased   = errors.New("library: item is tracked by progress count")
	ErrInvalidInput = errors.New("library: invalid input")
)

// Entry, Watch and friends are the table rows.
type (
	Entry  = db.Entry
	Watch  = db.Watch
	Item   = db.Item
	Season = db.EntrySeason
)

// Service is the library. Create it with New.
type Service struct {
	Conn    *sql.DB
	Q       *db.Queries
	Catalog *catalog.Catalog
	// Now and Loc are the clock and the zone "today" is read in.
	Now func() time.Time
	Loc *time.Location
}

// New returns a Service. cat provides the season/episode data (SyncSeasons).
func New(conn *sql.DB, q *db.Queries, cat *catalog.Catalog) *Service {
	return &Service{Conn: conn, Q: q, Catalog: cat, Now: func() time.Time { return time.Now().UTC() }, Loc: time.UTC}
}

func (s *Service) now() time.Time { return s.Now().UTC() }

// Today is the current date in the library's zone (YYYY-MM-DD).
func (s *Service) Today() string { return s.now().In(s.Loc).Format("2006-01-02") }

// Automated reports whether a source is an automated one (everything except
// the app itself), which follows the never-undo rules.
func Automated(source string) bool { return source != "" && source != SourceManual }

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func nstamp(t time.Time) sql.NullString { return sql.NullString{String: stamp(t), Valid: true} }

func parseStamp(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func nullInt(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: true} }

// op is one operation inside a transaction: the item, the entry being
// changed and the context of the change.
type op struct {
	ctx    context.Context
	s      *Service
	q      *db.Queries
	userID int64
	item   Item
	entry  *Entry // nil until loaded or created
	orig   Entry  // what the entry looked like at the last flush
	source string
	auto   bool
	at     time.Time // when the change happened (watch time, import time)
	today  string

	eps map[int64][]db.ItemEpisode // season -> episodes, loaded on demand
}

// run executes fn in a transaction on the item. at is when the change is
// dated (zero = now).
func (s *Service) run(ctx context.Context, userID, itemID int64, source string, at time.Time, fn func(o *op) error) error {
	if at.IsZero() {
		at = s.now()
	}
	tx, err := s.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := s.Q.WithTx(tx)
	item, err := q.GetItem(ctx, itemID)
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return catalog.ErrNotFound
		}
		return err
	}
	o := &op{ctx: ctx, s: s, q: q, userID: userID, item: item, source: source, auto: Automated(source), at: at.UTC(), today: s.Today(), eps: map[int64][]db.ItemEpisode{}}
	if err := fn(o); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := o.flush(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// load fetches the current entry (highest run) into o.entry.
func (o *op) load() error {
	e, err := o.q.GetCurrentEntry(o.ctx, db.GetCurrentEntryParams{UserID: o.userID, ItemID: o.item.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	o.entry, o.orig = &e, e
	return nil
}

// create inserts run 1 (or run n+1 with newRun) with the given status and
// records the "created" event.
func (o *op) create(status string, run int64) error {
	e, err := o.q.InsertEntry(o.ctx, db.InsertEntryParams{
		UserID: o.userID, ItemID: o.item.ID, Run: run, Status: status,
		CreatedAt: stamp(o.at), UpdatedAt: stamp(o.at),
	})
	if err != nil {
		return err
	}
	o.entry, o.orig = &e, e
	return o.q.InsertEntryEvent(o.ctx, db.InsertEntryEventParams{
		EntryID: e.ID, At: stamp(o.at), Field: "created", New: sql.NullString{String: status, Valid: true},
	})
}

// ensure loads the current entry or creates it with status when missing.
func (o *op) ensure(status string) (created bool, err error) {
	if err := o.load(); err != nil {
		return false, err
	}
	if o.entry != nil {
		return false, nil
	}
	return true, o.create(status, 1)
}

// flush writes the entry if it changed, with an entry_events row for every
// status, progress and score change.
func (o *op) flush() error {
	if o.entry == nil || *o.entry == o.orig {
		return nil
	}
	e, old := o.entry, o.orig
	at := stamp(o.at)
	ev := func(field string, from, to sql.NullString) error {
		return o.q.InsertEntryEvent(o.ctx, db.InsertEntryEventParams{EntryID: e.ID, At: at, Field: field, Old: from, New: to})
	}
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	if e.Status != old.Status {
		if err := ev("status", str(old.Status), str(e.Status)); err != nil {
			return err
		}
	}
	if e.Progress != old.Progress {
		if err := ev("progress", str(strconv.FormatInt(old.Progress, 10)), str(strconv.FormatInt(e.Progress, 10))); err != nil {
			return err
		}
	}
	if e.Score != old.Score {
		if err := ev("score", fmtScore(old.Score), fmtScore(e.Score)); err != nil {
			return err
		}
	}
	e.UpdatedAt = at
	if err := o.q.UpdateEntry(o.ctx, db.UpdateEntryParams{
		Status: e.Status, Score: e.Score, Progress: e.Progress, ProgressedAt: e.ProgressedAt,
		StartedAt: e.StartedAt, FinishedAt: e.FinishedAt, Notes: e.Notes, UpdatedAt: e.UpdatedAt, ID: e.ID,
	}); err != nil {
		return err
	}
	o.orig = *e
	return nil
}

func fmtScore(s sql.NullFloat64) sql.NullString {
	if !s.Valid {
		return sql.NullString{}
	}
	return sql.NullString{String: strconv.FormatFloat(s.Float64, 'f', -1, 64), Valid: true}
}

// statusRank orders the statuses an automated source may move through.
func statusRank(status string) int {
	switch status {
	case Planning:
		return 0
	case InProgress:
		return 1
	case Completed:
		return 2
	}
	return -1
}

// canAdvance reports whether an automated source may move from one status to
// another: only forward along Planning -> In progress -> Completed. Anything
// outside the progression (Paused, Dropped) was set by the user and stays.
func canAdvance(from, to string) bool {
	f, t := statusRank(from), statusRank(to)
	return f >= 0 && t > f
}

// setStatus changes the status of the entry, keeping the started and
// finished dates consistent: starting fills started_at, completing fills
// finished_at, leaving Completed clears finished_at.
func (o *op) setStatus(status string) {
	e := o.entry
	if e.Status == status {
		return
	}
	prev := e.Status
	e.Status = status
	switch status {
	case InProgress:
		if !e.StartedAt.Valid {
			e.StartedAt = nstamp(o.at)
		}
	case Completed:
		if !e.FinishedAt.Valid {
			e.FinishedAt = nstamp(o.at)
		}
	}
	if prev == Completed && status != Completed {
		e.FinishedAt = sql.NullString{}
	}
}

func (o *op) requireEntry() error {
	if o.entry == nil {
		return ErrNoEntry
	}
	return nil
}

// CurrentEntry returns the user's current entry (highest run) for an item.
func (s *Service) CurrentEntry(ctx context.Context, userID, itemID int64) (Entry, error) {
	e, err := s.Q.GetCurrentEntry(ctx, db.GetCurrentEntryParams{UserID: userID, ItemID: itemID})
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNoEntry
	}
	return e, err
}

// Runs returns every run of an item for a user, oldest first.
func (s *Service) Runs(ctx context.Context, userID, itemID int64) ([]Entry, error) {
	return s.Q.ListEntryRuns(ctx, db.ListEntryRunsParams{UserID: userID, ItemID: itemID})
}

// GetEntry returns an entry of the user by id.
func (s *Service) GetEntry(ctx context.Context, userID, entryID int64) (Entry, error) {
	e, err := s.Q.GetEntryForUser(ctx, db.GetEntryForUserParams{ID: entryID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNoEntry
	}
	return e, err
}

// Track creates the entry for an item (run 1) with a status when the user
// has none, and returns the current entry either way. A new entry gets a
// "created" event.
func (s *Service) Track(ctx context.Context, userID, itemID int64, status, source string, at time.Time) (Entry, error) {
	if status == "" {
		status = Planning
	}
	if !validStatus(status) {
		return Entry{}, fmt.Errorf("%w: status %q", ErrInvalidInput, status)
	}
	err := s.run(ctx, userID, itemID, source, at, func(o *op) error {
		created, err := o.ensure(status)
		if err != nil {
			return err
		}
		if created && status == InProgress {
			o.entry.StartedAt = nstamp(o.at)
		}
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	return s.CurrentEntry(ctx, userID, itemID)
}

// NewRun starts a rewatch or reread: a new entry with the next run number
// (the highest run is the current one), with the given status (default In
// progress) started at `at`. It is only done when the owner asks; automated
// sources never call it, they add watches to the current run instead.
func (s *Service) NewRun(ctx context.Context, userID, itemID int64, status string, at time.Time) (Entry, error) {
	if status == "" {
		status = InProgress
	}
	if !validStatus(status) {
		return Entry{}, fmt.Errorf("%w: status %q", ErrInvalidInput, status)
	}
	err := s.run(ctx, userID, itemID, SourceManual, at, func(o *op) error {
		if err := o.load(); err != nil {
			return err
		}
		run := int64(1)
		if o.entry != nil {
			run = o.entry.Run + 1
		}
		if err := o.create(status, run); err != nil {
			return err
		}
		if status == InProgress {
			o.entry.StartedAt = nstamp(o.at)
		}
		return nil
	})
	if err != nil {
		return Entry{}, err
	}
	return s.CurrentEntry(ctx, userID, itemID)
}

func validStatus(s string) bool {
	switch s {
	case Planning, InProgress, Paused, Dropped, Completed:
		return true
	}
	return false
}

// Optional is a value that may be set, cleared or left alone, for partial updates.
type Optional[T any] struct {
	Set   bool
	Value *T // nil with Set = clear
}

// Some sets a value.
func Some[T any](v T) Optional[T] { return Optional[T]{Set: true, Value: &v} }

// Clear clears a value.
func Clear[T any]() Optional[T] { return Optional[T]{Set: true} }

// SetDates changes the start and finish dates of the current entry.
func (s *Service) SetDates(ctx context.Context, userID, itemID int64, started, finished Optional[time.Time], source string) error {
	return s.run(ctx, userID, itemID, source, time.Time{}, func(o *op) error {
		if err := o.load(); err != nil {
			return err
		}
		if err := o.requireEntry(); err != nil {
			return err
		}
		apply := func(dst *sql.NullString, v Optional[time.Time]) {
			if !v.Set {
				return
			}
			if v.Value == nil {
				*dst = sql.NullString{}
			} else {
				*dst = nstamp(*v.Value)
			}
		}
		apply(&o.entry.StartedAt, started)
		apply(&o.entry.FinishedAt, finished)
		return nil
	})
}

// SetNotes changes the notes of the current entry.
func (s *Service) SetNotes(ctx context.Context, userID, itemID int64, notes string) error {
	return s.run(ctx, userID, itemID, SourceManual, time.Time{}, func(o *op) error {
		if err := o.load(); err != nil {
			return err
		}
		if err := o.requireEntry(); err != nil {
			return err
		}
		o.entry.Notes = notes
		return nil
	})
}

// SetScore sets the score (0-10, one decimal) of the current entry, creating
// the entry as Planning when the user has none; nil clears it.
func (s *Service) SetScore(ctx context.Context, userID, itemID int64, score *float64, source string) error {
	var v sql.NullFloat64
	if score != nil {
		if *score < 0 || *score > 10 {
			return fmt.Errorf("%w: score %v", ErrInvalidInput, *score)
		}
		v = sql.NullFloat64{Float64: float64(int(*score*10+0.5)) / 10, Valid: true}
	}
	return s.run(ctx, userID, itemID, source, time.Time{}, func(o *op) error {
		if _, err := o.ensure(Planning); err != nil {
			return err
		}
		o.entry.Score = v
		return nil
	})
}

// Delete removes an entry of the user with its watches, seasons and history.
func (s *Service) Delete(ctx context.Context, userID, entryID int64) error {
	e, err := s.GetEntry(ctx, userID, entryID)
	if err != nil {
		return err
	}
	return s.Q.DeleteEntry(ctx, e.ID)
}

package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
)

// StepSize is how far Step moves progress: games count minutes in half hours,
// everything else (chapters, issues, pages, episodes of count-based anime) by one.
func StepSize(kind string) int {
	if kind == catalog.KindGame {
		return 30
	}
	return 1
}

// total is the number progress counts up to, 0 when unknown.
func (o *op) total() int64 { return o.item.Total.Int64 }

// moveProgress sets the progress of a count-based entry and derives the
// status, following process_progress: progress is clamped to the total, and
// an In progress entry that reaches the total is Completed.
//
// Manual changes may lower progress; lowering or raising a Completed entry
// reopens it, and a Planning entry that gets progress starts. Automated ones
// never lower progress and never touch Completed, Paused or Dropped.
func (o *op) moveProgress(progress int64) {
	e := o.entry
	if progress < 0 {
		progress = 0
	}
	if t := o.total(); t > 0 && progress > t {
		progress = t
	}
	if progress == e.Progress {
		return
	}
	if o.auto && progress < e.Progress {
		return
	}
	e.Progress = progress
	e.ProgressedAt = nstamp(o.at)
	switch e.Status {
	case Planning:
		if progress > 0 {
			o.setStatus(InProgress)
		}
	case Completed:
		if !o.auto {
			o.setStatus(InProgress)
		}
	}
	if e.Status == InProgress && o.total() > 0 && e.Progress == o.total() {
		o.setStatus(Completed)
	}
}

func (s *Service) countBased(ctx context.Context, itemID int64) (Item, error) {
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return Item{}, err
	}
	if catalog.HasEpisodes(item) || catalog.IsMovie(item.Kind) {
		return Item{}, ErrCountBased
	}
	return item, nil
}

// SetProgress sets the progress of a count-based item (chapters, issues, pages,
// minutes played, episodes of anime tracked by count), creating the entry
// when the user has none. `at` dates the change.
func (s *Service) SetProgress(ctx context.Context, userID, itemID int64, progress int, source string, at time.Time) error {
	if _, err := s.countBased(ctx, itemID); err != nil {
		return err
	}
	return s.run(ctx, userID, itemID, source, at, func(o *op) error {
		if _, err := o.ensure(Planning); err != nil {
			return err
		}
		o.moveProgress(int64(progress))
		return nil
	})
}

// Step moves the progress of a count-based item one unit up or down (30
// minutes for games); manual only.
func (s *Service) Step(ctx context.Context, userID, itemID int64, up bool) error {
	item, err := s.countBased(ctx, itemID)
	if err != nil {
		return err
	}
	return s.run(ctx, userID, itemID, SourceManual, time.Time{}, func(o *op) error {
		if _, err := o.ensure(Planning); err != nil {
			return err
		}
		n := int64(StepSize(item.Kind))
		if !up {
			n = -n
		}
		o.moveProgress(o.entry.Progress + n)
		return nil
	})
}

// WatchMovie records a play of a movie (or anime movie). The first play
// completes the entry. Another play of a completed movie is a rewatch and
// starts a new run, unless Import is set (a backfill never logs a rewatch)
// or the source is automated and the play is the one already recorded.
func (s *Service) WatchMovie(ctx context.Context, userID, itemID int64, at time.Time, source string, opts WatchOptions) (WatchResult, error) {
	var res WatchResult
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return res, err
	}
	if !catalog.IsMovie(item.Kind) {
		return res, ErrNotMovie
	}
	err = s.run(ctx, userID, itemID, source, at, func(o *op) error {
		if err := o.load(); err != nil {
			return err
		}
		switch {
		case o.entry == nil:
			if err := o.create(Completed, 1); err != nil {
				return err
			}
			res.EntryCreated = true
		case o.entry.Status == Completed:
			if opts.Import {
				res.Skipped, res.Entry = true, *o.entry
				return nil
			}
			if o.auto {
				// A webhook retry of the play just recorded.
				if latest, err := s.lastMovieWatch(o, o.entry.ID); err == nil {
					if d := o.at.Sub(parseStamp(latest.WatchedAt)); d < dupWindow && d > -dupWindow {
						res.Skipped, res.Entry = true, *o.entry
						return nil
					}
				}
			}
			if err := o.create(Completed, o.entry.Run+1); err != nil {
				return err
			}
		}
		e := o.entry
		e.Progress = 1
		e.ProgressedAt = nstamp(o.at)
		o.setStatus(Completed)
		e.FinishedAt = nstamp(o.at)
		w, err := o.q.InsertWatch(o.ctx, db.InsertWatchParams{
			EntryID: e.ID, WatchedAt: stamp(o.at), Source: source, Notes: opts.Notes,
		})
		if err != nil {
			return err
		}
		res.Watch = &w
		res.Entry = *e
		return nil
	})
	if err != nil {
		return WatchResult{}, err
	}
	if res.Entry.ID != 0 {
		res.Entry, _ = s.Q.GetEntry(ctx, res.Entry.ID)
	}
	return res, nil
}

func (s *Service) lastMovieWatch(o *op, entryID int64) (db.Watch, error) {
	ws, err := o.q.ListEntryWatches(o.ctx, entryID)
	if err != nil {
		return db.Watch{}, err
	}
	if len(ws) == 0 {
		return db.Watch{}, sql.ErrNoRows
	}
	return ws[len(ws)-1], nil
}

// SetStatus changes the status of the current entry, creating it when the
// user has none.
//
// Manual changes port Media.save/TV.save: Completed on a count-based item
// sets progress to the total, on a movie to 1 (with a play), on a show marks
// every released episode watched (TV._completed) and leaves the show In
// progress when unreleased episodes or seasons remain; Dropped drops the
// show's In progress seasons; In progress on a show with no In progress
// season starts the next released one.
//
// Automated changes (sync from an import) only move forward along Planning,
// In progress, Completed and have no other effect.
func (s *Service) SetStatus(ctx context.Context, userID, itemID int64, status, source string, at time.Time) error {
	if !validStatus(status) {
		return fmt.Errorf("%w: status %q", ErrInvalidInput, status)
	}
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	if catalog.HasEpisodes(item) && !Automated(source) {
		if err := s.Catalog.SyncSeasons(ctx, item); err != nil {
			return err
		}
	}
	return s.run(ctx, userID, itemID, source, at, func(o *op) error {
		created, err := o.ensure(status)
		if err != nil {
			return err
		}
		e := o.entry
		if o.auto {
			// A new entry takes the imported status as is; an existing one moves forward only.
			if !created && canAdvance(e.Status, status) {
				o.setStatus(status)
			}
			return nil
		}
		if created {
			// Made with the target status (one "created" event); now apply its effects
			// as a change from Planning.
			e.Status = Planning
			o.orig.Status = status
		}
		prev := e.Status
		o.setStatus(status)
		switch {
		case catalog.HasEpisodes(item):
			return o.showStatusEffects(prev, status)
		case catalog.IsMovie(item.Kind):
			if status == Completed {
				e.Progress = 1
				if !e.ProgressedAt.Valid {
					e.ProgressedAt = nstamp(o.at)
				}
				plays, err := o.q.ListEntryWatches(o.ctx, e.ID)
				if err != nil {
					return err
				}
				if len(plays) == 0 {
					if _, err := o.q.InsertWatch(o.ctx, db.InsertWatchParams{EntryID: e.ID, WatchedAt: stamp(o.at), Source: o.source}); err != nil {
						return err
					}
				}
			}
		default:
			if status == Completed && o.total() > 0 && e.Progress != o.total() {
				e.Progress = o.total()
				e.ProgressedAt = nstamp(o.at)
			}
		}
		return nil
	})
}

// showStatusEffects ports the status change hooks of TV.save.
func (o *op) showStatusEffects(prev, status string) error {
	switch status {
	case Completed:
		if err := o.completeShow(); err != nil {
			return err
		}
	case Dropped:
		rows, err := o.q.ListEntrySeasons(o.ctx, o.entry.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Status == InProgress {
				if err := o.putSeason(r.Season, Dropped); err != nil {
					return err
				}
			}
		}
	case InProgress:
		rows, err := o.q.ListEntrySeasons(o.ctx, o.entry.ID)
		if err != nil {
			return err
		}
		active := false
		for _, r := range rows {
			active = active || r.Status == InProgress
		}
		if !active && prev != InProgress {
			if _, err := o.startNextAvailableSeason(0); err != nil {
				return err
			}
		}
	}
	return o.refreshShow()
}

// SyncValues are what an import knows about an entry, applied additively by SyncEntry.
type SyncValues struct {
	Status     string
	Score      *float64
	Notes      string
	Progress   int
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// SyncEntry ports imports.helpers.sync_existing_media for count-based items
// and movies: it fills the score and notes the entry is missing, moves its
// status forward, raises its progress (never lowers it) and fills the dates
// it lacks (a finish date only for completed entries), creating the entry
// when the user has none. It reports whether anything changed.
func (s *Service) SyncEntry(ctx context.Context, userID, itemID int64, v SyncValues, source string, at time.Time) (bool, error) {
	changed := false
	err := s.run(ctx, userID, itemID, source, at, func(o *op) error {
		status := v.Status
		if status == "" {
			status = Planning
		}
		created, err := o.ensure(status)
		if err != nil {
			return err
		}
		e := o.entry
		changed = created
		hadStart, hadFinish := e.StartedAt.Valid, e.FinishedAt.Valid
		if v.Score != nil && !e.Score.Valid {
			e.Score = sql.NullFloat64{Float64: *v.Score, Valid: true}
		}
		if v.Notes != "" && e.Notes == "" {
			e.Notes = v.Notes
		}
		if canAdvance(e.Status, v.Status) {
			o.setStatus(v.Status)
		}
		if int64(v.Progress) > e.Progress {
			e.Progress = int64(v.Progress)
			e.ProgressedAt = nstamp(o.at)
		}
		if v.StartedAt != nil && !hadStart {
			e.StartedAt = nstamp(*v.StartedAt)
		}
		if v.FinishedAt != nil && e.Status == Completed && !hadFinish {
			e.FinishedAt = nstamp(*v.FinishedAt)
		}
		changed = changed || *e != o.orig
		return nil
	})
	return changed, err
}

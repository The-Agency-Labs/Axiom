package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// dupWindow is how close two automated plays of one episode must be to count
// as one (webhooks fire more than once, Django issue #689).
const dupWindow = 5 * time.Second

// episodes returns the TMDB episodes of a season in position order.
func (o *op) episodes(season int64) ([]db.ItemEpisode, error) {
	if eps, ok := o.eps[season]; ok {
		return eps, nil
	}
	eps, err := o.q.ListSeasonEpisodes(o.ctx, db.ListSeasonEpisodesParams{ItemID: o.item.ID, Season: season})
	if err != nil {
		return nil, err
	}
	o.eps[season] = eps
	return eps, nil
}

func (o *op) entrySeason(n int64) (db.EntrySeason, bool, error) {
	es, err := o.q.GetEntrySeason(o.ctx, db.GetEntrySeasonParams{EntryID: o.entry.ID, Season: n})
	if errors.Is(err, sql.ErrNoRows) {
		return db.EntrySeason{}, false, nil
	}
	return es, err == nil, err
}

// putSeason sets the status of a season row, creating it when missing and
// keeping its score and notes.
func (o *op) putSeason(n int64, status string) error {
	es, _, err := o.entrySeason(n)
	if err != nil {
		return err
	}
	es.EntryID, es.Season, es.Status = o.entry.ID, n, status
	return o.q.UpsertEntrySeason(o.ctx, db.UpsertEntrySeasonParams{
		EntryID: es.EntryID, Season: es.Season, Status: es.Status, Score: es.Score, Notes: es.Notes,
	})
}

// positions maps each episode number of a season to its position; episodes
// TMDB doesn't list (a Jellyfin numbering mismatch) get their own number.
func (o *op) positions(season int64) (map[int64]int64, error) {
	eps, err := o.episodes(season)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(eps))
	for _, e := range eps {
		out[e.Number] = e.Position
	}
	return out, nil
}

func (o *op) position(season, number int64) (int64, error) {
	pos, err := o.positions(season)
	if err != nil {
		return 0, err
	}
	if p, ok := pos[number]; ok {
		return p, nil
	}
	return number, nil
}

// latestWatchedPosition is the highest position of a watched episode of the
// season, 0 when none.
func (o *op) latestWatchedPosition(season int64) (int64, error) {
	ws, err := o.q.ListSeasonWatches(o.ctx, db.ListSeasonWatchesParams{EntryID: o.entry.ID, Season: sql.NullInt64{Int64: season, Valid: true}})
	if err != nil {
		return 0, err
	}
	var best int64
	for _, w := range ws {
		p, err := o.position(season, w.Episode.Int64)
		if err != nil {
			return 0, err
		}
		best = max(best, p)
	}
	return best, nil
}

// released returns the numbers of the season's episodes that have aired.
func (o *op) released(season int64) (map[int64]bool, error) {
	eps, err := o.episodes(season)
	if err != nil {
		return nil, err
	}
	return catalog.ReleasedEpisodes(eps, o.today), nil
}

// completionStatus ports Season.get_completion_status: the status a season
// gets when everything released is marked watched. Episodes up to the latest
// watched one count as covered. It also returns the released episodes still
// to mark (get_remaining_eps).
func (o *op) completionStatus(season int64, unreleasedOnly string) (string, []db.ItemEpisode, error) {
	latest, err := o.latestWatchedPosition(season)
	if err != nil {
		return "", nil, err
	}
	eps, err := o.episodes(season)
	if err != nil {
		return "", nil, err
	}
	rel := catalog.ReleasedEpisodes(eps, o.today)
	var remaining []db.ItemEpisode
	unreleased := false
	for _, e := range eps {
		if e.Position <= latest {
			continue
		}
		if rel[e.Number] {
			remaining = append(remaining, e)
		} else {
			unreleased = true
		}
	}
	switch {
	case !unreleased:
		return Completed, remaining, nil
	case latest > 0 || len(remaining) > 0:
		return InProgress, remaining, nil
	}
	return unreleasedOnly, remaining, nil
}

func (o *op) addWatch(season, episode int64, at time.Time, notes string) (db.Watch, error) {
	return o.q.InsertWatch(o.ctx, db.InsertWatchParams{
		EntryID: o.entry.ID, Season: sql.NullInt64{Int64: season, Valid: true}, Episode: sql.NullInt64{Int64: episode, Valid: true},
		WatchedAt: stamp(at), Source: o.source, Notes: notes,
	})
}

func (o *op) addWatches(season int64, eps []db.ItemEpisode) error {
	for _, e := range eps {
		if _, err := o.addWatch(season, e.Number, o.at, ""); err != nil {
			return err
		}
	}
	return nil
}

// refreshShow recomputes what is derived from the watches: progress (distinct
// episodes watched in regular seasons), when it last moved, and the dates.
func (o *op) refreshShow() error {
	e := o.entry
	n, err := o.q.CountWatchedEpisodes(o.ctx, e.ID)
	if err != nil {
		return err
	}
	b, err := o.q.WatchBounds(o.ctx, e.ID)
	if err != nil {
		return err
	}
	first, _ := b.FirstAt.(string)
	last, _ := b.LastAt.(string)
	e.Progress = n
	if last != "" {
		e.ProgressedAt = sql.NullString{String: last, Valid: true}
	} else if n == 0 {
		e.ProgressedAt = sql.NullString{}
	}
	at := stamp(o.at)
	if first != "" && (!e.StartedAt.Valid || (e.StartedAt.String == at && first < at)) {
		e.StartedAt = sql.NullString{String: first, Valid: true}
	}
	if e.Status == Completed && last != "" && (!e.FinishedAt.Valid || e.FinishedAt.String == at) {
		e.FinishedAt = sql.NullString{String: last, Valid: true}
	}
	return nil
}

// WatchOptions tune WatchEpisode.
type WatchOptions struct {
	// Import is a backfill: an episode already watched is left alone instead
	// of being logged again as a rewatch.
	Import bool
	// Notes are stored with the watch.
	Notes string
}

// WatchResult describes what WatchEpisode did.
type WatchResult struct {
	Entry           Entry
	Watch           *Watch // nil when nothing was recorded
	Skipped         bool   // already watched (import) or a duplicate play
	EntryCreated    bool
	SeasonCreated   bool
	SeasonCompleted bool // this watch completed the season
}

// WatchEpisode records a play of one episode of a show (`season` and
// `episode` are TMDB numbers), creating the entry and the season on demand,
// and derives the statuses. Seasons and episodes are synced first when stale.
//
// Manual plays follow Django's Episode.save: the finale completes the
// season (and may start the next one or complete the show), any other
// episode moves the season and show to In progress, even from Completed.
//
// Automated plays never undo a completion: watching an old episode of a
// completed season or show changes no status, a completed show reopens only
// for a regular season never watched before, a first play at season N
// creates only season N, a Paused or Dropped status is left alone, and with
// Import an episode already watched is skipped. Plays of the same episode a
// few seconds apart are one play.
func (s *Service) WatchEpisode(ctx context.Context, userID, itemID int64, season, episode int, at time.Time, source string, opts WatchOptions) (WatchResult, error) {
	var res WatchResult
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return res, err
	}
	if !catalog.HasEpisodes(item) {
		return res, ErrNotShow
	}
	if err := s.Catalog.SyncSeasons(ctx, item); err != nil {
		slog.WarnContext(ctx, "library: season sync failed", "item", itemID, "err", err)
	}
	err = s.run(ctx, userID, itemID, source, at, func(o *op) error {
		if err := o.load(); err != nil {
			return err
		}
		sn, ep := int64(season), int64(episode)
		if o.entry != nil {
			latest, err := o.q.LatestEpisodeWatch(o.ctx, db.LatestEpisodeWatchParams{
				EntryID: o.entry.ID, Season: sql.NullInt64{Int64: sn, Valid: true}, Episode: sql.NullInt64{Int64: ep, Valid: true},
			})
			switch {
			case err == nil && opts.Import:
				res.Skipped, res.Entry = true, *o.entry
				return nil
			case err == nil && o.auto:
				if d := o.at.Sub(parseStamp(latest.WatchedAt)); d < dupWindow && d > -dupWindow {
					res.Skipped, res.Entry = true, *o.entry
					return nil
				}
			case err != nil && !errors.Is(err, sql.ErrNoRows):
				return err
			}
		} else {
			if err := o.create(Planning, 1); err != nil {
				return err
			}
			res.EntryCreated = true
		}

		es, hadSeason, err := o.entrySeason(sn)
		if err != nil {
			return err
		}
		if !hadSeason {
			if err := o.putSeason(sn, InProgress); err != nil {
				return err
			}
			es.Status, res.SeasonCreated = InProgress, true
		}
		if o.auto {
			// Move the show and season forward without undoing anything.
			switch o.entry.Status {
			case Planning:
				o.setStatus(InProgress)
			case Completed:
				if res.SeasonCreated && sn > 0 {
					o.setStatus(InProgress)
				}
			}
			if es.Status == Planning {
				if err := o.putSeason(sn, InProgress); err != nil {
					return err
				}
				es.Status = InProgress
			}
		}

		w, err := o.addWatch(sn, ep, o.at, opts.Notes)
		if err != nil {
			return err
		}
		res.Watch = &w

		eps, err := o.episodes(sn)
		if err != nil {
			return err
		}
		isFinale := len(eps) > 0 && eps[len(eps)-1].Number == ep
		if o.auto {
			if isFinale && (es.Status == Planning || es.Status == InProgress) {
				res.SeasonCompleted = true
			}
		} else if isFinale {
			res.SeasonCompleted = es.Status != Completed
		} else if es.Status != InProgress {
			if err := o.putSeason(sn, InProgress); err != nil {
				return err
			}
		}
		if res.SeasonCompleted {
			if err := o.putSeason(sn, Completed); err != nil {
				return err
			}
			if sn > 0 {
				if err := o.handleCompletedSeason(sn); err != nil {
					return err
				}
			}
		} else if !o.auto && !isFinale && o.entry.Status != InProgress {
			o.setStatus(InProgress)
		}
		if err := o.refreshShow(); err != nil {
			return err
		}
		res.Entry = *o.entry
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

// handleCompletedSeason ports TV._handle_completed_season: start the next
// released season, or complete the show when every released season is done.
// Automated changes leave a Paused, Dropped or Completed show alone.
func (o *op) handleCompletedSeason(season int64) error {
	if o.auto && (o.entry.Status == Paused || o.entry.Status == Dropped || o.entry.Status == Completed) {
		return nil
	}
	started, err := o.startNextAvailableSeason(season)
	if err != nil || started {
		return err
	}
	incomplete, err := o.incompleteSeasonsExist()
	if err != nil {
		return err
	}
	switch {
	case incomplete && o.entry.Status != InProgress:
		o.setStatus(InProgress)
	case !incomplete && o.entry.Status != Completed:
		o.setStatus(Completed)
	}
	return nil
}

// incompleteSeasonsExist reports whether a regular season is not completed:
// one the user tracks, or one TMDB lists as already aired.
func (o *op) incompleteSeasonsExist() (bool, error) {
	rows, err := o.q.ListEntrySeasons(o.ctx, o.entry.ID)
	if err != nil {
		return false, err
	}
	done := map[int64]bool{}
	for _, r := range rows {
		if r.Season > 0 && r.Status != Completed {
			return true, nil
		}
		done[r.Season] = r.Status == Completed
	}
	seasons, err := o.q.ListItemSeasons(o.ctx, o.item.ID)
	if err != nil {
		return false, err
	}
	for _, sd := range seasons {
		if sd.Number > 0 && sd.EpisodeCount > 0 && releasedDate(sd.AirDate, o.today) && !done[sd.Number] {
			return true, nil
		}
	}
	return false, nil
}

func releasedDate(d sql.NullString, today string) bool {
	if !d.Valid {
		return false
	}
	t, err := catalog.ParseReleaseDate(d.String)
	return err == nil && t.Format("2006-01-02") <= today
}

// startNextAvailableSeason ports TV._start_next_available_season: the first
// released season after `after` that isn't completed becomes In progress
// (created when the user has no row for it) and the show with it.
func (o *op) startNextAvailableSeason(after int64) (bool, error) {
	seasons, err := o.q.ListItemSeasons(o.ctx, o.item.ID)
	if err != nil {
		return false, err
	}
	rows, err := o.q.ListEntrySeasons(o.ctx, o.entry.ID)
	if err != nil {
		return false, err
	}
	existing := map[int64]db.EntrySeason{}
	for _, r := range rows {
		existing[r.Season] = r
	}
	started := false
	for _, sd := range seasons {
		if sd.Number <= after {
			continue
		}
		es, has := existing[sd.Number]
		if has && es.Status == Completed {
			continue
		}
		if !releasedDate(sd.AirDate, o.today) {
			continue
		}
		switch {
		case !has:
			if err := o.putSeason(sd.Number, InProgress); err != nil {
				return false, err
			}
			started = true
		case o.auto && (es.Status == Paused || es.Status == Dropped):
			// The user parked it; don't wake it up or move past it.
		case es.Status != InProgress:
			if err := o.putSeason(sd.Number, InProgress); err != nil {
				return false, err
			}
			started = true
		default:
			started = true
		}
		break
	}
	if started && o.entry.Status != InProgress && !(o.auto && (o.entry.Status == Paused || o.entry.Status == Dropped || o.entry.Status == Completed)) {
		o.setStatus(InProgress)
	}
	return started, nil
}

// UnwatchEpisode removes the newest play of an episode (manual only, like
// the Django UI). A Completed season goes back to In progress when no
// episode at or past the removed one remains watched, and a Completed show
// follows when its season did. Unwatching something never watched is a no-op.
func (s *Service) UnwatchEpisode(ctx context.Context, userID, itemID int64, season, episode int) error {
	return s.run(ctx, userID, itemID, SourceManual, time.Time{}, func(o *op) error {
		if err := o.load(); err != nil {
			return err
		}
		if err := o.requireEntry(); err != nil {
			return err
		}
		sn, ep := int64(season), int64(episode)
		w, err := o.q.LatestEpisodeWatch(o.ctx, db.LatestEpisodeWatchParams{
			EntryID: o.entry.ID, Season: sql.NullInt64{Int64: sn, Valid: true}, Episode: sql.NullInt64{Int64: ep, Valid: true},
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := o.q.DeleteWatch(o.ctx, w.ID); err != nil {
			return err
		}
		es, has, err := o.entrySeason(sn)
		if err != nil {
			return err
		}
		if has && es.Status == Completed {
			remaining, err := o.latestWatchedPosition(sn)
			if err != nil {
				return err
			}
			pos, err := o.position(sn, ep)
			if err != nil {
				return err
			}
			if remaining < pos {
				if err := o.putSeason(sn, InProgress); err != nil {
					return err
				}
				es.Status = InProgress
			}
		}
		if has && es.Status != Completed && o.entry.Status == Completed {
			o.setStatus(InProgress)
		}
		return o.refreshShow()
	})
}

// StepSeason is the +1/-1 button of a season: up watches the first episode
// after the highest one watched, down unwatches the highest one watched
// (manual only).
func (s *Service) StepSeason(ctx context.Context, userID, itemID int64, season int, up bool, at time.Time) error {
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	if !catalog.HasEpisodes(item) {
		return ErrNotShow
	}
	if err := s.Catalog.SyncSeasons(ctx, item); err != nil {
		slog.WarnContext(ctx, "library: season sync failed", "item", itemID, "err", err)
	}
	eps, err := s.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: itemID, Season: int64(season)})
	if err != nil {
		return err
	}
	e, err := s.CurrentEntry(ctx, userID, itemID)
	if err != nil && !errors.Is(err, ErrNoEntry) {
		return err
	}
	watched := map[int64]bool{}
	if err == nil {
		ws, err := s.Q.ListSeasonWatches(ctx, db.ListSeasonWatchesParams{EntryID: e.ID, Season: sql.NullInt64{Int64: int64(season), Valid: true}})
		if err != nil {
			return err
		}
		for _, w := range ws {
			watched[w.Episode.Int64] = true
		}
	}
	highest := -1
	for i, ep := range eps {
		if watched[ep.Number] {
			highest = i
		}
	}
	if up {
		if highest+1 >= len(eps) {
			return nil
		}
		_, err := s.WatchEpisode(ctx, userID, itemID, season, int(eps[highest+1].Number), at, SourceManual, WatchOptions{})
		return err
	}
	if highest < 0 {
		return nil
	}
	return s.UnwatchEpisode(ctx, userID, itemID, season, int(eps[highest].Number))
}

// SetSeasonStatus changes the status of one season of a show, creating the
// season (and the show entry) when missing.
//
// Manual changes port Season.save: Completed marks every released episode
// not yet covered as watched and then completes the season, which starts the
// next season or completes the show; when unreleased episodes remain the
// season stays In progress instead. Dropped drops the show too (not for
// specials), In progress moves the show to In progress. Automated changes
// only move a season forward and have no side effects.
func (s *Service) SetSeasonStatus(ctx context.Context, userID, itemID int64, season int, status, source string, at time.Time) error {
	if !validStatus(status) {
		return fmt.Errorf("%w: status %q", ErrInvalidInput, status)
	}
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	if !catalog.HasEpisodes(item) {
		return ErrNotShow
	}
	if err := s.Catalog.SyncSeasons(ctx, item); err != nil {
		slog.WarnContext(ctx, "library: season sync failed", "item", itemID, "err", err)
	}
	return s.run(ctx, userID, itemID, source, at, func(o *op) error {
		if _, err := o.ensure(Planning); err != nil {
			return err
		}
		sn := int64(season)
		es, has, err := o.entrySeason(sn)
		if err != nil {
			return err
		}
		if o.auto {
			if !has {
				return o.putSeason(sn, status)
			}
			if canAdvance(es.Status, status) {
				return o.putSeason(sn, status)
			}
			return nil
		}
		switch status {
		case Completed:
			target, remaining, err := o.completionStatus(sn, InProgress)
			if err != nil {
				return err
			}
			if err := o.addWatches(sn, remaining); err != nil {
				return err
			}
			if err := o.putSeason(sn, target); err != nil {
				return err
			}
			if target == Completed {
				if sn > 0 {
					if err := o.handleCompletedSeason(sn); err != nil {
						return err
					}
				}
			} else if o.entry.Status != InProgress {
				o.setStatus(InProgress)
			}
		case Dropped:
			if err := o.putSeason(sn, Dropped); err != nil {
				return err
			}
			if sn != 0 && o.entry.Status != Dropped {
				o.setStatus(Dropped)
			}
		case InProgress:
			if err := o.putSeason(sn, InProgress); err != nil {
				return err
			}
			if o.entry.Status != InProgress {
				o.setStatus(InProgress)
			}
		default:
			if err := o.putSeason(sn, status); err != nil {
				return err
			}
		}
		return o.refreshShow()
	})
}

// SetSeasonScore sets the score of one season; nil clears it.
func (s *Service) SetSeasonScore(ctx context.Context, userID, itemID int64, season int, score *float64) error {
	var v sql.NullFloat64
	if score != nil {
		if *score < 0 || *score > 10 {
			return fmt.Errorf("%w: score %v", ErrInvalidInput, *score)
		}
		v = sql.NullFloat64{Float64: float64(int(*score*10+0.5)) / 10, Valid: true}
	}
	return s.updateSeason(ctx, userID, itemID, season, func(es *db.EntrySeason) { es.Score = v })
}

// SetSeasonNotes sets the notes of one season.
func (s *Service) SetSeasonNotes(ctx context.Context, userID, itemID int64, season int, notes string) error {
	return s.updateSeason(ctx, userID, itemID, season, func(es *db.EntrySeason) { es.Notes = notes })
}

func (s *Service) updateSeason(ctx context.Context, userID, itemID int64, season int, fn func(*db.EntrySeason)) error {
	return s.run(ctx, userID, itemID, SourceManual, time.Time{}, func(o *op) error {
		if _, err := o.ensure(Planning); err != nil {
			return err
		}
		es, _, err := o.entrySeason(int64(season))
		if err != nil {
			return err
		}
		if es.Status == "" {
			es.Status = Planning
		}
		fn(&es)
		return o.q.UpsertEntrySeason(o.ctx, db.UpsertEntrySeasonParams{
			EntryID: o.entry.ID, Season: int64(season), Status: es.Status, Score: es.Score, Notes: es.Notes,
		})
	})
}

// SetWatchNotes sets the note of one play (an episode or a movie watch).
func (s *Service) SetWatchNotes(ctx context.Context, userID, watchID int64, notes string) error {
	w, err := s.Q.GetWatchForUser(ctx, db.GetWatchForUserParams{ID: watchID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoEntry
	}
	if err != nil {
		return err
	}
	return s.Q.UpdateWatchNotes(ctx, db.UpdateWatchNotesParams{Notes: notes, ID: w.ID})
}

// completeShow ports TV._completed (manual only): every regular season gets
// the status its released episodes allow (Planning when nothing has aired),
// the released episodes not yet covered are marked watched, and the show
// stays In progress when anything remains unreleased.
func (o *op) completeShow() error {
	seasons, err := o.q.ListItemSeasons(o.ctx, o.item.ID)
	if err != nil {
		return err
	}
	var touched []int64
	allDone := true
	targets := map[int64][]db.ItemEpisode{}
	for _, sd := range seasons {
		if sd.Number == 0 {
			continue
		}
		target, remaining, err := o.completionStatus(sd.Number, Planning)
		if err != nil {
			return err
		}
		es, has, err := o.entrySeason(sd.Number)
		if err != nil {
			return err
		}
		if !has || es.Status != target {
			if err := o.putSeason(sd.Number, target); err != nil {
				return err
			}
			touched = append(touched, sd.Number)
			targets[sd.Number] = remaining
		}
		if target != Completed {
			allDone = false
		}
	}
	sort.Slice(touched, func(i, j int) bool { return touched[i] < touched[j] })
	for _, n := range touched {
		if err := o.addWatches(n, targets[n]); err != nil {
			return err
		}
	}
	if !allDone {
		o.setStatus(InProgress)
	}
	return nil
}

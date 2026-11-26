package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// AssumedShow is a migrated show the assumed-earlier-seasons rule applies to.
type AssumedShow struct {
	EntryID, ItemID int64
	Title, Kind     string
	// FirstSeason is the first regular season the owner has watches in.
	FirstSeason int
	// Seasons are the earlier seasons assumed watched (the candidates, for a
	// show that is deferred).
	Seasons  []int
	Episodes int
}

// AssumedResult is what ApplyAssumed did.
type AssumedResult struct {
	Assumed  []AssumedShow
	Deferred []AssumedShow
	Episodes int
}

// assumedCandidates finds the shows the rule applies to. A migrated show
// started mid-series when its first watched regular season N is above 1 and
// it has no watch at all in seasons 1..N-1 (nor an earlier season the owner
// already marked Completed). A show with any watch in an earlier season is
// never touched; neither are shows that are not TMDB-structured (count-based
// anime), movies, or specials. Django did not record where a play came from,
// so the first-play-from-Jellyfin-or-import condition cannot be checked and
// every migrated play counts as an import.
func assumedCandidates(ctx context.Context, q querier) ([]AssumedShow, error) {
	rows, err := queryRows(ctx, q, `
		SELECT e.id AS entry_id, i.id AS item_id, i.title, i.kind,
		       (SELECT MIN(season) FROM watches w WHERE w.entry_id = e.id AND w.season > 0 AND w.source != 'assumed') AS first
		FROM entries e JOIN items i ON i.id = e.item_id
		WHERE i.kind IN ('tv', 'anime') AND i.source = 'tmdb'
		  AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
		ORDER BY i.title, e.id`)
	if err != nil {
		return nil, err
	}
	var out []AssumedShow
	for _, r := range rows {
		first := int(r.i64("first"))
		if r.null("first") || first <= 1 {
			continue
		}
		var earlier int
		if err := scanOne(ctx, q, &earlier, `SELECT
			(SELECT COUNT(*) FROM watches WHERE entry_id = ? AND season > 0 AND season < ?) +
			(SELECT COUNT(*) FROM entry_seasons WHERE entry_id = ? AND season > 0 AND season < ? AND status = 'Completed')`,
			r.i64("entry_id"), first, r.i64("entry_id"), first); err != nil {
			return nil, err
		}
		if earlier > 0 {
			continue
		}
		a := AssumedShow{EntryID: r.i64("entry_id"), ItemID: r.i64("item_id"), Title: r.str("title"), Kind: r.str("kind"), FirstSeason: first}
		for n := 1; n < first; n++ {
			a.Seasons = append(a.Seasons, n)
		}
		out = append(out, a)
	}
	return out, nil
}

func scanOne(ctx context.Context, q querier, dst *int, query string, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(dst)
	}
	return rows.Err()
}

// ApplyAssumed applies the rule to the migrated shows: every released
// episode of the earlier seasons gets a watch with source "assumed", dated by
// its air date and never after the show's first real play, and the season
// becomes Completed (like library.assumeEarlierSeasons does for a show the
// Jellyfin webhook sees first at season N). Assumed watches count for
// progress and completion but not for activity, year views or entry dates.
//
// Episode data comes from item_episodes; with a SyncSeasons hook the
// show's TMDB seasons are fetched first. A show with no episode data for the
// earlier seasons is returned in Deferred and left untouched, so the step can
// be rerun later (it is idempotent: a show with assumed watches in earlier
// seasons no longer qualifies).
func ApplyAssumed(ctx context.Context, opts Options) (*AssumedResult, error) {
	opts.defaults()
	cands, err := assumedCandidates(ctx, opts.Target)
	if err != nil {
		return nil, err
	}
	res := &AssumedResult{}
	today := opts.Now().In(opts.Loc).Format(time.DateOnly)
	q := db.New(opts.Target)
	for _, c := range cands {
		if opts.SyncSeasons != nil {
			if err := opts.SyncSeasons(ctx, c.ItemID); err != nil && !errors.Is(err, ErrSkip) {
				opts.Log.Warn("assumed seasons: season sync failed", "item", c.ItemID, "err", err)
			}
		}
		eps := map[int][]db.ItemEpisode{}
		total := 0
		for _, n := range c.Seasons {
			list, err := q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: c.ItemID, Season: int64(n)})
			if err != nil {
				return nil, err
			}
			eps[n] = list
			total += len(list)
		}
		if total == 0 {
			res.Deferred = append(res.Deferred, c)
			continue
		}
		done, err := assumeShow(ctx, opts.Target, c, eps, today)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Title, err)
		}
		if done.Episodes == 0 {
			res.Deferred = append(res.Deferred, c)
			continue
		}
		res.Assumed = append(res.Assumed, done)
		res.Episodes += done.Episodes
	}
	return res, nil
}

func assumeShow(ctx context.Context, conn *sql.DB, c AssumedShow, eps map[int][]db.ItemEpisode, today string) (AssumedShow, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback() //nolint:errcheck
	var firstReal sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MIN(watched_at) FROM watches WHERE entry_id = ? AND source != ?`, c.EntryID, library.SourceAssumed).Scan(&firstReal); err != nil {
		return c, err
	}
	latest := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339, firstReal.String); err == nil {
		latest = t.Add(-time.Second)
	}
	out := c
	out.Seasons, out.Episodes = nil, 0
	for _, n := range c.Seasons {
		var status sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT status FROM entry_seasons WHERE entry_id = ? AND season = ?`, c.EntryID, n).Scan(&status)
		if err != nil && err != sql.ErrNoRows {
			return c, err
		}
		if status.Valid && status.String != library.Planning && status.String != library.InProgress {
			continue // the owner parked or finished it; leave it alone
		}
		released := catalog.ReleasedEpisodes(eps[n], today)
		added := 0
		for _, e := range eps[n] {
			if !released[e.Number] {
				continue
			}
			at := latest
			if e.AirDate.Valid {
				if day, err := time.Parse(time.DateOnly, e.AirDate.String); err == nil {
					if noon := day.Add(12 * time.Hour); noon.Before(at) {
						at = noon
					}
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO watches (entry_id, season, episode, watched_at, source, notes) VALUES (?, ?, ?, ?, ?, '')`,
				c.EntryID, n, e.Number, stampOf(at), library.SourceAssumed); err != nil {
				return c, err
			}
			added++
		}
		if added == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entry_seasons (entry_id, season, status, notes) VALUES (?, ?, 'Completed', '')
			ON CONFLICT (entry_id, season) DO UPDATE SET status = 'Completed'`, c.EntryID, n); err != nil {
			return c, err
		}
		out.Seasons = append(out.Seasons, n)
		out.Episodes += added
	}
	if out.Episodes > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE entries SET progress = (SELECT COUNT(*) FROM (SELECT DISTINCT season, episode FROM watches WHERE entry_id = ? AND season > 0))
			WHERE id = ?`, c.EntryID, c.EntryID); err != nil {
			return c, err
		}
	}
	return out, tx.Commit()
}

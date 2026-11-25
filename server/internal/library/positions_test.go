package library

import (
	"database/sql"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
)

// Ported from test_import_anilist_shows.py: episodes are marked by position, so a
// season TMDB numbers continuously (S2 starting at 33) gets the right episodes,
// a rerun adds nothing, and nothing already tracked is lowered.
func TestWatchPositionsOnContinuousSeason(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Shippuden", season(1, "2007-01-01", 1, 32), season(2, "2009-01-01", 33, 3))
	dates := func(e db.ItemEpisode) time.Time { return t0.Add(-time.Duration(e.Number) * time.Hour) }

	n, err := f.svc.WatchPositions(ctx, f.user, item.ID, 2, []int{1, 2, 99, 0}, dates, "anilist")
	if err != nil || n != 2 {
		t.Fatalf("added %d, err %v", n, err)
	}
	ws, _ := f.env.Q.ListSeasonWatches(ctx, db.ListSeasonWatchesParams{EntryID: f.entry(item).ID, Season: sql.NullInt64{Int64: 2, Valid: true}})
	if len(ws) != 2 || ws[0].Episode.Int64 != 33 || ws[1].Episode.Int64 != 34 {
		t.Fatalf("watched episodes = %+v", ws)
	}
	eq(t, "watch date is the import's", ws[0].WatchedAt, dates(db.ItemEpisode{Number: 33}).Format(time.RFC3339))
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: InProgress}) // no S1 invented
	eq(t, "show", f.entry(item).Status, InProgress)

	// Re-running with more progress adds only the new episode, and finishing completes the season.
	n, _ = f.svc.WatchPositions(ctx, f.user, item.ID, 2, []int{1, 2, 3}, dates, "anilist")
	eq(t, "added on rerun", n, 1)
	n, _ = f.svc.WatchPositions(ctx, f.user, item.ID, 2, []int{1, 2, 3}, dates, "anilist")
	eq(t, "added on second rerun", n, 0)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: Completed})
	eq(t, "rows", f.watchCount(item), 3)

	// A lower AniList count later never removes anything.
	n, _ = f.svc.WatchPositions(ctx, f.user, item.ID, 2, []int{1}, dates, "anilist")
	eq(t, "lower count adds", n, 0)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: Completed})
}

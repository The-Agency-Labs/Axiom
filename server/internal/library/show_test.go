package library

import (
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// Ported from app/tests/models/test_episode.py: the finale completes the
// season, the next released season starts, the last one completes the show.
func TestManualWatchProgressesSeasonsAndShow(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 3), season(2, "2021-01-01", 1, 2))

	res := f.watch(item, 1, 1, SourceManual)
	if !res.EntryCreated || !res.SeasonCreated {
		t.Errorf("first watch: created=%v season=%v", res.EntryCreated, res.SeasonCreated)
	}
	eq(t, "show after S1E1", f.entry(item).Status, InProgress)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: InProgress})

	f.watch(item, 1, 2, SourceManual)
	eq(t, "show after middle episode", f.entry(item).Status, InProgress)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: InProgress})

	res = f.watch(item, 1, 3, SourceManual) // S1 finale
	if !res.SeasonCompleted {
		t.Error("finale did not complete the season")
	}
	eq(t, "show after S1 finale", f.entry(item).Status, InProgress)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed, 2: InProgress}) // S2 started

	f.watch(item, 2, 1, SourceManual)
	f.watch(item, 2, 2, SourceManual) // last finale
	e := f.entry(item)
	eq(t, "show after last finale", e.Status, Completed)
	eq(t, "progress", e.Progress, int64(5))
	if !e.FinishedAt.Valid || !e.StartedAt.Valid {
		t.Errorf("dates not set: started=%v finished=%v", e.StartedAt, e.FinishedAt)
	}
}

func TestFutureNextSeasonKeepsShowInProgress(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 1), season(2, "2999-01-01", 1, 2))
	f.watch(item, 1, 1, SourceManual)
	eq(t, "show", f.entry(item).Status, Completed) // an unaired season doesn't block completion
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed})

	// A planned (tracked) season that exists blocks completion, as in Django.
	f2 := newFixture(t)
	item2 := f2.show("100", "Show", season(1, "2020-01-01", 1, 2), season(2, "2999-01-01", 1, 2))
	f2.watch(item2, 1, 1, SourceManual)
	if err := f2.svc.SetSeasonStatus(ctx, f2.user, item2.ID, 2, Planning, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	f2.watch(item2, 1, 2, SourceManual)
	eq(t, "show with tracked unaired season", f2.entry(item2).Status, InProgress)
	eqSeasons(t, f2.seasonStatuses(item2), map[int64]string{1: Completed, 2: Planning})
}

// Owner rule: rewatching, rewinding or re-scrubbing never lowers progress,
// un-watches an episode, or reopens a completed show.
func TestAutomatedPlaysNeverUndoCompletion(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 3), season(2, "2021-01-01", 1, 2))
	for _, p := range [][2]int{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}} {
		f.watch(item, p[0], p[1], "jellyfin")
	}
	e := f.entry(item)
	eq(t, "show", e.Status, Completed)
	progress := e.Progress

	tests := []struct {
		name    string
		s, ep   int
		options WatchOptions
	}{
		{"rewatch a middle episode", 1, 2, WatchOptions{}},
		{"rewatch the first episode", 1, 1, WatchOptions{}},
		{"rewatch a finale", 2, 2, WatchOptions{}},
		{"scrub an old season", 1, 3, WatchOptions{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f.watch(item, tc.s, tc.ep, "jellyfin", tc.options)
			e := f.entry(item)
			eq(t, "show status", e.Status, Completed)
			eq(t, "progress", e.Progress, progress)
			eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed, 2: Completed})
		})
	}
	eq(t, "watch rows (rewatches are kept)", f.watchCount(item), 5+len(tests))
}

// A manual watch of a middle episode of a Completed season reopens it: the
// Django UI did, and manual actions may do anything it allowed.
func TestManualRewatchMayReopen(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 2))
	f.watch(item, 1, 1, SourceManual)
	f.watch(item, 1, 2, SourceManual)
	eq(t, "show", f.entry(item).Status, Completed)
	f.watch(item, 1, 1, SourceManual)
	eq(t, "show after manual rewatch", f.entry(item).Status, InProgress)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: InProgress})
}

// Import mode never logs an episode that is already watched.
func TestImportDoesNotDuplicate(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 3))
	first := f.watch(item, 1, 1, "import", WatchOptions{Import: true})
	if first.Skipped || first.Watch == nil {
		t.Fatal("first import play was skipped")
	}
	for i := 0; i < 3; i++ {
		res := f.watch(item, 1, 1, "import", WatchOptions{Import: true})
		if !res.Skipped || res.Watch != nil {
			t.Fatalf("import repeat %d not skipped: %+v", i, res)
		}
	}
	eq(t, "watch rows", f.watchCount(item), 1)
	// A different source importing the same episode is also skipped.
	if res := f.watch(item, 1, 1, "jellyfin", WatchOptions{Import: true}); !res.Skipped {
		t.Error("import from another source duplicated the episode")
	}
	// Without Import a later play is a rewatch and is recorded.
	f.watch(item, 1, 1, "jellyfin")
	eq(t, "watch rows after live rewatch", f.watchCount(item), 2)
}

func TestDuplicateWebhookPlaysAreOnePlay(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 3))
	at := t0.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := f.svc.WatchEpisode(ctx, f.user, item.ID, 1, 1, at.Add(time.Duration(i)*time.Second), "jellyfin", WatchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, "watch rows", f.watchCount(item), 1)
	// Manual plays are never merged.
	for i := 0; i < 2; i++ {
		if _, err := f.svc.WatchEpisode(ctx, f.user, item.ID, 1, 2, at, SourceManual, WatchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, "manual rows", f.watchCount(item), 3)
}

// Owner rule: a show first seen at season N does not create an empty season 1.
// Ported from test_webhooks_jellyfin.LaterSeasonFirstTests.
func TestShowFirstSeenAtLaterSeasonHasNoEmptySeasonOne(t *testing.T) {
	// An automated first sighting at season 2 assumes season 1 was watched
	// (see TestAssumedEarlierSeasons); it never leaves an empty season 1.
	// Through the manual path nothing is assumed.
	f2 := newFixture(t)
	item2 := f2.show("100", "Show", season(1, "2020-01-01", 1, 3), season(2, "2021-01-01", 1, 3))
	f2.watch(item2, 2, 1, SourceManual)
	eqSeasons(t, f2.seasonStatuses(item2), map[int64]string{2: InProgress})
	// Finishing season 2 doesn't invent season 1 either, and the show stays
	// open because season 1 has aired and was never completed.
	f2.watch(item2, 2, 3, SourceManual)
	eqSeasons(t, f2.seasonStatuses(item2), map[int64]string{2: Completed})
	eq(t, "show with an unwatched aired season", f2.entry(item2).Status, InProgress)
}

// Owner rule: a completed show only reopens for a season never watched before.
// Ported from test_webhooks_jellyfin.test_rewatch_keeps_completed_show_completed.
func TestCompletedShowReopensOnlyForNewSeason(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show",
		season(0, "2019-01-01", 1, 2),
		season(1, "2020-01-01", 1, 2),
		season(2, "2025-01-01", 1, 2)) // not aired yet at t0
	f.watch(item, 1, 1, "jellyfin")
	f.watch(item, 1, 2, "jellyfin")
	eq(t, "show", f.entry(item).Status, Completed)

	f.watch(item, 1, 2, "jellyfin")
	eq(t, "after rewatching a watched season", f.entry(item).Status, Completed)

	f.watch(item, 0, 1, "jellyfin") // a never-watched special does not count
	eq(t, "after a special", f.entry(item).Status, Completed)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{0: InProgress, 1: Completed})

	f.env.Advance(400 * 24 * time.Hour) // season 2 airs
	f.watch(item, 2, 1, "jellyfin")
	eq(t, "after a never-watched season", f.entry(item).Status, InProgress)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{0: InProgress, 1: Completed, 2: InProgress})
}

func TestAutomatedPlayLeavesPausedAndDroppedAlone(t *testing.T) {
	for _, status := range []string{Paused, Dropped} {
		t.Run(status, func(t *testing.T) {
			f := newFixture(t)
			item := f.show("100", "Show", season(1, "2020-01-01", 1, 3))
			f.watch(item, 1, 1, SourceManual)
			if err := f.svc.SetStatus(ctx, f.user, item.ID, status, SourceManual, time.Time{}); err != nil {
				t.Fatal(err)
			}
			f.watch(item, 1, 2, "jellyfin")
			eq(t, "show", f.entry(item).Status, status)
			f.watch(item, 1, 3, "jellyfin") // even the finale
			eq(t, "show after finale", f.entry(item).Status, status)
		})
	}
}

func TestManualUnwatch(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 3))
	for e := 1; e <= 3; e++ {
		f.watch(item, 1, e, SourceManual)
	}
	eq(t, "show", f.entry(item).Status, Completed)

	if err := f.svc.UnwatchEpisode(ctx, f.user, item.ID, 1, 3); err != nil {
		t.Fatal(err)
	}
	e := f.entry(item)
	eq(t, "show after unwatching the finale", e.Status, InProgress)
	eq(t, "progress", e.Progress, int64(2))
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: InProgress})
	if e.FinishedAt.Valid {
		t.Error("finished_at not cleared on reopen")
	}

	// Unwatching a repeat of a watched episode keeps everything.
	f.watch(item, 1, 3, SourceManual)
	f.watch(item, 1, 3, SourceManual)
	if err := f.svc.UnwatchEpisode(ctx, f.user, item.ID, 1, 3); err != nil {
		t.Fatal(err)
	}
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed})
	eq(t, "show", f.entry(item).Status, Completed)

	// Unwatching an episode never watched is a no-op.
	if err := f.svc.UnwatchEpisode(ctx, f.user, item.ID, 1, 99); err != nil {
		t.Fatal(err)
	}
	eq(t, "rows", f.watchCount(item), 3)
}

func TestSeasonCompletionWithUnreleasedEpisodes(t *testing.T) {
	airing := catalogtest.Season{Number: 1, AirDate: "2024-01-01", Episodes: append(
		catalogtest.Eps(1, 2, "2024-01-01"),     // aired
		catalogtest.Eps(3, 2, "2999-01-01")...)} // not yet
	f := newFixture(t)
	item := f.show("100", "Show", airing)

	if err := f.svc.SetSeasonStatus(ctx, f.user, item.ID, 1, Completed, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Only released episodes were marked; the season stays open.
	eq(t, "watch rows", f.watchCount(item), 2)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: InProgress})
	eq(t, "show", f.entry(item).Status, InProgress)

	// Watching the last released episode doesn't complete either.
	f2 := newFixture(t)
	item2 := f2.show("100", "Show", airing)
	f2.watch(item2, 1, 2, SourceManual)
	eqSeasons(t, f2.seasonStatuses(item2), map[int64]string{1: InProgress})

	// Nothing released at all: completing yields no watches and In progress is not
	// forced onto the show without episodes to watch.
	future := catalogtest.Season{Number: 1, AirDate: "2999-01-01", Episodes: catalogtest.Eps(1, 2, "2999-01-01")}
	f3 := newFixture(t)
	item3 := f3.show("100", "Show", future)
	if err := f3.svc.SetSeasonStatus(ctx, f3.user, item3.ID, 1, Completed, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "watch rows", f3.watchCount(item3), 0)
}

func TestShowCompletedMarksReleasedEpisodesAndKeepsAiringShowOpen(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show",
		season(1, "2020-01-01", 1, 3),
		catalogtest.Season{Number: 2, AirDate: "2024-01-01", Episodes: append(catalogtest.Eps(1, 2, "2024-01-01"), catalogtest.Eps(3, 1, "2999-01-01")...)},
		season(3, "2999-01-01", 1, 2))

	if err := f.svc.SetStatus(ctx, f.user, item.ID, Completed, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e := f.entry(item)
	eq(t, "show", e.Status, InProgress) // unreleased episodes/seasons remain
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed, 2: InProgress, 3: Planning})
	eq(t, "watch rows", f.watchCount(item), 3+2)
	eq(t, "progress", e.Progress, int64(5))

	// A fully aired show completes.
	f2 := newFixture(t)
	item2 := f2.show("100", "Show", season(1, "2020-01-01", 1, 3), season(2, "2021-01-01", 1, 2))
	f2.watch(item2, 1, 1, SourceManual)
	if err := f2.svc.SetStatus(ctx, f2.user, item2.ID, Completed, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "show", f2.entry(item2).Status, Completed)
	eqSeasons(t, f2.seasonStatuses(item2), map[int64]string{1: Completed, 2: Completed})
	eq(t, "rows", f2.watchCount(item2), 5)
}

func TestShowDroppedDropsInProgressSeasons(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 2), season(2, "2021-01-01", 1, 2))
	f.watch(item, 1, 1, SourceManual)
	f.watch(item, 1, 2, SourceManual) // S1 done, S2 started
	if err := f.svc.SetStatus(ctx, f.user, item.ID, Dropped, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "show", f.entry(item).Status, Dropped)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed, 2: Dropped})

	// Dropping a season drops the show (not for specials).
	f2 := newFixture(t)
	item2 := f2.show("100", "Show", season(0, "2019-01-01", 1, 1), season(1, "2020-01-01", 1, 2))
	f2.watch(item2, 1, 1, SourceManual)
	if err := f2.svc.SetSeasonStatus(ctx, f2.user, item2.ID, 0, Dropped, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "show after dropping specials", f2.entry(item2).Status, InProgress)
	if err := f2.svc.SetSeasonStatus(ctx, f2.user, item2.ID, 1, Dropped, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "show after dropping a season", f2.entry(item2).Status, Dropped)
}

func TestInProgressStartsNextAvailableSeason(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 2), season(2, "2021-01-01", 1, 2), season(3, "2999-01-01", 1, 2))
	if err := f.svc.SetStatus(ctx, f.user, item.ID, InProgress, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: InProgress}) // first released season only
}

// Continuous numbering: TMDB numbers some long runners' seasons continuously
// (season 2 starting at episode 33). Positions order a season, the finale is
// the last episode, and episode numbers are what watches store.
func TestContinuousEpisodeNumbering(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Long Runner", season(1, "2007-01-01", 1, 32), season(2, "2009-01-01", 33, 3))
	eps, err := f.env.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: item.ID, Season: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 || eps[0].Number != 33 || eps[0].Position != 1 || eps[2].Number != 35 || eps[2].Position != 3 {
		t.Fatalf("episodes = %+v", eps)
	}

	f.watch(item, 2, 33, SourceManual)
	f.watch(item, 2, 34, SourceManual)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: InProgress})
	res := f.watch(item, 2, 35, SourceManual)
	if !res.SeasonCompleted {
		t.Error("finale (episode 35, position 3) did not complete the season")
	}
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: Completed})

	// Unwatching the finale reopens (position-based comparison, not number-based).
	if err := f.svc.UnwatchEpisode(ctx, f.user, item.ID, 2, 35); err != nil {
		t.Fatal(err)
	}
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: InProgress})

	// Completing the season marks only what is missing, by position.
	if err := f.svc.SetSeasonStatus(ctx, f.user, item.ID, 2, Completed, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "watch rows", f.watchCount(item), 3) // 33, 34, 35 -> unwatch 35 -> complete adds 35 back
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{2: Completed})
}

func TestStepSeason(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 2))
	for i := 0; i < 3; i++ {
		f.env.Advance(time.Minute)
		if err := f.svc.StepSeason(ctx, f.user, item.ID, 1, true, f.env.Now()); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, "rows after 3 steps up (only 2 episodes)", f.watchCount(item), 2)
	eq(t, "show", f.entry(item).Status, Completed)
	if err := f.svc.StepSeason(ctx, f.user, item.ID, 1, false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "rows after step down", f.watchCount(item), 1)
	eq(t, "show after step down", f.entry(item).Status, InProgress)
}

func TestAutomatedSeasonStatusOnlyMovesForward(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 2))
	steps := []struct {
		to   string
		want string
	}{
		{Planning, Planning},
		{InProgress, InProgress},
		{Planning, InProgress},
		{Completed, Completed},
		{InProgress, Completed},
		{Dropped, Completed},
	}
	for _, s := range steps {
		if err := f.svc.SetSeasonStatus(ctx, f.user, item.ID, 1, s.to, "anilist", time.Time{}); err != nil {
			t.Fatal(err)
		}
		eq(t, "season after "+s.to, f.seasonStatuses(item)[1], s.want)
	}
	eq(t, "no episodes were created by an automated completion", f.watchCount(item), 0)
}

// Owner rule: a show first seen by Jellyfin (or its history import) at
// season N was watched up to N-1 before Encompass existed. Earlier regular
// seasons are filled in as "assumed" watches dated by air date (never after
// the first real play), and those seasons are Completed. Specials are not
// assumed, a show already tracked is never back-filled, and assumed watches
// stay out of activity and the entry's dates.
func TestAssumedEarlierSeasons(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show",
		season(0, "2019-06-01", 1, 2),
		season(1, "2020-01-01", 1, 3),
		season(2, "2021-01-01", 1, 2),
		season(3, "2022-01-01", 1, 2))
	f.watch(item, 3, 1, "jellyfin", WatchOptions{Import: true})
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed, 2: Completed, 3: InProgress})
	eq(t, "show", f.entry(item).Status, InProgress)

	var assumed, real int
	var latestAssumed string
	rows, err := f.env.Conn.Query(`SELECT source, watched_at FROM watches`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var src, at string
		if err := rows.Scan(&src, &at); err != nil {
			t.Fatal(err)
		}
		if src == SourceAssumed {
			assumed++
			if at > latestAssumed {
				latestAssumed = at
			}
		} else {
			real++
		}
	}
	rows.Close()
	if assumed != 5 || real != 1 {
		t.Fatalf("assumed=%d real=%d, want 5 and 1", assumed, real)
	}
	if !strings.HasPrefix(latestAssumed, "2021-01-0") {
		t.Fatalf("latest assumed watch %s, want season 2 air dates", latestAssumed)
	}
	if e := f.entry(item); e.StartedAt.Valid && !strings.HasPrefix(e.StartedAt.String, t0.Format("2006-01-02")) {
		t.Fatalf("started_at %s should come from the real play, not assumed ones", e.StartedAt.String)
	}

	// Finishing season 3 completes the show: every aired season is done.
	f.watch(item, 3, 2, "jellyfin")
	eq(t, "show", f.entry(item).Status, Completed)

	// A show already tracked is never back-filled.
	f2 := newFixture(t)
	item2 := f2.show("200", "Other", season(1, "2020-01-01", 1, 2), season(2, "2021-01-01", 1, 2))
	f2.watch(item2, 2, 1, SourceManual)
	f2.watch(item2, 2, 2, "jellyfin")
	eqSeasons(t, f2.seasonStatuses(item2), map[int64]string{2: Completed})
}

// Caught up on a show that is still airing isn't finished: the show stays In
// progress (its seasons still complete) until the show itself has ended.
func TestCaughtUpOnAiringShowStaysInProgress(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(1, "2020-01-01", 1, 2))
	if _, err := f.env.Conn.ExecContext(ctx, `UPDATE items SET meta = json_set(meta, '$.details.status', 'Continuing') WHERE id = ?`, item.ID); err != nil {
		t.Fatal(err)
	}
	f.watch(item, 1, 1, SourceManual)
	f.watch(item, 1, 2, SourceManual) // finale of the only aired season
	eq(t, "airing show", f.entry(item).Status, InProgress)
	eqSeasons(t, f.seasonStatuses(item), map[int64]string{1: Completed})

	f2 := newFixture(t)
	ended := f2.show("101", "Ended", season(1, "2020-01-01", 1, 1))
	if _, err := f2.env.Conn.ExecContext(ctx, `UPDATE items SET meta = json_set(meta, '$.details.status', 'Ended') WHERE id = ?`, ended.ID); err != nil {
		t.Fatal(err)
	}
	f2.watch(ended, 1, 1, SourceManual)
	eq(t, "ended show", f2.entry(ended).Status, Completed)
}

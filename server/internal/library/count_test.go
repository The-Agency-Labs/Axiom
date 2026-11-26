package library

import (
	"database/sql"
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

// countItem inserts a count-based item (manga, game, ...) with a total.
func (f *fixture) countItem(kind, source, id string, total int) Item {
	f.t.Helper()
	item, err := f.env.Q.InsertItem(ctx, db.InsertItemParams{
		Kind: kind, Source: source, ExternalID: id, Title: kind + " " + id, Meta: "{}",
		Total: sql.NullInt64{Int64: int64(total), Valid: total > 0},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f *fixture) events(item Item) []string {
	f.t.Helper()
	evs, err := f.env.Q.ListEntryEvents(ctx, f.entry(item).ID)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Field+":"+e.Old.String+">"+e.New.String)
	}
	return out
}

func TestSetProgressRules(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		steps      []int
		wantProg   int64
		wantStatus string
	}{
		{"manual raises and starts", SourceManual, []int{3}, 3, InProgress},
		{"manual reaching the total completes", SourceManual, []int{3, 10}, 10, Completed},
		{"manual lowering is allowed", SourceManual, []int{5, 2}, 2, InProgress},
		{"manual lowering reopens a completed entry", SourceManual, []int{10, 4}, 4, InProgress},
		{"progress is clamped to the total", SourceManual, []int{99}, 10, Completed},
		{"automated raises", "anilist", []int{4}, 4, InProgress},
		{"automated never lowers progress", "anilist", []int{6, 2}, 6, InProgress},
		{"automated never reopens a completed entry", "anilist", []int{10, 3}, 10, Completed},
		{"automated zero keeps planning", "anilist", []int{0}, 0, Planning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			item := f.countItem("manga", "mal", "1", 10)
			for _, p := range tc.steps {
				f.env.Advance(time.Minute)
				if err := f.svc.SetProgress(ctx, f.user, item.ID, p, tc.source, f.env.Now()); err != nil {
					t.Fatal(err)
				}
			}
			e := f.entry(item)
			eq(t, "progress", e.Progress, tc.wantProg)
			eq(t, "status", e.Status, tc.wantStatus)
		})
	}
}

func TestSetProgressKeepsPausedAndDropped(t *testing.T) {
	for _, status := range []string{Paused, Dropped} {
		f := newFixture(t)
		item := f.countItem("book", "hardcover", "1", 300)
		if _, err := f.svc.Track(ctx, f.user, item.ID, status, SourceManual, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.SetProgress(ctx, f.user, item.ID, 50, "koreader", time.Time{}); err != nil {
			t.Fatal(err)
		}
		e := f.entry(item)
		eq(t, status+" status", e.Status, status)
		eq(t, status+" progress", e.Progress, int64(50))
	}
}

func TestStep(t *testing.T) {
	f := newFixture(t)
	manga := f.countItem("manga", "mal", "1", 3)
	game := f.countItem("game", "igdb", "1", 0)
	for i := 0; i < 4; i++ {
		if err := f.svc.Step(ctx, f.user, manga.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	e := f.entry(manga)
	eq(t, "manga progress (capped)", e.Progress, int64(3))
	eq(t, "manga status", e.Status, Completed)
	if err := f.svc.Step(ctx, f.user, manga.ID, false); err != nil {
		t.Fatal(err)
	}
	e = f.entry(manga)
	eq(t, "manga after down", e.Progress, int64(2))
	eq(t, "manga status after down", e.Status, InProgress)

	for i := 0; i < 3; i++ {
		if err := f.svc.Step(ctx, f.user, game.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	eq(t, "game minutes", f.entry(game).Progress, int64(90))
	for i := 0; i < 5; i++ {
		_ = f.svc.Step(ctx, f.user, game.ID, false)
	}
	eq(t, "game minutes floor", f.entry(game).Progress, int64(0))

	show := f.show("100", "Show", season(1, "2020-01-01", 1, 2))
	if err := f.svc.Step(ctx, f.user, show.ID, true); err != ErrCountBased {
		t.Errorf("step on a show = %v, want ErrCountBased", err)
	}
}

func TestSetStatusCountBased(t *testing.T) {
	f := newFixture(t)
	item := f.countItem("manga", "mal", "1", 10)
	if err := f.svc.SetStatus(ctx, f.user, item.ID, Completed, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	e := f.entry(item)
	eq(t, "status", e.Status, Completed)
	eq(t, "progress set to the total", e.Progress, int64(10))
	if !e.FinishedAt.Valid {
		t.Error("finished_at not set")
	}
	want := []string{"created:>Completed", "progress:0>10"}
	got := f.events(item)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// Statuses only move forward for automated sources.
func TestAutomatedStatusProgression(t *testing.T) {
	steps := []struct{ to, want string }{
		{InProgress, InProgress},
		{Planning, InProgress},
		{Completed, Completed},
		{InProgress, Completed},
		{Planning, Completed},
		{Dropped, Completed},
	}
	f := newFixture(t)
	item := f.countItem("manga", "mal", "1", 10)
	if _, err := f.svc.Track(ctx, f.user, item.ID, Planning, "anilist", time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if err := f.svc.SetStatus(ctx, f.user, item.ID, s.to, "anilist", time.Time{}); err != nil {
			t.Fatal(err)
		}
		eq(t, "status after "+s.to, f.entry(item).Status, s.want)
	}
	// Statuses the user set stay.
	for _, status := range []string{Paused, Dropped} {
		item := f.countItem("manga", "mal", "p"+status, 10)
		if _, err := f.svc.Track(ctx, f.user, item.ID, status, SourceManual, time.Time{}); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.SetStatus(ctx, f.user, item.ID, Completed, "anilist", time.Time{}); err != nil {
			t.Fatal(err)
		}
		eq(t, status+" left alone", f.entry(item).Status, status)
	}
	// A new entry takes the imported status as is, even Paused.
	paused := f.countItem("manga", "mal", "new", 10)
	if err := f.svc.SetStatus(ctx, f.user, paused.ID, Paused, "anilist", time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "new imported entry", f.entry(paused).Status, Paused)
}

func TestSyncEntryIsAdditive(t *testing.T) {
	f := newFixture(t)
	item := f.countItem("manga", "mal", "1", 20)
	score := 8.0
	started := t0.Add(-48 * time.Hour)
	finished := t0.Add(-24 * time.Hour)
	changed, err := f.svc.SyncEntry(ctx, f.user, item.ID, SyncValues{Status: InProgress, Score: &score, Notes: "n", Progress: 5, StartedAt: &started}, "anilist", time.Time{})
	if err != nil || !changed {
		t.Fatalf("first sync: changed=%v err=%v", changed, err)
	}
	// A second import with less data changes nothing.
	lower := 3.0
	changed, err = f.svc.SyncEntry(ctx, f.user, item.ID, SyncValues{Status: Planning, Score: &lower, Notes: "other", Progress: 2}, "anilist", time.Time{})
	if err != nil || changed {
		t.Fatalf("repeat sync: changed=%v err=%v", changed, err)
	}
	e := f.entry(item)
	if e.Score.Float64 != 8 || e.Notes != "n" || e.Progress != 5 || e.Status != InProgress {
		t.Errorf("entry changed by a lower import: %+v", e)
	}
	// Moving forward works, and the finish date is only taken for completed entries.
	changed, err = f.svc.SyncEntry(ctx, f.user, item.ID, SyncValues{Status: Completed, Progress: 20, FinishedAt: &finished}, "anilist", time.Time{})
	if err != nil || !changed {
		t.Fatalf("forward sync: changed=%v err=%v", changed, err)
	}
	e = f.entry(item)
	eq(t, "status", e.Status, Completed)
	eq(t, "progress", e.Progress, int64(20))
	eq(t, "finished_at", e.FinishedAt.String, finished.Format(time.RFC3339))
}

func TestMovieWatches(t *testing.T) {
	f := newFixture(t)
	movie := f.movie("500", "Film")

	play := func(source string, opts WatchOptions) WatchResult {
		f.env.Advance(time.Hour)
		res, err := f.svc.WatchMovie(ctx, f.user, movie.ID, f.env.Now(), source, opts)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := play("jellyfin", WatchOptions{})
	if !res.EntryCreated || res.Watch == nil {
		t.Fatalf("first play: %+v", res)
	}
	e := f.entry(movie)
	eq(t, "status", e.Status, Completed)
	eq(t, "progress", e.Progress, int64(1))
	eq(t, "run", e.Run, int64(1))

	// An import never logs a rewatch.
	if res := play("jellyfin", WatchOptions{Import: true}); !res.Skipped {
		t.Error("import of a completed movie was not skipped")
	}
	eq(t, "run after import", f.entry(movie).Run, int64(1))

	// A webhook retry of the same play is one play.
	f.env.Advance(-time.Hour)
	if res, _ := f.svc.WatchMovie(ctx, f.user, movie.ID, f.env.Now().Add(2*time.Second), "jellyfin", WatchOptions{}); !res.Skipped {
		t.Error("retried play was not skipped")
	}
	f.env.Advance(time.Hour)

	// Finishing it again is a rewatch: a new run.
	play("jellyfin", WatchOptions{})
	e = f.entry(movie)
	eq(t, "run after rewatch", e.Run, int64(2))
	eq(t, "status of the new run", e.Status, Completed)
	runs, _ := f.svc.Runs(ctx, f.user, movie.ID)
	eq(t, "runs", len(runs), 2)

	if _, err := f.svc.WatchMovie(ctx, f.user, f.countItem("manga", "mal", "9", 1).ID, time.Time{}, SourceManual, WatchOptions{}); err != ErrNotMovie {
		t.Errorf("WatchMovie on a manga = %v", err)
	}
}

func TestMovieStatusAutomatedStartThenFinish(t *testing.T) {
	f := newFixture(t)
	movie := f.movie("500", "Film")
	// A play started (not finished): In progress, never undone by later automation.
	if err := f.svc.SetStatus(ctx, f.user, movie.ID, InProgress, "jellyfin", time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "status", f.entry(movie).Status, InProgress)
	if _, err := f.svc.WatchMovie(ctx, f.user, movie.ID, time.Time{}, "jellyfin", WatchOptions{}); err != nil {
		t.Fatal(err)
	}
	e := f.entry(movie)
	eq(t, "completed in place", e.Status, Completed)
	eq(t, "run", e.Run, int64(1))
	if err := f.svc.SetStatus(ctx, f.user, movie.ID, InProgress, "jellyfin", time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "not reopened by a start event", f.entry(movie).Status, Completed)
}

func TestNewRunAndEvents(t *testing.T) {
	f := newFixture(t)
	book := f.countItem("book", "hardcover", "1", 100)
	if err := f.svc.SetProgress(ctx, f.user, book.ID, 100, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	eq(t, "status", f.entry(book).Status, Completed)
	e, err := f.svc.NewRun(ctx, f.user, book.ID, "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "run", e.Run, int64(2))
	eq(t, "status", e.Status, InProgress)
	eq(t, "progress of the new run", e.Progress, int64(0))
	if !e.StartedAt.Valid {
		t.Error("new run has no start date")
	}
	// Progress and score land on the current (new) run.
	if err := f.svc.SetProgress(ctx, f.user, book.ID, 10, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	score := 9.5
	if err := f.svc.SetScore(ctx, f.user, book.ID, &score, SourceManual); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.svc.Runs(ctx, f.user, book.ID)
	eq(t, "old run progress", runs[0].Progress, int64(100))
	eq(t, "new run progress", runs[1].Progress, int64(10))
	eq(t, "new run score", runs[1].Score.Float64, 9.5)
	got := f.events(book)
	want := []string{"created:>In progress", "progress:0>10", "score:>9.5"}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		eq(t, "event", got[i], want[i])
	}
	if err := f.svc.SetScore(ctx, f.user, book.ID, ptr(11.0), SourceManual); err == nil {
		t.Error("score 11 accepted")
	}
}

func ptr[T any](v T) *T { return &v }

func TestDatesNotesAndDelete(t *testing.T) {
	f := newFixture(t)
	book := f.countItem("book", "hardcover", "1", 100)
	if err := f.svc.SetNotes(ctx, f.user, book.ID, "x"); err != ErrNoEntry {
		t.Errorf("notes without an entry = %v", err)
	}
	if _, err := f.svc.Track(ctx, f.user, book.ID, Planning, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	start := t0.Add(-time.Hour)
	if err := f.svc.SetDates(ctx, f.user, book.ID, Some(start), Clear[time.Time](), SourceManual); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetNotes(ctx, f.user, book.ID, "great"); err != nil {
		t.Fatal(err)
	}
	e := f.entry(book)
	eq(t, "started", e.StartedAt.String, start.Format(time.RFC3339))
	eq(t, "notes", e.Notes, "great")
	if err := f.svc.Delete(ctx, f.user+1, e.ID); err != ErrNoEntry {
		t.Errorf("deleting another user's entry = %v", err)
	}
	if err := f.svc.Delete(ctx, f.user, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CurrentEntry(ctx, f.user, book.ID); err != ErrNoEntry {
		t.Errorf("entry after delete = %v", err)
	}
}

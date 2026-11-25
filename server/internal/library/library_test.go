package library

import (
	"context"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

// fixture is a library on a temp DB with a fake TMDB and a fixed clock
// (2024-06-01), one user, and helpers to add shows and read state.
type fixture struct {
	t    *testing.T
	env  *catalogtest.Env
	svc  *Service
	user int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	svc := New(env.Conn, env.Q, env.Catalog)
	svc.Now = env.Now
	return &fixture{t: t, env: env, svc: svc, user: env.User(t, "owner")}
}

// show adds a fake TMDB show and returns its item. Seasons are
// {number, air date, episodes}.
func (f *fixture) show(id, title string, seasons ...catalogtest.Season) Item {
	f.t.Helper()
	f.env.TMDB.AddShow(id, title, seasons...)
	item, err := f.env.Catalog.EnsureItem(ctx, "tv", "tmdb", id)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.env.Catalog.SyncSeasons(ctx, item, catalog.Force); err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f *fixture) movie(id, title string) Item {
	f.t.Helper()
	f.env.TMDB.AddMovie(id, title, "2020-01-01")
	item, err := f.env.Catalog.EnsureItem(ctx, "movie", "tmdb", id)
	if err != nil {
		f.t.Fatal(err)
	}
	return item
}

// season builds a fake season whose n episodes aired on date.
func season(number int, date string, first, n int) catalogtest.Season {
	return catalogtest.Season{Number: number, AirDate: date, Episodes: catalogtest.Eps(first, n, date)}
}

func (f *fixture) watch(item Item, s, e int, source string, opts ...WatchOptions) WatchResult {
	f.t.Helper()
	var o WatchOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	f.env.Advance(time.Minute)
	res, err := f.svc.WatchEpisode(ctx, f.user, item.ID, s, e, f.env.Now(), source, o)
	if err != nil {
		f.t.Fatalf("WatchEpisode S%dE%d: %v", s, e, err)
	}
	return res
}

func (f *fixture) entry(item Item) Entry {
	f.t.Helper()
	e, err := f.svc.CurrentEntry(ctx, f.user, item.ID)
	if err != nil {
		f.t.Fatalf("no entry: %v", err)
	}
	return e
}

func (f *fixture) seasonStatuses(item Item) map[int64]string {
	f.t.Helper()
	e := f.entry(item)
	rows, err := f.env.Q.ListEntrySeasons(ctx, e.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[int64]string{}
	for _, r := range rows {
		out[r.Season] = r.Status
	}
	return out
}

func (f *fixture) watchCount(item Item) int {
	f.t.Helper()
	ws, err := f.env.Q.ListEntryWatches(ctx, f.entry(item).ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return len(ws)
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func eqSeasons(t *testing.T, got, want map[int64]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("seasons = %v, want %v", got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("seasons = %v, want %v", got, want)
			return
		}
	}
}

package library

import (
	"testing"
	"time"
)

// A show with no season under way still gets a season card: the next season's
// first episode, or caught up when nothing further is dated.
func TestNowBetweenSeasons(t *testing.T) {
	f := newFixture(t)
	show := f.show("100", "Show", season(1, "2020-01-01", 1, 2), season(2, "2021-01-01", 1, 2))
	f.watch(show, 1, 1, SourceManual)
	f.watch(show, 1, 2, SourceManual)

	card := func() NowEntry {
		t.Helper()
		groups, err := f.svc.NowGroups(ctx, f.user)
		if err != nil || len(groups) != 1 || len(groups[0].Entries) != 1 {
			t.Fatalf("now = %+v %v", groups, err)
		}
		return groups[0].Entries[0]
	}
	next := card()
	if next.Season == nil || *next.Season != 2 || next.Next == nil || next.Next.Number != 1 {
		t.Fatalf("between seasons = %+v next %+v", next, next.Next)
	}

	// Once that season is under way it is the ordinary season card.
	f.watch(show, 2, 1, SourceManual)
	if c := card(); *c.Season != 2 || c.Next == nil || c.Next.Number != 2 {
		t.Fatalf("under way = %+v next %+v", c, c.Next)
	}
}

// Now is what happened lately: an In progress title with nothing in the last
// 30 days isn't on it, whatever dates its entry carries.
func TestNowKeepsToTheLast30Days(t *testing.T) {
	f := newFixture(t)
	old := f.countItem("manga", "mal", "1", 10)
	fresh := f.countItem("manga", "mal", "2", 10)
	if err := f.svc.SetProgress(ctx, f.user, old.ID, 3, SourceManual, f.svc.Now().Add(-NowWindow-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetProgress(ctx, f.user, fresh.ID, 3, SourceManual, f.svc.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	groups, err := f.svc.NowGroups(ctx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Entries) != 1 || groups[0].Entries[0].Item.ID != fresh.ID {
		t.Fatalf("now = %+v", groups)
	}
}

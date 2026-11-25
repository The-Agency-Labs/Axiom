package library

import (
	"testing"
	"time"
)

func TestListLibraryFiltersAndYears(t *testing.T) {
	f := newFixture(t)
	show := f.show("100", "Breaking Show", season(1, "2020-01-01", 1, 2))
	movie := f.movie("500", "Some Film")
	manga := f.countItem("manga", "mal", "1", 10)

	f.watch(show, 1, 1, SourceManual)
	if _, err := f.svc.WatchMovie(ctx, f.user, movie.ID, time.Date(2022, 3, 1, 12, 0, 0, 0, time.UTC), SourceManual, WatchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetProgress(ctx, f.user, manga.ID, 3, SourceManual, time.Date(2023, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetNotes(ctx, f.user, manga.ID, "secret plot"); err != nil {
		t.Fatal(err)
	}

	all, err := f.svc.ListLibrary(ctx, f.user, LibraryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "count", all.Count, 3)
	// Newest activity first: the show was watched "now" (2024), then manga (2023), then the film.
	eq(t, "first", all.Results[0].Item.Title, "Breaking Show")
	eq(t, "last", all.Results[2].Item.Title, "Some Film")
	if all.Results[0].MaxProgress == nil || *all.Results[0].MaxProgress != 2 {
		t.Errorf("show max progress = %v", all.Results[0].MaxProgress)
	}

	for _, tc := range []struct {
		name string
		f    LibraryFilter
		want []string
	}{
		{"kind", LibraryFilter{Kinds: []string{"movie"}}, []string{"Some Film"}},
		{"kinds", LibraryFilter{Kinds: []string{"movie", "manga"}}, []string{"manga 1", "Some Film"}},
		{"status", LibraryFilter{Status: Completed}, []string{"Some Film"}},
		{"search title", LibraryFilter{Search: "BREAKING"}, []string{"Breaking Show"}},
		{"search notes", LibraryFilter{Search: "plot"}, []string{"manga 1"}},
		{"year 2022", LibraryFilter{Year: ptr(2022)}, []string{"Some Film"}},
		{"year 2024", LibraryFilter{Year: ptr(2024)}, []string{"Breaking Show"}},
		{"year 2023", LibraryFilter{Year: ptr(2023)}, []string{"manga 1"}},
		{"paging", LibraryFilter{Limit: 1, Offset: 1}, []string{"manga 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.svc.ListLibrary(ctx, f.user, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range page.Results {
				got = append(got, r.Item.Title)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				eq(t, "result", got[i], tc.want[i])
			}
		})
	}
	// The year list covers every matching entry, not just the page.
	page, _ := f.svc.ListLibrary(ctx, f.user, LibraryFilter{Year: ptr(2022)})
	if len(page.Years) < 3 || page.Years[0] != 2024 || page.Years[len(page.Years)-1] != 2022 {
		t.Errorf("years = %v", page.Years)
	}
	years, _ := f.svc.Years(ctx, f.user)
	eq(t, "years", len(years), len(page.Years))
}

func TestEntryYears(t *testing.T) {
	today := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	mk := func(created, started, finished, progressed, status string) Entry {
		e := Entry{CreatedAt: created, Status: status}
		if started != "" {
			e.StartedAt.String, e.StartedAt.Valid = started, true
		}
		if finished != "" {
			e.FinishedAt.String, e.FinishedAt.Valid = finished, true
		}
		if progressed != "" {
			e.ProgressedAt.String, e.ProgressedAt.Valid = progressed, true
		}
		return e
	}
	tests := []struct {
		name string
		e    Entry
		want []int
	}{
		{"created only", mk("2021-05-01T00:00:00Z", "", "", "", Planning), []int{2021}},
		{"in progress runs to today", mk("2021-05-01T00:00:00Z", "", "", "", InProgress), []int{2021, 2022, 2023, 2024}},
		{"start to finish", mk("2024-01-01T00:00:00Z", "2020-12-30T00:00:00Z", "2022-01-02T00:00:00Z", "", Completed), []int{2020, 2021, 2022}},
		{"progressed", mk("2024-01-01T00:00:00Z", "2022-02-01T00:00:00Z", "", "2023-02-01T00:00:00Z", Paused), []int{2022, 2023}},
	}
	for _, tc := range tests {
		got := EntryYears(tc.e, today)
		if len(got) != len(tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			eq(t, tc.name, got[i], tc.want[i])
		}
	}
}

func TestEntryDetailAndHistory(t *testing.T) {
	f := newFixture(t)
	show := f.show("100", "Show", season(1, "2020-01-01", 1, 2), season(2, "2021-01-01", 1, 2))
	f.watch(show, 1, 1, SourceManual)
	f.watch(show, 1, 1, SourceManual) // a rewatch
	f.watch(show, 1, 2, SourceManual) // finale: S1 done, S2 started
	score := 8.5
	if err := f.svc.SetSeasonScore(ctx, f.user, show.ID, 1, &score); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetSeasonNotes(ctx, f.user, show.ID, 1, "great season"); err != nil {
		t.Fatal(err)
	}
	e := f.entry(show)
	d, err := f.svc.GetEntryDetail(ctx, f.user, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "seasons", len(d.Seasons), 2)
	s1 := d.Seasons[0]
	eq(t, "S1 status", s1.Status, Completed)
	eq(t, "S1 score", *s1.Score, 8.5)
	eq(t, "S1 notes", s1.Notes, "great season")
	eq(t, "S1 distinct watched", s1.Watched, 2)
	eq(t, "S1 plays listed (repeats included)", len(s1.Episodes), 3)
	eq(t, "S1 first episode title", s1.Episodes[0].Title, "Episode 1")
	eq(t, "S1 first episode repeat count", s1.Episodes[0].Watches, 2)
	eq(t, "S1 episode count", s1.EpisodeCount, 2)
	eq(t, "S2 status", d.Seasons[1].Status, InProgress)
	eq(t, "runs", len(d.Runs), 1)
	if len(d.History) == 0 || d.History[0].Type != "created" {
		t.Fatalf("history = %+v", d.History)
	}
	last := d.History[len(d.History)-1]
	eq(t, "history status", last.Status, InProgress)
	eq(t, "history progress", last.Progress, 2)
	if _, err := f.svc.GetEntryDetail(ctx, f.user+1, e.ID); err != ErrNoEntry {
		t.Errorf("another user's detail = %v", err)
	}

	movie := f.movie("500", "Film")
	if _, err := f.svc.WatchMovie(ctx, f.user, movie.ID, time.Time{}, SourceManual, WatchOptions{Notes: "popcorn"}); err != nil {
		t.Fatal(err)
	}
	md, _ := f.svc.GetEntryDetail(ctx, f.user, f.entry(movie).ID)
	if len(md.Watches) != 1 || md.Watches[0].Notes != "popcorn" {
		t.Errorf("movie watches = %+v", md.Watches)
	}
	if err := f.svc.SetWatchNotes(ctx, f.user, md.Watches[0].ID, "changed"); err != nil {
		t.Fatal(err)
	}
	md, _ = f.svc.GetEntryDetail(ctx, f.user, f.entry(movie).ID)
	eq(t, "watch note", md.Watches[0].Notes, "changed")
}

func TestShowAndSeasonViews(t *testing.T) {
	f := newFixture(t)
	item := f.show("100", "Show", season(0, "2019-01-01", 1, 2), season(1, "2020-01-01", 1, 4), season(2, "2999-01-01", 1, 3))

	v, err := f.svc.GetShowView(ctx, f.user, item)
	if err != nil {
		t.Fatal(err)
	}
	if v.Entry != nil {
		t.Error("untracked show has an entry")
	}
	// Regular seasons first, specials last.
	order := []int{}
	for _, s := range v.Seasons {
		order = append(order, s.Number)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Errorf("season order = %v", order)
	}

	f.watch(item, 1, 1, SourceManual)
	f.watch(item, 1, 2, SourceManual)
	f.watch(item, 0, 1, SourceManual)
	v, _ = f.svc.GetShowView(ctx, f.user, item)
	eq(t, "completion watched", v.Completion.Watched, 2)
	eq(t, "completion total", v.Completion.Total, 7)
	eq(t, "specials watched", v.Completion.SpecialsWatched, 1)
	eq(t, "seasons", v.Completion.Seasons, 2)
	eq(t, "seasons completed", v.Completion.SeasonsCompleted, 0)
	eq(t, "percent", *v.Completion.Percent, 28.6)
	eq(t, "S1 percent", *v.Seasons[0].Percent, 50.0)
	eq(t, "S1 entry status", v.Seasons[0].Entry.Status, InProgress)
	if v.Seasons[1].Entry != nil {
		t.Error("S2 has an entry")
	}

	sv, err := f.svc.GetSeasonView(ctx, f.user, item, 1)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "watched", sv.Watched, 2)
	eq(t, "total", sv.Total, 4)
	eq(t, "runtime", sv.RuntimeMinutes, 96)
	if !sv.Episodes[0].Watched || sv.Episodes[2].Watched || !sv.Episodes[0].Aired {
		t.Errorf("episodes = %+v", sv.Episodes)
	}
	eq(t, "kind", sv.Episodes[0].Kind, "standard")
	sv2, _ := f.svc.GetSeasonView(ctx, f.user, item, 2)
	if sv2.Episodes[0].Aired {
		t.Error("future episode aired")
	}
}

func TestNowGroupsAndFinished(t *testing.T) {
	f := newFixture(t)
	show := f.show("100", "Show", season(1, "2020-01-01", 1, 3))
	manga := f.countItem("manga", "mal", "1", 10)
	done := f.countItem("book", "hardcover", "2", 100)
	f.watch(show, 1, 2, SourceManual)
	if err := f.svc.SetProgress(ctx, f.user, manga.ID, 4, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetProgress(ctx, f.user, done.ID, 100, SourceManual, time.Time{}); err != nil {
		t.Fatal(err)
	}

	groups, err := f.svc.NowGroups(ctx, f.user, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "groups", len(groups), 2) // tv and manga; the finished book is not "now"
	var tv, mg NowEntry
	for _, g := range groups {
		switch g.Kind {
		case "tv":
			tv = g.Entries[0]
		case "manga":
			mg = g.Entries[0]
		}
	}
	if tv.Season == nil || *tv.Season != 1 {
		t.Fatalf("tv now entry = %+v", tv)
	}
	eq(t, "tv progress (episode number)", tv.Progress, 2)
	eq(t, "tv total (last episode number)", *tv.Total, 3)
	if tv.Next == nil || tv.Next.Number != 3 || tv.Next.Title != "Episode 3" {
		t.Errorf("tv next = %+v", tv.Next)
	}
	eq(t, "manga progress", mg.Progress, 4)
	if mg.Next == nil || mg.Next.Number != 5 {
		t.Errorf("manga next = %+v", mg.Next)
	}

	fin, err := f.svc.RecentlyFinished(ctx, f.user, 10)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "finished", len(fin), 1)
	eq(t, "finished title", fin[0].Item.Title, "book 2")
	fin, _ = f.svc.RecentlyFinished(ctx, f.user, 10, "movie")
	eq(t, "finished movies", len(fin), 0)
}

func TestActivityFeedPaging(t *testing.T) {
	f := newFixture(t)
	show := f.show("100", "Show", season(1, "2020-01-01", 1, 3))
	manga := f.countItem("manga", "mal", "1", 10)
	f.watch(show, 1, 1, SourceManual)
	f.watch(show, 1, 2, SourceManual)
	f.env.Advance(time.Minute)
	if err := f.svc.SetProgress(ctx, f.user, manga.ID, 3, SourceManual, f.env.Now()); err != nil {
		t.Fatal(err)
	}

	full, err := f.svc.Activity(ctx, f.user, ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	// manga: created + status + progress at one moment (one row, 3 changes... status then progress),
	// two episode plays, show creation and its progress changes.
	var kinds []string
	for _, a := range full.Entries {
		kinds = append(kinds, a.Kind)
	}
	if len(full.Entries) < 5 {
		t.Fatalf("feed = %v", kinds)
	}
	if full.Entries[0].Kind != "change" || full.Entries[0].Item.Title != "manga 1" || len(full.Entries[0].Changes) < 2 {
		t.Errorf("newest row = %+v", full.Entries[0])
	}
	// Walking the feed two rows at a time yields the same rows in the same order.
	var walked []ActivityEntry
	var cur *ActivityCursor
	for i := 0; i < 20; i++ {
		page, err := f.svc.Activity(ctx, f.user, ActivityQuery{Limit: 2, Cursor: cur})
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, page.Entries...)
		if page.Next == nil {
			break
		}
		cur = page.Next
	}
	if len(walked) != len(full.Entries) {
		for _, a := range full.Entries {
			t.Logf("full: %s %s e%d id%d %v", a.At, a.Kind, a.EntryID, a.Cursor().ID, a.Changes)
		}
		for _, a := range walked {
			t.Logf("walked: %s %s e%d id%d %v", a.At, a.Kind, a.EntryID, a.Cursor().ID, a.Changes)
		}
		t.Fatalf("walked %d rows, full feed has %d", len(walked), len(full.Entries))
	}
	for i := range walked {
		if walked[i].At != full.Entries[i].At || walked[i].Kind != full.Entries[i].Kind || walked[i].EntryID != full.Entries[i].EntryID {
			t.Errorf("row %d: %+v vs %+v", i, walked[i], full.Entries[i])
		}
	}
	// A date range keeps only what is inside.
	to := t0.Add(-time.Hour)
	empty, _ := f.svc.Activity(ctx, f.user, ActivityQuery{To: &to})
	eq(t, "rows before the start", len(empty.Entries), 0)
}

func TestProgressDates(t *testing.T) {
	f := newFixture(t)
	anime := f.countItem("anime", "mal", "1", 12)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	for _, step := range []struct{ p, h int }{{2, 1}, {3, 2}, {1, 3}, {4, 4}} {
		if err := f.svc.SetProgress(ctx, f.user, anime.ID, step.p, SourceManual, at(step.h)); err != nil {
			t.Fatal(err)
		}
	}
	dates, err := f.svc.ProgressDates(ctx, f.entry(anime).ID)
	if err != nil {
		t.Fatal(err)
	}
	// Episode 1 first reached at hour 1, 2 at hour 1 then removed by the drop to 1 and
	// re-reached at hour 4, 3 re-reached at hour 4.
	eq(t, "ep1", dates[1], at(1).Format(time.RFC3339))
	eq(t, "ep2", dates[2], at(4).Format(time.RFC3339))
	eq(t, "ep4", dates[4], at(4).Format(time.RFC3339))
}

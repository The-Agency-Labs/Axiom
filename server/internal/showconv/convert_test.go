package showconv

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/providers/tvdb"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

// dated builds n episodes numbered from first, one a week from start; ids are
// idBase+number (0 = no TVDB id).
func dated(first, n int, start string, idBase int) []catalogtest.Episode {
	d, _ := time.Parse(time.DateOnly, start)
	out := make([]catalogtest.Episode, n)
	for i := range out {
		num := first + i
		out[i] = catalogtest.Episode{Number: num, Name: fmt.Sprintf("Ep %d", num), Runtime: 24, AirDate: d.AddDate(0, 0, 7*i).Format(time.DateOnly)}
		if idBase != 0 {
			out[i].ID = int64(idBase + num)
		}
	}
	return out
}

func season(n int, eps []catalogtest.Episode) catalogtest.Season {
	return catalogtest.Season{Number: n, AirDate: eps[0].AirDate, Episodes: eps}
}

type fx struct {
	t    *testing.T
	env  *catalogtest.Env
	lib  *library.Service
	user int64
	cv   *Converter
}

// newFx builds a library on TMDB shows; call start() to switch the catalog
// to TVDB and get the converter.
func newFx(t *testing.T) *fx {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	lib := library.New(env.Conn, env.Q, env.Catalog)
	lib.Now = env.Now
	return &fx{t: t, env: env, lib: lib, user: env.User(t, "owner")}
}

func (f *fx) start() {
	f.env.UseTVDB(f.env.TVDB)
	f.cv = New(f.env.Conn, f.env.Q, f.env.Catalog)
	f.cv.Today = func() string { return "2024-06-01" }
}

// tmdbShow creates the TMDB item (synced) of a fake TMDB show.
func (f *fx) tmdbShow(id string) db.Item {
	f.t.Helper()
	item, err := f.env.Catalog.EnsureItem(ctx, "tv", "tmdb", id)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.env.Catalog.SyncSeasons(ctx, item, catalog.Force); err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f *fx) watch(item db.Item, s, e int, at time.Time, source string) {
	f.t.Helper()
	if _, err := f.lib.WatchEpisode(ctx, f.user, item.ID, s, e, at, source, library.WatchOptions{}); err != nil {
		f.t.Fatalf("watch S%dE%d: %v", s, e, err)
	}
}

func (f *fx) tvdbItem(id string) db.Item {
	f.t.Helper()
	for _, k := range []string{"tv", "anime"} {
		if it, err := f.env.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: "tvdb", ExternalID: id, Kind: k}); err == nil {
			return it
		}
	}
	f.t.Fatalf("no tvdb item %s", id)
	return db.Item{}
}

func (f *fx) entry(item db.Item) db.Entry {
	f.t.Helper()
	e, err := f.lib.CurrentEntry(ctx, f.user, item.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return e
}

func (f *fx) watches(e db.Entry) []db.Watch {
	f.t.Helper()
	ws, err := f.env.Q.ListEntryWatches(ctx, e.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return ws
}

func (f *fx) seasonStatuses(e db.Entry) map[int64]string {
	f.t.Helper()
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

func (f *fx) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.env.Conn.QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// friends: TMDB lumps 24 weekly episodes and a special into one season; TVDB has two seasons of 12.
func (f *fx) friends() {
	f.env.TMDB.AddShow("1668", "Friends",
		season(0, []catalogtest.Episode{{Number: 1, Name: "Recap", AirDate: "2020-02-01", Runtime: 5}}),
		season(1, dated(1, 24, "2020-01-06", 0)))
	f.env.TVDB.AddShow("7000", "Friends",
		season(0, []catalogtest.Episode{{ID: 70001, Number: 1, Name: "Recap", AirDate: "2020-02-01", Runtime: 5}}),
		season(1, dated(1, 12, "2020-01-06", 71000)),
		season(2, dated(1, 12, "2020-03-30", 72000)))
}

func TestConvertSplitsASeasonAndKeepsEveryWatch(t *testing.T) {
	f := newFx(t)
	f.friends()
	item := f.tmdbShow("1668")
	f.watch(item, 0, 1, t0.Add(-2*time.Hour), library.SourceManual)  // the special
	f.watch(item, 1, 99, t0.Add(-1*time.Hour), library.SourceManual) // an episode TMDB never listed
	for i := 1; i <= 24; i++ {
		f.watch(item, 1, i, t0.Add(time.Duration(i)*time.Hour), library.SourceManual)
	}
	before := f.entry(item)
	if before.Status != library.Completed {
		t.Fatalf("setup: show is %s", before.Status)
	}
	nBefore := len(f.watches(before))
	f.start()

	rep, err := f.cv.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Converted) != 1 || len(rep.Kept) != 0 {
		t.Fatalf("converted %d kept %d (%+v)", len(rep.Converted), len(rep.Kept), rep.Kept)
	}
	res := rep.Converted[0]
	if res.Seasons.Before != 1 || res.Seasons.After != 2 || res.TVDBID != "7000" {
		t.Errorf("seasons %+v tvdb %s", res.Seasons, res.TVDBID)
	}
	if rep.WatchesBefore != int64(nBefore) || rep.WatchesAfter != rep.WatchesBefore {
		t.Errorf("watches %d -> %d, want both %d", rep.WatchesBefore, rep.WatchesAfter, nBefore)
	}

	// The TMDB item is gone; the entry follows to the TVDB item with its status.
	if n := f.count(`SELECT count(*) FROM items WHERE source = 'tmdb'`); n != 0 {
		t.Errorf("%d TMDB items left", n)
	}
	tv := f.tvdbItem("7000")
	after := f.entry(tv)
	if after.ID != before.ID || after.Status != library.Completed || after.Notes != before.Notes || after.Score != before.Score {
		t.Errorf("entry changed: %+v -> %+v", before, after)
	}
	if after.StartedAt != before.StartedAt || after.FinishedAt != before.FinishedAt {
		t.Errorf("entry dates changed: %+v -> %+v", before, after)
	}
	ws := f.watches(after)
	if len(ws) != nBefore {
		t.Fatalf("watches = %d, want %d", len(ws), nBefore)
	}
	got := map[[2]int64]bool{}
	for _, w := range ws {
		got[[2]int64{w.Season.Int64, w.Episode.Int64}] = true
	}
	for _, k := range [][2]int64{{1, 1}, {1, 12}, {2, 1}, {2, 12}, {0, 1}} {
		if !got[k] {
			t.Errorf("no watch on TVDB S%dE%d", k[0], k[1])
		}
	}
	if got[[2]int64{1, 13}] || got[[2]int64{1, 24}] || got[[2]int64{1, 99}] {
		t.Errorf("watch left on a TMDB number: %v", got)
	}
	// One TMDB season became two TVDB seasons, both completed; the special too.
	st := f.seasonStatuses(after)
	if st[1] != library.Completed || st[2] != library.Completed || st[0] != library.Completed || len(st) != 3 {
		t.Errorf("season rows = %v", st)
	}
	// Progress counts distinct regular episodes: the stray watch sits on a twin.
	if after.Progress != 24 {
		t.Errorf("progress = %d", after.Progress)
	}
	// The unlisted episode is kept on the closest one, with a note, and reported.
	if len(res.Unmapped) != 1 || res.Unmapped[0].FromEpisode != 99 || res.Unmapped[0].ToSeason != 1 {
		t.Fatalf("unmapped = %+v", res.Unmapped)
	}
	var noted int
	for _, w := range ws {
		if strings.Contains(w.Notes, "Converted from TMDB S01E99") {
			noted++
		}
	}
	if noted != 1 {
		t.Errorf("notes on %d watches", noted)
	}
	// Old links keep working: the TMDB id is recorded on the TVDB item.
	if s, id := f.env.Catalog.ResolveShowRef(ctx, "tmdb", "1668"); s != "tvdb" || id != "7000" {
		t.Errorf("old URL resolves to %s/%s", s, id)
	}
	if it, err := f.env.Catalog.FindByExternalID(ctx, "tmdb", "1668", "tv", "anime"); err != nil || it.ID != tv.ID {
		t.Errorf("find by TMDB id: %v %v", it.ID, err)
	}
	// An old-style add (a TMDB id) lands on the TVDB item.
	if it, err := f.env.Catalog.EnsureItem(ctx, "tv", "tmdb", "1668"); err != nil || it.ID != tv.ID {
		t.Errorf("EnsureItem by TMDB id: %v %v", it.ID, err)
	}
}

func TestConvertIsIdempotentAndDryRunWritesNothing(t *testing.T) {
	f := newFx(t)
	f.friends()
	item := f.tmdbShow("1668")
	for i := 1; i <= 5; i++ {
		f.watch(item, 1, i, t0.Add(time.Duration(i)*time.Hour), library.SourceManual)
	}
	f.start()

	rep, err := f.cv.Run(ctx, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Converted) != 1 || rep.Converted[0].Seasons.After != 2 {
		t.Fatalf("dry run report = %+v", rep.Converted)
	}
	if n := f.count(`SELECT count(*) FROM items WHERE source = 'tvdb'`); n != 0 {
		t.Fatalf("dry run created %d TVDB items", n)
	}
	if n := f.count(`SELECT count(*) FROM watches w JOIN entries e ON e.id = w.entry_id JOIN items i ON i.id = e.item_id WHERE i.source = 'tmdb'`); n != 5 {
		t.Fatalf("dry run touched watches: %d", n)
	}

	if _, err := f.cv.Run(ctx, Options{}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.count(`SELECT count(*) FROM watches`)
	season1 := f.count(`SELECT count(*) FROM watches WHERE season = 1 AND episode BETWEEN 1 AND 5`)
	rep, err = f.cv.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Converted) != 0 || len(rep.Kept) != 0 {
		t.Errorf("second run converted %d, kept %d", len(rep.Converted), len(rep.Kept))
	}
	if f.count(`SELECT count(*) FROM watches`) != snapshot || f.count(`SELECT count(*) FROM watches WHERE season = 1 AND episode BETWEEN 1 AND 5`) != season1 {
		t.Error("second run moved watches")
	}
}

func TestConvertAssumedWatchesAndShowsWithoutATVDBMatch(t *testing.T) {
	f := newFx(t)
	// Jellyfin first saw it at S2: S1 is assumed watched.
	f.env.TMDB.AddShow("20", "Assumed",
		season(1, dated(1, 3, "2019-01-07", 0)), season(2, dated(1, 3, "2020-01-06", 0)))
	f.env.TVDB.AddShow("2000", "Assumed",
		season(1, dated(1, 3, "2019-01-07", 20100)), season(2, dated(1, 3, "2020-01-06", 20200)))
	f.env.TMDB.SetTVDBID("20", "2000")
	// TMDB links this one to a TVDB series that does not exist, and has no IMDb id.
	f.env.TMDB.AddShow("30", "Orphan", season(1, dated(1, 2, "2018-01-01", 0)))
	f.env.TMDB.SetTVDBID("30", "")
	a := f.tmdbShow("20")
	f.watch(a, 2, 2, t0, library.SourceJellyfin)
	o := f.tmdbShow("30")
	f.watch(o, 1, 1, t0, library.SourceManual)
	ae := f.entry(a)
	if n := len(f.watches(ae)); n != 4 { // 3 assumed + the play
		t.Fatalf("setup: %d watches", n)
	}
	f.start()

	rep, err := f.cv.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Converted) != 1 || len(rep.Kept) != 1 || rep.Kept[0].Title != "Orphan" || rep.Kept[0].Reason == "" {
		t.Fatalf("converted %d, kept %+v", len(rep.Converted), rep.Kept)
	}
	// Left on TMDB, still tracked, untouched.
	if n := f.count(`SELECT count(*) FROM items WHERE source = 'tmdb' AND external_id = '30'`); n != 1 {
		t.Errorf("orphan item gone")
	}
	tv := f.tvdbItem("2000")
	ws := f.watches(f.entry(tv))
	assumed, jf := 0, 0
	for _, w := range ws {
		switch w.Source {
		case library.SourceAssumed:
			assumed++
			if w.Season.Int64 != 1 {
				t.Errorf("assumed watch on S%d", w.Season.Int64)
			}
		case library.SourceJellyfin:
			jf++
		}
	}
	if assumed != 3 || jf != 1 {
		t.Errorf("assumed %d jellyfin %d (source must survive the move)", assumed, jf)
	}
	st := f.seasonStatuses(f.entry(tv))
	if st[1] != library.Completed || st[2] != library.InProgress {
		t.Errorf("season rows = %v", st)
	}
}

func TestConvertMergesIntoAnExistingTVDBEntryAsAnotherRun(t *testing.T) {
	f := newFx(t)
	f.friends()
	item := f.tmdbShow("1668")
	f.watch(item, 1, 1, t0, library.SourceManual)
	f.start()
	// The user already tracks the TVDB series.
	tv, err := f.env.Catalog.EnsureItem(ctx, "tv", "tvdb", "7000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.Track(ctx, f.user, tv.ID, library.Planning, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	rep, err := f.cv.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Converted) != 1 || !rep.Converted[0].Merged {
		t.Fatalf("report = %+v", rep.Converted)
	}
	runs, err := f.lib.Runs(ctx, f.user, tv.ID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %d (%v)", len(runs), err)
	}
	if runs[len(runs)-1].Run != 2 || len(f.watches(runs[len(runs)-1])) != 1 {
		t.Errorf("moved entry should be run 2 with its watch: %+v", runs)
	}
}

func TestConvertRewritesCredits(t *testing.T) {
	f := newFx(t)
	f.friends()
	item := f.tmdbShow("1668")
	f.watch(item, 1, 1, t0, library.SourceManual)
	// A TMDB person who also acted in a film.
	movie, err := f.env.Catalog.EnsureItem(ctx, "movie", "tmdb", "603")
	if err == nil {
		_ = movie
	}
	f.env.TMDB.AddMovie("603", "Film", "1999-01-01")
	movie, err = f.env.Catalog.EnsureItem(ctx, "movie", "tmdb", "603")
	if err != nil {
		t.Fatal(err)
	}
	mp := f.person("tmdb", "555", "Jane Doe")
	other := f.person("tmdb", "556", "Only In Old Cast")
	f.credit(movie.ID, mp)
	f.credit(item.ID, mp)
	f.credit(item.ID, other)
	f.env.TVDB.AddCharacters("7000",
		tvdb.Character{PersonID: 1, Person: "Jane D.", PersonType: "Actor", Character: "Rachel", Sort: 1},
		tvdb.Character{PersonID: 2, Person: "No Ids", PersonType: "Actor", Character: "Joey", Sort: 2},
		tvdb.Character{PersonID: 3, Person: "Newly Keyed", PersonType: "Actor", Character: "Ross", Sort: 3},
		tvdb.Character{PersonID: 4, Person: "Guest", PersonType: "Guest Star", Character: "Nobody", Sort: 4},
		tvdb.Character{PersonID: 5, Person: "Maker", PersonType: "Creator", Sort: 5},
	)
	f.env.TVDB.AddPerson("1", map[string]string{"tmdb": "555", "imdb": "nm1"})
	f.env.TVDB.AddPerson("3", map[string]string{"tmdb": "777"})
	f.env.TVDB.AddPerson("5", map[string]string{})
	f.start()

	rep, err := f.cv.Run(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	tv := f.tvdbItem("7000")
	// The TVDB cast replaced the TMDB cast: Jane merged with the film's person
	// by the shared TMDB id (not by name), "Newly Keyed" got a TMDB-keyed record,
	// "No Ids" and the creator stay TVDB people, the guest star is not listed.
	rows, err := f.env.Q.ListItemCredits(ctx, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("credits = %d: %+v", len(rows), rows)
	}
	srcs := map[string]string{}
	for _, r := range rows {
		srcs[r.PersonName] = r.PersonSource + ":" + r.PersonExternalID
	}
	if srcs["Jane Doe"] != "tmdb:555" || srcs["Newly Keyed"] != "tmdb:777" || srcs["No Ids"] != "tvdb:2" || srcs["Maker"] != "tvdb:5" {
		t.Errorf("people = %v", srcs)
	}
	if _, ok := srcs["Jane D."]; ok {
		t.Error("TVDB's spelling replaced the known person's name")
	}
	// The film's credit is untouched and the same person now also has the show.
	if n := f.count(`SELECT count(*) FROM credits WHERE person_id = ?`, mp); n != 2 {
		t.Errorf("Jane has %d credits, want film + show", n)
	}
	// The TMDB-only cast member is gone.
	if n := f.count(`SELECT count(*) FROM people WHERE id = ?`, other); n != 0 {
		t.Errorf("orphaned TMDB person kept")
	}
	if rep.People.Merged != 1 || rep.People.TMDBKeyed != 1 || rep.People.TVDBOnly != 2 || rep.PeopleRemoved != 1 {
		t.Errorf("people stats = %+v removed %d", rep.People, rep.PeopleRemoved)
	}
	if n := f.count(`SELECT count(*) FROM credits c JOIN items i ON i.id = c.item_id WHERE i.source = 'tmdb' AND i.kind = 'tv'`); n != 0 {
		t.Errorf("%d TMDB show credits left", n)
	}
}

func (f *fx) person(source, id, name string) int64 {
	f.t.Helper()
	pid, err := f.env.Q.UpsertPerson(ctx, db.UpsertPersonParams{Source: source, ExternalID: id, Name: name})
	if err != nil {
		f.t.Fatal(err)
	}
	return pid
}

func (f *fx) credit(itemID, personID int64) {
	f.t.Helper()
	if err := f.env.Q.InsertCredit(ctx, db.InsertCreditParams{ItemID: itemID, PersonID: personID, Role: "cast", Ord: 0, EpisodeCount: sql.NullInt64{}}); err != nil {
		f.t.Fatal(err)
	}
}

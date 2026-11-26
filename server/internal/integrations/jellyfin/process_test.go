package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers/tvdb"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

type fixture struct {
	t    *testing.T
	env  *catalogtest.Env
	find *catalogtest.FindTMDB
	lib  *library.Service
	svc  *Service
	user db.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	find := catalogtest.NewFindTMDB(env.TMDB)
	env.UseTMDB(find)
	env.UseTVDB(env.TVDB)
	lib := library.New(env.Conn, env.Q, env.Catalog)
	lib.Now = env.Now
	svc := New(Deps{
		Conn: env.Conn, Q: env.Q, Catalog: env.Catalog, Library: lib, TMDB: find, TVDB: env.TVDB,
		Config: config.Config{JellyfinWatchedPercent: 75}, Now: env.Now,
	})
	uid := env.User(t, "owner")
	u, err := env.Q.GetUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, env: env, find: find, lib: lib, svc: svc, user: u}
}

// friends registers TVDB series 7000 with three seasons of three episodes;
// the TVDB id of an episode is 700000 + 100*season + episode.
func (f *fixture) friends() {
	season := func(n int, date string) catalogtest.Season {
		eps := catalogtest.Eps(1, 3, date)
		for i := range eps {
			eps[i].ID = int64(700000 + 100*n + eps[i].Number)
		}
		return catalogtest.Season{Number: n, AirDate: date, Episodes: eps}
	}
	f.env.TVDB.AddShow("7000", "Friends", season(1, "2000-01-01"), season(2, "2001-01-01"), season(3, "2002-01-01"))
}

// ep is an event for episode (season, episode) of Friends, identified by its TVDB episode id.
func (f *fixture) ep(kind string, season, episode int, played bool) Event {
	return Event{
		Kind: kind, Type: "Episode", Name: "Ep", SeriesName: "Friends", Season: ip(season), Episode: ip(episode),
		Tvdb: fmt.Sprint(700000 + 100*season + episode), Played: played,
	}
}

func (f *fixture) process(ev Event, opt Options) Result {
	f.t.Helper()
	r, err := f.svc.Process(ctx, f.user, ev, opt)
	if err != nil {
		f.t.Fatalf("process %s: %v", ev.Label(), err)
	}
	return r
}

func ip(n int) *int { return &n }

// epEvent is an episode event carrying a TVDB episode id.
func epEvent(kind, tvdb string, season, episode int, played bool) Event {
	return Event{Kind: kind, Type: "Episode", Name: "Ep", SeriesName: "Friends", Season: ip(season), Episode: ip(episode), Tvdb: tvdb, Played: played}
}

func (f *fixture) item(source, id string) db.Item {
	f.t.Helper()
	var it db.Item
	if err := f.env.Conn.QueryRow(`SELECT id FROM items WHERE source = ? AND external_id = ?`, source, id).Scan(&it.ID); err != nil {
		f.t.Fatalf("item %s %s: %v", source, id, err)
	}
	return it
}

func (f *fixture) entry(source, id string) db.Entry {
	f.t.Helper()
	e, err := f.lib.CurrentEntry(ctx, f.user.ID, f.item(source, id).ID)
	if err != nil {
		f.t.Fatalf("entry %s: %v", id, err)
	}
	return e
}

func (f *fixture) noEntry(source, id string) {
	f.t.Helper()
	var n int
	if err := f.env.Conn.QueryRow(`SELECT count(*) FROM entries e JOIN items i ON i.id = e.item_id WHERE i.source = ? AND i.external_id = ?`, source, id).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	if n != 0 {
		f.t.Fatalf("%s has %d entries, want none", id, n)
	}
}

func (f *fixture) watches(e db.Entry) []db.Watch {
	f.t.Helper()
	w, err := f.env.Q.ListEntryWatches(ctx, e.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func (f *fixture) seasonStatus(e db.Entry, n int64) string {
	f.t.Helper()
	ss, err := f.env.Q.ListEntrySeasons(ctx, e.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, s := range ss {
		if s.Season == n {
			return s.Status
		}
	}
	return ""
}

// jfWatches are the real plays (not the assumed earlier seasons).
func (f *fixture) jfWatches(e db.Entry) []db.Watch {
	f.t.Helper()
	var out []db.Watch
	for _, w := range f.watches(e) {
		if w.Source == library.SourceJellyfin {
			out = append(out, w)
		}
	}
	return out
}

func countSource(ws []db.Watch, source string) int {
	n := 0
	for _, w := range ws {
		if w.Source == source {
			n++
		}
	}
	return n
}

func TestParsePayload(t *testing.T) {
	plain := map[string]any{
		"NotificationType": "PlaybackStop",
		"Item": map[string]any{
			"Type": "Episode", "Name": "Pilot", "ProductionYear": "1994", "SeriesName": "Friends",
			"ParentIndexNumber": "2", "IndexNumber": "8",
			"ProviderIds":  map[string]any{"Tmdb": "", "Imdb": "tt1", "Tvdb": "303821"},
			"UserData":     map[string]any{"Played": "False"},
			"RunTimeTicks": "1000", "PlaybackPositionTicks": "760",
		},
	}
	ev := ParsePayload(plain, 75)
	if ev.Kind != EventStop || ev.Type != "Episode" || *ev.Season != 2 || *ev.Episode != 8 || *ev.Year != 1994 || ev.Tvdb != "303821" || ev.Tmdb != "" || ev.Imdb != "tt1" {
		t.Fatalf("plain template: %+v", ev)
	}
	if !ev.Played {
		t.Error("76% of the runtime at 75% threshold should count as played")
	}
	plain["Item"].(map[string]any)["PlaybackPositionTicks"] = "740"
	if ParsePayload(plain, 75).Played {
		t.Error("74% should not count")
	}
	if !ParsePayload(plain, 70).Played {
		t.Error("threshold follows the argument")
	}
	plain["NotificationType"] = "PlaybackStart"
	plain["Item"].(map[string]any)["PlaybackPositionTicks"] = "990"
	if ev := ParsePayload(plain, 75); ev.Kind != EventPlay || ev.Played {
		t.Errorf("start is never played by position: %+v", ev)
	}
	plain["NotificationType"] = "ItemAdded"
	if ev := ParsePayload(plain, 75); ev.Kind != "ItemAdded" {
		t.Errorf("other notification kept as is: %q", ev.Kind)
	}

	legacy := map[string]any{
		"Event": "Stop", "NotificationType": "PlaybackStart",
		"Item": map[string]any{"Type": "Movie", "Name": "The Matrix", "ProductionYear": float64(1999),
			"ProviderIds": map[string]any{"Tmdb": "603"}, "UserData": map[string]any{"Played": true}},
		"Series": map[string]any{"ProviderIds": map[string]any{"Tvdb": "7000"}},
	}
	ev = ParsePayload(legacy, 75)
	if ev.Kind != EventStop || !ev.Played || ev.Tmdb != "603" || ev.SeriesTvdb != "7000" || ev.Label() != "The Matrix (1999)" {
		t.Errorf("legacy template: %+v", ev)
	}
	if got := epEvent("Stop", "1", 1, 2, true).Label(); got != "Friends S01E02" {
		t.Errorf("label = %q", got)
	}
}

func TestEpisodeByTVDBEpisodeID(t *testing.T) {
	f := newFixture(t)
	f.friends()
	if r := f.process(f.ep("Stop", 1, 1, true), Options{}); r.Outcome != Matched {
		t.Fatalf("outcome = %v", r.Outcome)
	}
	e := f.entry("tvdb", "7000")
	if e.Status != library.InProgress {
		t.Errorf("show status = %s", e.Status)
	}
	if got := f.seasonStatus(e, 1); got != library.InProgress {
		t.Errorf("season status = %s", got)
	}
	ws := f.watches(e)
	if len(ws) != 1 || ws[0].Source != library.SourceJellyfin || ws[0].Season.Int64 != 1 || ws[0].Episode.Int64 != 1 {
		t.Fatalf("watches = %+v", ws)
	}
	// No TMDB at all: the show is a TVDB show and the TMDB client is never asked.
	if n := f.env.TMDB.Calls["TV"] + f.env.TMDB.Calls["TVWithSeasons"] + f.env.TMDB.Calls["Season"] + len(f.find.Finds); n != 0 {
		t.Errorf("TMDB was used for a show: %v finds=%v", f.env.TMDB.Calls, f.find.Finds)
	}
}

func TestEpisodeIDIsTrustedOverJellyfinNumbers(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// Jellyfin says S9E9; TVDB knows the episode id as S2E3 and wins.
	ev := f.ep("Stop", 2, 3, true)
	ev.Season, ev.Episode = ip(9), ip(9)
	f.process(ev, Options{})
	w := f.jfWatches(f.entry("tvdb", "7000"))
	if len(w) != 1 || w[0].Season.Int64 != 2 || w[0].Episode.Int64 != 3 {
		t.Fatalf("watches = %+v (TVDB numbering must win over Jellyfin's)", w)
	}
	// The show is in the catalog now: the next episode needs no TVDB lookup.
	f.env.TVDB.Calls = nil
	f.process(f.ep("Stop", 2, 2, true), Options{})
	for _, c := range f.env.TVDB.Calls {
		if c == "episode:700202" {
			t.Errorf("episode looked up on TVDB although the catalog knows it: %v", f.env.TVDB.Calls)
		}
	}
	if len(f.jfWatches(f.entry("tvdb", "7000"))) != 2 {
		t.Errorf("watches = %+v", f.jfWatches(f.entry("tvdb", "7000")))
	}
}

func TestSeriesIDWithJellyfinNumbering(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// An unknown episode id, but the series' TVDB id and Jellyfin's numbers (TVDB's).
	ev := Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Season: ip(2), Episode: ip(1), Tvdb: "404404", SeriesTvdb: "7000", Played: true}
	if r := f.process(ev, Options{}); r.Outcome != Matched {
		t.Fatalf("outcome = %v", r.Outcome)
	}
	w := f.jfWatches(f.entry("tvdb", "7000"))
	if len(w) != 1 || w[0].Season.Int64 != 2 || w[0].Episode.Int64 != 1 {
		t.Fatalf("watches = %+v", w)
	}
	// A series id without episode numbers cannot be placed.
	ev = Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", SeriesTvdb: "7000", Played: true}
	if r := f.process(ev, Options{}); r.Outcome != Unmatched {
		t.Errorf("no numbers: outcome = %v", r.Outcome)
	}
}

func TestIMDbEpisodeIDGoesThroughTVDB(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.env.TVDB.AddRemote("tt777", tvdb.Hit{SeriesID: 7000, Episode: &tvdb.Episode{ID: 1, Season: 2, Number: 2}})
	ev := Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Season: ip(1), Episode: ip(1), Imdb: "tt777", Played: true}
	if r := f.process(ev, Options{}); r.Outcome != Matched {
		t.Fatalf("outcome = %v", r.Outcome)
	}
	w := f.jfWatches(f.entry("tvdb", "7000"))
	if len(w) != 1 || w[0].Season.Int64 != 2 || w[0].Episode.Int64 != 2 {
		t.Fatalf("imdb path watches = %+v", w)
	}
	if len(f.find.Finds) != 0 {
		t.Errorf("TMDB find used for an episode: %v", f.find.Finds)
	}
}

func TestNoTMDBNumberingFallback(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// The old fallback took the series' TMDB id and used Jellyfin's (or TVDB's)
	// numbers as TMDB numbers. It is gone: a TMDB id alone places nothing.
	ev := Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Season: ip(1), Episode: ip(1), Tvdb: "404405", Tmdb: "1668", Played: true}
	if r := f.process(ev, Options{}); r.Outcome != Unmatched {
		t.Errorf("outcome = %v, want Unmatched", r.Outcome)
	}
	f.noEntry("tvdb", "7000")
	f.noEntry("tmdb", "1668")
}

func TestUnmatchedItemsAreNotErrors(t *testing.T) {
	f := newFixture(t)
	f.friends()
	cases := map[string]Event{
		"no ids":             {Kind: "Stop", Type: "Episode", SeriesName: "X", Season: ip(1), Episode: ip(1), Played: true},
		"unknown tvdb":       epEvent("Stop", "123", 1, 1, true),
		"unknown imdb movie": {Kind: "Stop", Type: "Movie", Name: "Nope", Imdb: "tt0", Played: true},
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			if r := f.process(ev, Options{}); r.Outcome != Unmatched {
				t.Errorf("outcome = %v", r.Outcome)
			}
		})
	}
	// Other events and media types are ignored outright.
	if r := f.process(Event{Kind: "ItemAdded", Type: "Episode", Tvdb: "1"}, Options{}); r.Outcome != Ignored {
		t.Errorf("ItemAdded: %v", r.Outcome)
	}
	if r := f.process(Event{Kind: "Stop", Type: "Audio", Tmdb: "1"}, Options{}); r.Outcome != Ignored {
		t.Errorf("Audio: %v", r.Outcome)
	}
	f.noEntry("tvdb", "7000")
}

func TestStopBelowThresholdOnlyStartsThenPlayedFlagCounts(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// Below the threshold ParsePayload leaves Played false: only In progress.
	f.process(f.ep("Stop", 1, 1, false), Options{})
	e := f.entry("tvdb", "7000")
	if e.Status != library.InProgress || len(f.watches(e)) != 0 {
		t.Fatalf("status %s, watches %d", e.Status, len(f.watches(e)))
	}
	// Played flag set by Jellyfin (no ticks needed).
	f.process(f.ep("Stop", 1, 1, true), Options{})
	if got := len(f.watches(e)); got != 1 {
		t.Fatalf("watches = %d", got)
	}
}

func TestPlayIsForwardOnlyAndNeverReopens(t *testing.T) {
	f := newFixture(t)
	f.friends()
	play := func(s, e int) { f.process(f.ep("Play", s, e, false), Options{}) }
	// Play on a new S1 show creates it In progress with S1 In progress.
	play(1, 1)
	e := f.entry("tvdb", "7000")
	if e.Status != library.InProgress || f.seasonStatus(e, 1) != library.InProgress {
		t.Fatalf("after play: %s / %s", e.Status, f.seasonStatus(e, 1))
	}
	// Finish the season (finale S1E3) then more plays change nothing.
	f.process(f.ep("Stop", 1, 3, true), Options{})
	if got := f.seasonStatus(f.entry("tvdb", "7000"), 1); got != library.Completed {
		t.Fatalf("S1 should be completed by the finale, is %s", got)
	}
	play(1, 1)
	if got := f.seasonStatus(f.entry("tvdb", "7000"), 1); got != library.Completed {
		t.Errorf("play reopened a completed season: %s", got)
	}
}

func TestNeverUndoRewatchAndDuplicates(t *testing.T) {
	f := newFixture(t)
	f.friends()
	stop := func(s, e int, played bool, at time.Time) {
		f.process(f.ep("Stop", s, e, played), Options{WatchedAt: at})
	}
	stop(1, 1, true, t0)
	// Webhook retry a couple of seconds later: one play.
	stop(1, 1, true, t0.Add(2*time.Second))
	e := f.entry("tvdb", "7000")
	if got := len(f.watches(e)); got != 1 {
		t.Fatalf("duplicate stop recorded %d watches", got)
	}
	stop(1, 3, true, t0.Add(time.Hour)) // finale: S1 completed
	e = f.entry("tvdb", "7000")
	if f.seasonStatus(e, 1) != library.Completed {
		t.Fatalf("S1 = %s", f.seasonStatus(e, 1))
	}
	before := e
	nWatches := len(f.watches(e))
	// Rewind: an unfinished stop of an earlier episode changes nothing.
	stop(1, 1, false, t0.Add(2*time.Hour))
	e = f.entry("tvdb", "7000")
	if e.Status != before.Status || e.Progress != before.Progress || f.seasonStatus(e, 1) != library.Completed || len(f.watches(e)) != nWatches {
		t.Errorf("unfinished stop changed state: %+v -> %+v", before, e)
	}
	// A full rewatch later adds a watch but undoes nothing.
	stop(1, 1, true, t0.Add(48*time.Hour))
	e = f.entry("tvdb", "7000")
	if f.seasonStatus(e, 1) != library.Completed || e.Status == library.Planning || len(f.watches(e)) != nWatches+1 {
		t.Errorf("rewatch: status %s, S1 %s, watches %d", e.Status, f.seasonStatus(e, 1), len(f.watches(e)))
	}
}

func TestCompletedShowStaysCompletedOnRewatch(t *testing.T) {
	f := newFixture(t)
	f.env.TVDB.AddShow("5", "Mini", catalogtest.Season{Number: 1, AirDate: "2000-01-01", Episodes: []catalogtest.Episode{
		{ID: 501, Number: 1, AirDate: "2000-01-01", Name: "a"}, {ID: 502, Number: 2, AirDate: "2000-01-01", Name: "b"},
	}}).SetStatus("5", "Ended")
	ev := func(kind, id string) Event {
		return Event{Kind: kind, Type: "Episode", SeriesName: "Mini", Tvdb: id, Played: kind == "Stop"}
	}
	st := func(id string, at time.Time) { f.process(ev("Stop", id), Options{WatchedAt: at}) }
	st("501", t0)
	st("502", t0.Add(time.Hour))
	if e := f.entry("tvdb", "5"); e.Status != library.Completed {
		t.Fatalf("show = %s", e.Status)
	}
	st("501", t0.Add(24*time.Hour))
	f.process(ev("Play", "501"), Options{})
	if e := f.entry("tvdb", "5"); e.Status != library.Completed {
		t.Errorf("rewatch reopened the show: %s", e.Status)
	}
}

func TestFirstSightingAtSeason3AssumesEarlierSeasons(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// A Play at S3 for an untracked show creates nothing yet.
	f.process(f.ep("Play", 3, 2, false), Options{})
	f.noEntry("tvdb", "7000")
	// The first finished play does, and assumes S1-S2.
	f.process(f.ep("Stop", 3, 2, true), Options{})
	e := f.entry("tvdb", "7000")
	ws := f.watches(e)
	if got := countSource(ws, library.SourceAssumed); got != 6 {
		t.Errorf("assumed watches = %d, want 6 (S1 and S2, 3 episodes each)", got)
	}
	if got := countSource(ws, library.SourceJellyfin); got != 1 {
		t.Errorf("jellyfin watches = %d, want 1", got)
	}
	if f.seasonStatus(e, 1) != library.Completed || f.seasonStatus(e, 2) != library.Completed || f.seasonStatus(e, 3) != library.InProgress {
		t.Errorf("seasons: %s %s %s", f.seasonStatus(e, 1), f.seasonStatus(e, 2), f.seasonStatus(e, 3))
	}
	if e.Status != library.InProgress {
		t.Errorf("show = %s", e.Status)
	}
	// Already tracked: a later first play of S1 does not add assumed watches.
	before := len(f.watches(e))
	f.process(f.ep("Stop", 1, 1, true), Options{})
	if got := countSource(f.watches(e), library.SourceAssumed); got != 6 {
		t.Errorf("assumed watches grew to %d", got)
	}
	if len(f.watches(e)) != before+1 {
		t.Errorf("watches = %d", len(f.watches(e)))
	}
}

func TestMovies(t *testing.T) {
	f := newFixture(t)
	f.env.TMDB.AddMovie("603", "The Matrix", "1999-03-31")
	f.env.TMDB.AddMovie("604", "Reloaded", "2003-05-15")
	f.find.AddMovieResult("imdb_id", "tt0604", 604)

	// Play marks a new movie In progress.
	play := Event{Kind: "Play", Type: "Movie", Name: "The Matrix", Tmdb: "603"}
	f.process(play, Options{})
	e := f.entry("tmdb", "603")
	if e.Status != library.InProgress {
		t.Fatalf("after play: %s", e.Status)
	}
	// Stop past the threshold (ParsePayload sets Played) completes it.
	stop := play
	stop.Kind, stop.Played = "Stop", true
	f.process(stop, Options{WatchedAt: t0})
	e = f.entry("tmdb", "603")
	if e.Status != library.Completed || e.Progress != 1 || countSource(f.watches(e), library.SourceJellyfin) != 1 {
		t.Fatalf("after stop: %+v watches=%d", e, len(f.watches(e)))
	}
	// Duplicate stop seconds later, play and unfinished stop: nothing changes.
	f.process(stop, Options{WatchedAt: t0.Add(3 * time.Second)})
	f.process(play, Options{})
	unfinished := stop
	unfinished.Played = false
	f.process(unfinished, Options{})
	e = f.entry("tmdb", "603")
	if e.Status != library.Completed || len(f.watches(e)) != 1 {
		t.Errorf("never-undo broken: %s, %d watches", e.Status, len(f.watches(e)))
	}
	// An import never logs a rewatch.
	f.process(stop, Options{WatchedAt: t0.Add(72 * time.Hour), Import: true})
	if got := len(f.watches(f.entry("tmdb", "603"))); got != 1 {
		t.Errorf("import added a rewatch: %d", got)
	}
	// IMDb-only movie goes through TMDB find.
	f.process(Event{Kind: "Stop", Type: "Movie", Name: "Reloaded", Imdb: "tt0604", Played: true}, Options{})
	if e := f.entry("tmdb", "604"); e.Status != library.Completed {
		t.Errorf("imdb movie = %s", e.Status)
	}
}

func TestMarkEventsAreIgnored(t *testing.T) {
	f := newFixture(t)
	f.env.TMDB.AddMovie("603", "The Matrix", "1999-03-31")
	for _, kind := range []string{"MarkPlayed", "MarkUnplayed"} {
		if r := f.process(Event{Kind: kind, Type: "Movie", Tmdb: "603", Played: true}, Options{}); r.Outcome != Ignored {
			t.Errorf("%s: %v", kind, r.Outcome)
		}
	}
	f.noEntry("tmdb", "603")
}

func TestPercentPrefersUserThenConfig(t *testing.T) {
	f := newFixture(t)
	if got := f.svc.Percent(db.User{WatchedPercent: 90}); got != 90 {
		t.Errorf("user value = %d", got)
	}
	if got := f.svc.Percent(db.User{}); got != 75 {
		t.Errorf("config fallback = %d", got)
	}
}

func TestStatusJSONShape(t *testing.T) {
	f := newFixture(t)
	f.svc.RecordWebhook(ctx, f.user.ID, "Friends S01E01", true)
	v, err := f.env.Q.GetKV(ctx, fmt.Sprintf("source:%d:jellyfin_webhook", f.user.ID))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatal(err)
	}
	if m["last_item"] != "Friends S01E01" || m["ok"] != true || m["last_event"] != t0.Format(time.RFC3339) {
		t.Errorf("webhook status = %v", m)
	}
}

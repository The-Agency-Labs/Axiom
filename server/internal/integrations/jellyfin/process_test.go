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
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

type fixture struct {
	t    *testing.T
	env  *catalogtest.Env
	find *catalogtest.FindTMDB
	tvdb *catalogtest.TVDB
	lib  *library.Service
	svc  *Service
	user db.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	find := catalogtest.NewFindTMDB(env.TMDB)
	env.UseTMDB(find)
	lib := library.New(env.Conn, env.Q, env.Catalog)
	lib.Now = env.Now
	tvdb := catalogtest.NewTVDB()
	svc := New(Deps{
		Conn: env.Conn, Q: env.Q, Catalog: env.Catalog, Library: lib, TMDB: find, TVDB: tvdb,
		Config: config.Config{JellyfinWatchedPercent: 75}, Now: env.Now,
	})
	uid := env.User(t, "owner")
	u, err := env.Q.GetUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, env: env, find: find, tvdb: tvdb, lib: lib, svc: svc, user: u}
}

// friends registers TMDB show 1668 with two seasons of three episodes.
func (f *fixture) friends() {
	f.env.TMDB.AddShow("1668", "Friends",
		catalogtest.Season{Number: 1, AirDate: "2000-01-01", Episodes: catalogtest.Eps(1, 3, "2000-01-01")},
		catalogtest.Season{Number: 2, AirDate: "2001-01-01", Episodes: catalogtest.Eps(1, 3, "2001-01-01")},
		catalogtest.Season{Number: 3, AirDate: "2002-01-01", Episodes: catalogtest.Eps(1, 3, "2002-01-01")},
	)
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
		"Series": map[string]any{"ProviderIds": map[string]any{"Tmdb": "1668"}},
	}
	ev = ParsePayload(legacy, 75)
	if ev.Kind != EventStop || !ev.Played || ev.Tmdb != "603" || ev.SeriesTmdb != "1668" || ev.Label() != "The Matrix (1999)" {
		t.Errorf("legacy template: %+v", ev)
	}
	if got := epEvent("Stop", "1", 1, 2, true).Label(); got != "Friends S01E02" {
		t.Errorf("label = %q", got)
	}
}

func TestEpisodeViaTVDBThenTMDBFind(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.tvdb.AddEpisode("303821", 7000, 1, 1)
	f.find.AddEpisodeResult("tvdb_id", "303821", 1668, 1, 1)
	if r := f.process(epEvent("Stop", "303821", 1, 1, true), Options{}); r.Outcome != Matched {
		t.Fatalf("outcome = %v", r.Outcome)
	}
	e := f.entry("tmdb", "1668")
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
	if f.env.TMDB.Calls["TVWithSeasons"]+f.env.TMDB.Calls["Season"]+f.env.TMDB.Calls["TV"] == 0 {
		t.Error("expected TMDB metadata fetches")
	}
}

func TestTVDBSeriesFallbackUsesCatalogThenTVDB(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// TMDB find misses the episode; TVDB numbering with the series' TMDB id.
	f.tvdb.AddEpisode("900", 7000, 2, 3)
	f.tvdb.AddSeries(7000, "1668")
	f.process(epEvent("Stop", "900", 9, 9, true), Options{})
	e := f.entry("tmdb", "1668")
	w := f.jfWatches(e)
	if len(w) != 1 || w[0].Season.Int64 != 2 || w[0].Episode.Int64 != 3 {
		t.Fatalf("watches = %+v (TVDB numbering must win over Jellyfin's)", w)
	}
	// Show now known by its TVDB series id (7000 in the fake metadata):
	// the next episode needs no TVDB series lookup.
	f.tvdb.Calls = nil
	f.tvdb.AddEpisode("901", 7000, 2, 2)
	f.process(epEvent("Stop", "901", 9, 9, true), Options{})
	for _, c := range f.tvdb.Calls {
		if c == "series:7000" {
			t.Errorf("series looked up on TVDB although the catalog knows it: %v", f.tvdb.Calls)
		}
	}
	if len(f.jfWatches(e)) != 2 {
		t.Errorf("watches = %+v", f.jfWatches(e))
	}
}

func TestTVDB404FallsBackToIMDbThenShowID(t *testing.T) {
	f := newFixture(t)
	f.friends()
	// TVDB knows nothing (404 = miss); IMDb resolves through TMDB find.
	f.find.AddEpisodeResult("imdb_id", "tt777", 1668, 2, 1)
	ev := epEvent("Stop", "404404", 1, 1, true)
	ev.Imdb = "tt777"
	if r := f.process(ev, Options{}); r.Outcome != Matched {
		t.Fatalf("outcome = %v", r.Outcome)
	}
	w := f.jfWatches(f.entry("tmdb", "1668"))
	if len(w) != 1 || w[0].Season.Int64 != 2 || w[0].Episode.Int64 != 1 {
		t.Fatalf("imdb path watches = %+v", w)
	}
	if len(f.tvdb.Calls) == 0 || f.tvdb.Calls[0] != "episode:404404" {
		t.Errorf("TVDB not tried first: %v", f.tvdb.Calls)
	}

	// Neither TVDB nor IMDb: the series TMDB id with Jellyfin's numbering.
	ev = epEvent("Stop", "404405", 1, 3, true)
	ev.SeriesTmdb = "1668"
	f.process(ev, Options{})
	if got := len(f.jfWatches(f.entry("tmdb", "1668"))); got != 2 {
		t.Errorf("series id path: %d watches", got)
	}

	// A show id without episode numbers cannot be placed.
	ev = Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", SeriesTmdb: "1668", Played: true}
	if r := f.process(ev, Options{}); r.Outcome != Unmatched {
		t.Errorf("no numbers: outcome = %v", r.Outcome)
	}
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
	f.noEntry("tmdb", "1668")
}

func TestStopBelowThresholdOnlyStartsThenPlayedFlagCounts(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.find.AddEpisodeResult("imdb_id", "tt1", 1668, 1, 1)
	ev := Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Season: ip(1), Episode: ip(1), Imdb: "tt1"}
	// Below the threshold ParsePayload leaves Played false: only In progress.
	f.process(ev, Options{})
	e := f.entry("tmdb", "1668")
	if e.Status != library.InProgress || len(f.watches(e)) != 0 {
		t.Fatalf("status %s, watches %d", e.Status, len(f.watches(e)))
	}
	// Played flag set by Jellyfin (no ticks needed).
	ev.Played = true
	f.process(ev, Options{})
	if got := len(f.watches(e)); got != 1 {
		t.Fatalf("watches = %d", got)
	}
}

func TestPlayIsForwardOnlyAndNeverReopens(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.find.AddEpisodeResult("imdb_id", "tt1", 1668, 1, 1)
	f.find.AddEpisodeResult("imdb_id", "tt2", 1668, 1, 3)
	play := func(imdb string) {
		f.process(Event{Kind: "Play", Type: "Episode", SeriesName: "Friends", Imdb: imdb}, Options{})
	}
	// Play on a new S1 show creates it In progress with S1 In progress.
	play("tt1")
	e := f.entry("tmdb", "1668")
	if e.Status != library.InProgress || f.seasonStatus(e, 1) != library.InProgress {
		t.Fatalf("after play: %s / %s", e.Status, f.seasonStatus(e, 1))
	}
	// Finish the season (finale S1E3) then more plays change nothing.
	f.process(Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Imdb: "tt2", Played: true}, Options{})
	if got := f.seasonStatus(f.entry("tmdb", "1668"), 1); got != library.Completed {
		t.Fatalf("S1 should be completed by the finale, is %s", got)
	}
	play("tt1")
	if got := f.seasonStatus(f.entry("tmdb", "1668"), 1); got != library.Completed {
		t.Errorf("play reopened a completed season: %s", got)
	}
}

func TestNeverUndoRewatchAndDuplicates(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.find.AddEpisodeResult("imdb_id", "tt1", 1668, 1, 1)
	f.find.AddEpisodeResult("imdb_id", "tt3", 1668, 1, 3)
	stop := func(imdb string, played bool, at time.Time) {
		f.process(Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Imdb: imdb, Played: played}, Options{WatchedAt: at})
	}
	stop("tt1", true, t0)
	// Webhook retry a couple of seconds later: one play.
	stop("tt1", true, t0.Add(2*time.Second))
	e := f.entry("tmdb", "1668")
	if got := len(f.watches(e)); got != 1 {
		t.Fatalf("duplicate stop recorded %d watches", got)
	}
	stop("tt3", true, t0.Add(time.Hour)) // finale: S1 completed
	// Only S1 exists, all aired: show completes? S2 and S3 are aired, so no.
	e = f.entry("tmdb", "1668")
	if f.seasonStatus(e, 1) != library.Completed {
		t.Fatalf("S1 = %s", f.seasonStatus(e, 1))
	}
	before := e
	nWatches := len(f.watches(e))
	// Rewind: an unfinished stop of an earlier episode changes nothing.
	stop("tt1", false, t0.Add(2*time.Hour))
	e = f.entry("tmdb", "1668")
	if e.Status != before.Status || e.Progress != before.Progress || f.seasonStatus(e, 1) != library.Completed || len(f.watches(e)) != nWatches {
		t.Errorf("unfinished stop changed state: %+v -> %+v", before, e)
	}
	// A full rewatch later adds a watch but undoes nothing.
	stop("tt1", true, t0.Add(48*time.Hour))
	e = f.entry("tmdb", "1668")
	if f.seasonStatus(e, 1) != library.Completed || e.Status == library.Planning || len(f.watches(e)) != nWatches+1 {
		t.Errorf("rewatch: status %s, S1 %s, watches %d", e.Status, f.seasonStatus(e, 1), len(f.watches(e)))
	}
}

func TestCompletedShowStaysCompletedOnRewatch(t *testing.T) {
	f := newFixture(t)
	f.env.TMDB.AddShow("5", "Mini", catalogtest.Season{Number: 1, AirDate: "2000-01-01", Episodes: catalogtest.Eps(1, 2, "2000-01-01")})
	f.find.AddEpisodeResult("imdb_id", "a", 5, 1, 1)
	f.find.AddEpisodeResult("imdb_id", "b", 5, 1, 2)
	st := func(imdb string, at time.Time) {
		f.process(Event{Kind: "Stop", Type: "Episode", SeriesName: "Mini", Imdb: imdb, Played: true}, Options{WatchedAt: at})
	}
	st("a", t0)
	st("b", t0.Add(time.Hour))
	if e := f.entry("tmdb", "5"); e.Status != library.Completed {
		t.Fatalf("show = %s", e.Status)
	}
	st("a", t0.Add(24*time.Hour))
	f.process(Event{Kind: "Play", Type: "Episode", SeriesName: "Mini", Imdb: "a"}, Options{})
	if e := f.entry("tmdb", "5"); e.Status != library.Completed {
		t.Errorf("rewatch reopened the show: %s", e.Status)
	}
}

func TestFirstSightingAtSeason3AssumesEarlierSeasons(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.find.AddEpisodeResult("imdb_id", "s3e2", 1668, 3, 2)
	// A Play at S3 for an untracked show creates nothing yet.
	f.process(Event{Kind: "Play", Type: "Episode", SeriesName: "Friends", Imdb: "s3e2"}, Options{})
	f.noEntry("tmdb", "1668")
	// The first finished play does, and assumes S1-S2.
	f.process(Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Imdb: "s3e2", Played: true}, Options{})
	e := f.entry("tmdb", "1668")
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
	f.find.AddEpisodeResult("imdb_id", "s1e1", 1668, 1, 1)
	before := len(f.watches(e))
	f.process(Event{Kind: "Stop", Type: "Episode", SeriesName: "Friends", Imdb: "s1e1", Played: true}, Options{})
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

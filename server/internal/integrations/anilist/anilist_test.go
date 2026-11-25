package anilist

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/catalog/mappings"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

func intp(n int) *int { return &n }

func TestEpisodeSlots(t *testing.T) {
	t.Run("part two starts after the offset", func(t *testing.T) {
		slots := episodeSlots(Start{3, 12}, map[int]int{1: 25, 2: 12, 3: 24}, 12, nil)
		if slots[0] != (Slot{3, 13}) || slots[len(slots)-1] != (Slot{3, 24}) {
			t.Fatalf("slots = %v", slots)
		}
	})
	t.Run("a long runner spills into the next seasons", func(t *testing.T) {
		slots := episodeSlots(Start{1, 0}, map[int]int{0: 4, 1: 25, 2: 25, 3: 30}, 60, nil)
		if len(slots) != 60 || slots[25] != (Slot{2, 1}) || slots[59] != (Slot{3, 10}) {
			t.Fatalf("slots = %v", slots)
		}
	})
	t.Run("specials never spill", func(t *testing.T) {
		slots := episodeSlots(Start{0, 2}, map[int]int{0: 3, 1: 12}, 4, nil)
		if len(slots) != 1 || slots[0] != (Slot{0, 3}) {
			t.Fatalf("slots = %v", slots)
		}
	})
	t.Run("the spill stops at a season another entry claims", func(t *testing.T) {
		slots := episodeSlots(Start{1, 0}, map[int]int{1: 12, 2: 13}, 13, map[int]bool{2: true})
		if len(slots) != 12 {
			t.Fatalf("len = %d", len(slots))
		}
	})
	t.Run("a season of unknown length takes everything", func(t *testing.T) {
		slots := episodeSlots(Start{1, 0}, map[int]int{1: 0}, 5, nil)
		if len(slots) != 5 {
			t.Fatalf("len = %d", len(slots))
		}
	})
}

func TestStartOf(t *testing.T) {
	if s, ok := startOf(nil, 0, "TV"); !ok || s != (Start{1, 0}) {
		t.Fatalf("TV without season: %v %v", s, ok)
	}
	if _, ok := startOf(nil, 0, "MOVIE"); ok {
		t.Fatal("a film without a season has no start")
	}
	if s, ok := startOf(intp(0), 3, "SPECIAL"); !ok || s != (Start{0, 3}) {
		t.Fatalf("specials: %v %v", s, ok)
	}
}

func testRun() *run {
	return &run{now: t0, loc: time.UTC, bulk: map[dateKey]bool{}}
}

func dateEntry(status string, start int, completed fuzzyDate, updated int64) entry {
	e := entry{Status: status, UpdatedAt: updated, CompletedAt: completed}
	e.Media.Episodes = intp(12)
	e.Media.StartDate = fuzzyDate{start, 4, 1}
	e.Media.EndDate = fuzzyDate{start, 6, 20}
	return e
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestWatchDates(t *testing.T) {
	r := testRun()
	tests := []struct {
		name string
		e    entry
		air  time.Time
		bulk bool
		want time.Time // the date part; noon unless noted
	}{
		{"a recent series uses the air date", dateEntry("COMPLETED", 2016, fuzzyDate{}, 0), day(2016, 4, 8), false, day(2016, 4, 8)},
		{"without an air date the series end is used", dateEntry("COMPLETED", 2016, fuzzyDate{}, 0), time.Time{}, false, day(2016, 6, 20)},
		{"an old series gets 2019-01-01", dateEntry("COMPLETED", 2004, fuzzyDate{}, 0), day(2004, 5, 1), false, OldSeriesDate},
		{"a real completion date is kept", dateEntry("COMPLETED", 2004, fuzzyDate{2023, 7, 4}, 0), day(2004, 5, 1), false, day(2023, 7, 4)},
		{"a bulk completion date is ignored", dateEntry("COMPLETED", 2016, fuzzyDate{2020, 2, 6}, 0), day(2016, 4, 8), true, day(2016, 4, 8)},
		{"a bulk date on an old series falls to 2019", dateEntry("COMPLETED", 2004, fuzzyDate{2020, 2, 6}, 0), day(2004, 4, 8), true, OldSeriesDate},
		{"2012 counts as recent", dateEntry("COMPLETED", 2012, fuzzyDate{}, 0), day(2012, 1, 5), false, day(2012, 1, 5)},
		{"2011 does not", dateEntry("COMPLETED", 2011, fuzzyDate{}, 0), day(2011, 1, 5), false, OldSeriesDate},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r.bulk = map[dateKey]bool{}
			if tc.bulk {
				r.bulk[fuzzyDate{2020, 2, 6}.key()] = true
			}
			got := r.watchDate(tc.e, tc.air)
			want := time.Date(tc.want.Year(), tc.want.Month(), tc.want.Day(), 12, 0, 0, 0, time.UTC)
			if !got.Equal(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}

	t.Run("watching uses the last update", func(t *testing.T) {
		stamp := time.Date(2023, 11, 1, 8, 30, 0, 0, time.UTC)
		got := r.watchDate(dateEntry("CURRENT", 2016, fuzzyDate{}, stamp.Unix()), day(2016, 4, 8))
		if !got.Equal(stamp) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("watching without an update is now", func(t *testing.T) {
		if got := r.watchDate(dateEntry("CURRENT", 2016, fuzzyDate{}, 0), day(2016, 4, 8)); !got.Equal(t0) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("never in the future", func(t *testing.T) {
		got := r.watchDate(dateEntry("COMPLETED", 2026, fuzzyDate{}, 0), t0.AddDate(0, 0, 30))
		if got.After(t0) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("a completion date is read at noon in the zone", func(t *testing.T) {
		z := testRun()
		z.loc = time.FixedZone("x", -5*3600)
		got := z.watchDate(dateEntry("COMPLETED", 2004, fuzzyDate{2023, 7, 4}, 0), time.Time{})
		if !got.Equal(time.Date(2023, 7, 4, 17, 0, 0, 0, time.UTC)) {
			t.Fatalf("got %v", got)
		}
	})
}

// --- end to end ---------------------------------------------------------

type env struct {
	*catalogtest.Env
	lib  *library.Service
	user int64
	imp  *Importer
	list *aniList
}

// aniList is a fake AniList GraphQL endpoint serving the list below.
type aniList struct {
	srv          *httptest.Server
	anime, manga []map[string]any
	calls        int
}

func (a *aniList) GraphQL(ctx context.Context, query string, vars map[string]any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.srv.URL, strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func collectionOf(entries []map[string]any) map[string]any {
	return map[string]any{"lists": []any{
		map[string]any{"isCustomList": false, "entries": entries},
		map[string]any{"isCustomList": true, "entries": []any{map[string]any{"status": "COMPLETED", "media": map[string]any{"id": 999, "title": map[string]any{"userPreferred": "custom list dup"}}}}},
	}}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ce := catalogtest.NewEnv(t, t0)
	lib := library.New(ce.Conn, ce.Q, ce.Catalog)
	lib.Now = ce.Now
	e := &env{Env: ce, lib: lib, user: ce.User(t, "owner"), list: &aniList{}}
	e.list.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.list.calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"anime": collectionOf(e.list.anime), "manga": collectionOf(e.list.manga),
		}})
	}))
	t.Cleanup(e.list.srv.Close)
	e.imp = &Importer{
		Q: ce.Q, Library: lib, Catalog: ce.Catalog, Mappings: ce.Mappings, AniList: e.list,
		Users: nil, Now: ce.Now, Loc: time.UTC,
	}
	return e
}

type opt func(m map[string]any)

func listEntry(id int, mal int, format, status string, progress int, eps int, opts ...opt) map[string]any {
	media := map[string]any{
		"id": id, "idMal": nil, "format": format, "episodes": eps, "chapters": nil,
		"title":      map[string]any{"userPreferred": "Title " + format + " " + string(rune('A'+id%26))},
		"coverImage": map[string]any{"large": "https://img/" + status},
		"startDate":  map[string]any{"year": 2016, "month": 4, "day": 1},
		"endDate":    map[string]any{"year": 2016, "month": 6, "day": 20},
	}
	if mal != 0 {
		media["idMal"] = mal
	}
	m := map[string]any{
		"status": status, "score": 0, "progress": progress, "repeat": 0, "notes": "", "updatedAt": 0,
		"startedAt": map[string]any{"year": nil, "month": nil, "day": nil}, "completedAt": map[string]any{"year": nil, "month": nil, "day": nil},
		"media": media,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func withMedia(k string, v any) opt {
	return func(m map[string]any) { m["media"].(map[string]any)[k] = v }
}
func with(k string, v any) opt { return func(m map[string]any) { m[k] = v } }
func completed(y, mo, d int) opt {
	return with("completedAt", map[string]any{"year": y, "month": mo, "day": d})
}

func (e *env) show(id, title string, seasons ...catalogtest.Season) {
	e.TMDB.AddShow(id, title, seasons...)
}

func sn(number int, date string, first, n int) catalogtest.Season {
	return catalogtest.Season{Number: number, AirDate: date, Episodes: catalogtest.Eps(first, n, date)}
}

func (e *env) run(t *testing.T, opts Options) *Result {
	t.Helper()
	res, err := e.imp.Run(ctx, e.user, "someone", opts)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) item(t *testing.T, kind, source, id string) catalog.Item {
	t.Helper()
	item, err := e.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: id, Kind: kind})
	if err != nil {
		t.Fatalf("item %s/%s/%s: %v", kind, source, id, err)
	}
	return item
}

func (e *env) entry(t *testing.T, item catalog.Item) db.Entry {
	t.Helper()
	en, err := e.lib.CurrentEntry(ctx, e.user, item.ID)
	if err != nil {
		t.Fatalf("entry of %s: %v", item.Title, err)
	}
	return en
}

func (e *env) noEntry(t *testing.T, item catalog.Item) {
	t.Helper()
	if _, err := e.lib.CurrentEntry(ctx, e.user, item.ID); err == nil {
		t.Fatalf("%s should have no entry", item.Title)
	}
}

func (e *env) watches(t *testing.T, item catalog.Item) []db.Watch {
	t.Helper()
	en, err := e.lib.CurrentEntry(ctx, e.user, item.ID)
	if err != nil {
		return nil
	}
	ws, err := e.Q.ListEntryWatches(ctx, en.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func watched(ws []db.Watch) []int {
	var out []int
	for _, w := range ws {
		out = append(out, int(w.Season.Int64)*1000+int(w.Episode.Int64))
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunPlacesEntriesInTMDBShows(t *testing.T) {
	e := newEnv(t)
	// Show 100: S1 has 12 episodes, S2 has 12 (cour 2 of S1 is AniList 11), S3 has 10.
	e.show("100", "Big Show", sn(1, "2016-04-01", 1, 24), sn(2, "2017-04-01", 1, 12), sn(3, "2018-04-01", 1, 10))
	e.Mappings.AddShow(100, 1, 0, 11, 111)  // S1, first cour
	e.Mappings.AddShow(100, 1, 12, 12, 112) // S1, second cour (offset 12)
	e.Mappings.AddShow(100, 3, 0, 13, 113)  // S3 claimed by its own entry
	e.list.anime = []map[string]any{
		listEntry(11, 111, "TV", "COMPLETED", 12, 12),
		listEntry(12, 112, "TV", "CURRENT", 5, 12, with("updatedAt", time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC).Unix())),
		listEntry(13, 113, "TV", "PLANNING", 0, 10),
	}
	res := e.run(t, Options{})

	item := e.item(t, "anime", "tmdb", "100")
	ws := e.watches(t, item)
	if len(ws) != 12+5 {
		t.Fatalf("watches = %d (%v)", len(ws), watched(ws))
	}
	// Cour 2 starts at position 13 of season 1 and is dated by the AniList update.
	if ws[12].Season.Int64 != 1 || ws[12].Episode.Int64 != 13 || !strings.HasPrefix(ws[12].WatchedAt, "2024-05-01") {
		t.Fatalf("second cour first watch = %+v", ws[12])
	}
	// Cour 1 is dated by the episode air date (series from 2016).
	if !strings.HasPrefix(ws[0].WatchedAt, "2016-04-01") || ws[0].Source != Source {
		t.Fatalf("first watch = %+v", ws[0])
	}
	if en := e.entry(t, item); en.Status != library.InProgress {
		t.Fatalf("show status = %s", en.Status)
	}
	if res.Counts["episodes"] != 17 || res.Counts["shows"] != 1 {
		t.Fatalf("counts = %v", res.Counts)
	}
	for _, w := range ws {
		if w.Source == library.SourceAssumed {
			t.Fatal("assumed watches must not be created for AniList")
		}
	}

	// Re-running adds nothing and lowers nothing.
	res = e.run(t, Options{})
	if res.Counts["episodes"] != 0 {
		t.Fatalf("second run counts = %v", res.Counts)
	}
}

func TestSpillStopsAtClaimedSeasonAndLongRunnersStartAtOne(t *testing.T) {
	e := newEnv(t)
	e.show("200", "Long Runner", sn(1, "2007-01-01", 1, 25), sn(2, "2008-01-01", 1, 25), sn(3, "2009-01-01", 1, 30))
	// A long runner (no season in the mapping) and another entry owning S3.
	e.Mappings.Rows = append(e.Mappings.Rows, mappings.Mapping{
		ID: 1, AnilistID: sql.NullInt64{Int64: 21, Valid: true}, Kind: "TV", TmdbTv: sql.NullInt64{Int64: 200, Valid: true},
	})
	e.Mappings.AddShow(200, 3, 0, 22, 0)
	e.list.anime = []map[string]any{
		listEntry(21, 0, "TV", "COMPLETED", 60, 60, withMedia("startDate", map[string]any{"year": 2002, "month": 10, "day": 3})),
		listEntry(22, 0, "TV", "PLANNING", 0, 30),
	}
	e.run(t, Options{})
	ws := e.watches(t, e.item(t, "anime", "tmdb", "200"))
	// 60 episodes would reach S3, but S3 is claimed: only S1 and S2 (50 episodes).
	if len(ws) != 50 {
		t.Fatalf("watches = %d", len(ws))
	}
	for _, w := range ws {
		if w.Season.Int64 == 3 {
			t.Fatal("spilled into a claimed season")
		}
		if !strings.HasPrefix(w.WatchedAt, "2019-01-01") {
			t.Fatalf("old series should be dated 2019-01-01, got %s", w.WatchedAt)
		}
	}
}

func TestContinuousNumberingUsesPositions(t *testing.T) {
	e := newEnv(t)
	// Season 2 is numbered on from season 1: episodes 33..35.
	e.show("300", "Shippuden-like", sn(1, "2007-02-15", 1, 32), sn(2, "2009-02-01", 33, 3))
	e.Mappings.AddShow(300, 2, 0, 31, 0)
	e.list.anime = []map[string]any{listEntry(31, 0, "TV", "CURRENT", 2, 3)}
	e.run(t, Options{})
	got := watched(e.watches(t, e.item(t, "anime", "tmdb", "300")))
	if !equalInts(got, []int{2033, 2034}) {
		t.Fatalf("watched = %v", got)
	}
}

func TestSecondSeasonFirstDoesNotCreateSeasonOne(t *testing.T) {
	e := newEnv(t)
	e.show("310", "Mid Show", sn(1, "2016-01-01", 1, 12), sn(2, "2017-01-01", 1, 12))
	e.Mappings.AddShow(310, 2, 0, 32, 0)
	e.list.anime = []map[string]any{listEntry(32, 0, "TV", "COMPLETED", 12, 12)}
	e.run(t, Options{})
	item := e.item(t, "anime", "tmdb", "310")
	ws := e.watches(t, item)
	if len(ws) != 12 {
		t.Fatalf("watches = %d", len(ws))
	}
	seasons, _ := e.Q.ListEntrySeasons(ctx, e.entry(t, item).ID)
	for _, s := range seasons {
		if s.Season == 1 {
			t.Fatal("season 1 must not be created")
		}
	}
}

func TestSeasonMissingInTMDBFallsBackToCountBased(t *testing.T) {
	e := newEnv(t)
	e.show("320", "Merged", sn(1, "2016-01-01", 1, 12))
	e.Mappings.AddShow(320, 4, 0, 33, 333) // TMDB has no season 4
	e.list.anime = []map[string]any{listEntry(33, 333, "TV", "CURRENT", 7, 12, withMedia("episodes", 24))}
	res := e.run(t, Options{})
	if res.Counts["anime (unmapped)"] != 1 {
		t.Fatalf("counts = %v", res.Counts)
	}
	item := e.item(t, "anime", "mal", "333")
	if en := e.entry(t, item); en.Progress != 7 || en.Status != library.InProgress {
		t.Fatalf("entry = %+v", en)
	}
	if item.Total.Int64 != 24 {
		t.Fatalf("total = %d", item.Total.Int64)
	}
}

func TestMoviesAndUnmappedAndManga(t *testing.T) {
	e := newEnv(t)
	e.TMDB.AddMovie("400", "A Film", "2016-08-26")
	e.Mappings.AddMovie(400, 41, 441)
	e.TMDB.AddMovie("401", "Planned Film", "2018-01-01")
	e.Mappings.AddMovie(401, 42, 0)
	e.list.anime = []map[string]any{
		listEntry(41, 441, "MOVIE", "COMPLETED", 1, 1, completed(2023, 7, 4)),
		listEntry(42, 0, "MOVIE", "PAUSED", 0, 1),
		listEntry(43, 0, "TV", "DROPPED", 3, 12), // unmapped, no MAL id: source anilist
	}
	e.list.manga = []map[string]any{
		listEntry(51, 551, "MANGA", "CURRENT", 33, 0, withMedia("chapters", 100), with("score", 8.5), with("notes", "great")),
		listEntry(52, 0, "MANGA", "COMPLETED", 0, 0, withMedia("chapters", 20), completed(2022, 3, 1),
			with("startedAt", map[string]any{"year": 2022, "month": 1, "day": nil})),
	}
	res := e.run(t, Options{})

	film := e.item(t, "anime_movie", "tmdb", "400")
	fe := e.entry(t, film)
	if fe.Status != library.Completed || fe.Progress != 1 || !strings.HasPrefix(fe.FinishedAt.String, "2023-07-04") {
		t.Fatalf("film = %+v", fe)
	}
	planned := e.entry(t, e.item(t, "anime_movie", "tmdb", "401"))
	if planned.Status != library.Planning { // Python maps a paused film to Planning
		t.Fatalf("paused film status = %s", planned.Status)
	}
	dropped := e.entry(t, e.item(t, "anime", "anilist", "43"))
	if dropped.Status != library.Dropped || dropped.Progress != 3 {
		t.Fatalf("unmapped = %+v", dropped)
	}
	m1 := e.entry(t, e.item(t, "manga", "mal", "551"))
	if m1.Progress != 33 || m1.Status != library.InProgress || m1.Score.Float64 != 8.5 || m1.Notes != "great" {
		t.Fatalf("manga 1 = %+v", m1)
	}
	m2 := e.entry(t, e.item(t, "manga", "anilist", "52"))
	if m2.Status != library.Completed || m2.Progress != 20 || m2.Score.Valid {
		t.Fatalf("manga 2 = %+v", m2)
	}
	if !strings.HasPrefix(m2.StartedAt.String, "2022-01-01") || !strings.HasPrefix(m2.FinishedAt.String, "2022-03-01") {
		t.Fatalf("manga 2 dates = %v / %v", m2.StartedAt, m2.FinishedAt)
	}
	if res.Counts["manga"] != 2 || res.Counts["movies"] != 2 || res.Counts["anime (unmapped)"] != 1 {
		t.Fatalf("counts = %v", res.Counts)
	}
	// The custom list duplicate was ignored.
	if _, err := e.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: "anilist", ExternalID: "999", Kind: "anime"}); err == nil {
		t.Fatal("custom list entries must be ignored")
	}
}

func TestBulkCompletionDatesAreIgnoredForShows(t *testing.T) {
	e := newEnv(t)
	for i, id := range []string{"500", "501", "502", "503"} {
		e.show(id, "Bulk "+id, sn(1, "2016-04-01", 1, 2))
		e.Mappings.AddShow(int64(500+i), 1, 0, int64(60+i), 0)
	}
	// Three entries share one completion date (bulk), one has its own.
	e.list.anime = []map[string]any{
		listEntry(60, 0, "TV", "COMPLETED", 2, 2, completed(2020, 2, 6)),
		listEntry(61, 0, "TV", "COMPLETED", 2, 2, completed(2020, 2, 6)),
		listEntry(62, 0, "TV", "COMPLETED", 2, 2, completed(2020, 2, 6)),
		listEntry(63, 0, "TV", "COMPLETED", 2, 2, completed(2021, 5, 5)),
	}
	e.run(t, Options{})
	if ws := e.watches(t, e.item(t, "anime", "tmdb", "500")); !strings.HasPrefix(ws[0].WatchedAt, "2016-04-01") {
		t.Fatalf("bulk date should fall back to the air date, got %s", ws[0].WatchedAt)
	}
	if ws := e.watches(t, e.item(t, "anime", "tmdb", "503")); !strings.HasPrefix(ws[0].WatchedAt, "2021-05-05") {
		t.Fatalf("entry-specific date should be kept, got %s", ws[0].WatchedAt)
	}
}

func TestAdditiveNeverLowers(t *testing.T) {
	e := newEnv(t)
	e.show("600", "Owned", sn(1, "2016-04-01", 1, 12))
	e.Mappings.AddShow(600, 1, 0, 71, 0)
	item, err := e.Catalog.EnsureItem(ctx, "anime", "tmdb", "600")
	if err != nil {
		t.Fatal(err)
	}
	// The owner already watched all 12 episodes; the show is Completed.
	for n := 1; n <= 12; n++ {
		if _, err := e.lib.WatchEpisode(ctx, e.user, item.ID, 1, n, t0.Add(-time.Hour), library.SourceManual, library.WatchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before := e.entry(t, item)
	// AniList lags behind: PLANNING with progress 3.
	e.list.anime = []map[string]any{listEntry(71, 0, "TV", "PLANNING", 3, 12)}
	// A count-based manga the owner has further along than AniList.
	e.list.manga = []map[string]any{listEntry(72, 0, "MANGA", "CURRENT", 5, 0, withMedia("chapters", 50))}
	mitem, err := e.Q.InsertItem(ctx, db.InsertItemParams{Kind: "manga", Source: "anilist", ExternalID: "72", Title: "M", Meta: "{}", Total: sql.NullInt64{Int64: 50, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.SetProgress(ctx, e.user, mitem.ID, 40, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	e.run(t, Options{})
	after := e.entry(t, item)
	if after.Status != library.Completed || after.Status != before.Status || len(e.watches(t, item)) != 12 {
		t.Fatalf("show changed: %+v -> %+v (%d watches)", before, after, len(e.watches(t, item)))
	}
	if m := e.entry(t, mitem); m.Progress != 40 {
		t.Fatalf("manga progress lowered to %d", m.Progress)
	}
	// And it raises: AniList is ahead.
	e.list.manga = []map[string]any{listEntry(72, 0, "MANGA", "CURRENT", 45, 0, withMedia("chapters", 50))}
	e.run(t, Options{})
	if m := e.entry(t, mitem); m.Progress != 45 {
		t.Fatalf("manga progress = %d", m.Progress)
	}
}

func TestPausedAndDroppedShowsStartWithThatStatus(t *testing.T) {
	e := newEnv(t)
	e.show("700", "Paused Show", sn(1, "2016-04-01", 1, 12))
	e.Mappings.AddShow(700, 1, 0, 81, 0)
	e.show("701", "Planned Show", sn(1, "2016-04-01", 1, 12))
	e.Mappings.AddShow(701, 1, 0, 82, 0)
	e.list.anime = []map[string]any{
		listEntry(81, 0, "TV", "PAUSED", 4, 12),
		listEntry(82, 0, "TV", "PLANNING", 0, 12),
	}
	e.run(t, Options{})
	if en := e.entry(t, e.item(t, "anime", "tmdb", "700")); en.Status != library.Paused {
		t.Fatalf("status = %s", en.Status)
	}
	p := e.item(t, "anime", "tmdb", "701")
	if en := e.entry(t, p); en.Status != library.Planning {
		t.Fatalf("status = %s", en.Status)
	}
	if len(e.watches(t, p)) != 0 {
		t.Fatal("a planned show has no watches")
	}
}

func TestDryRunWritesNothingToTheLibrary(t *testing.T) {
	e := newEnv(t)
	e.show("800", "Dry Show", sn(1, "2016-04-01", 1, 12))
	e.Mappings.AddShow(800, 1, 0, 91, 0)
	e.TMDB.AddMovie("801", "Dry Film", "2016-01-01")
	e.Mappings.AddMovie(801, 92, 0)
	e.list.anime = []map[string]any{
		listEntry(91, 0, "TV", "COMPLETED", 12, 12),
		listEntry(92, 0, "MOVIE", "COMPLETED", 1, 1),
		listEntry(93, 0, "TV", "CURRENT", 2, 12),
	}
	e.list.manga = []map[string]any{listEntry(94, 0, "MANGA", "CURRENT", 9, 0)}
	res := e.run(t, Options{DryRun: true})
	if !res.DryRun || res.Counts["episodes"] != 12 || res.Counts["movies"] != 1 || res.Counts["manga"] != 1 || res.Counts["anime (unmapped)"] != 1 {
		t.Fatalf("counts = %+v", res.Counts)
	}
	if len(res.Changes) < 4 {
		t.Fatalf("changes = %+v", res.Changes)
	}
	var entries, watches, items int
	_ = e.Conn.QueryRow(`SELECT count(*) FROM entries`).Scan(&entries)
	_ = e.Conn.QueryRow(`SELECT count(*) FROM watches`).Scan(&watches)
	_ = e.Conn.QueryRow(`SELECT count(*) FROM items WHERE source IN ('anilist','mal')`).Scan(&items)
	if entries != 0 || watches != 0 || items != 0 {
		t.Fatalf("dry run wrote entries=%d watches=%d items=%d", entries, watches, items)
	}
	// The dry run predicts what a real run then does.
	real := e.run(t, Options{})
	if real.Counts["episodes"] != res.Counts["episodes"] {
		t.Fatalf("real %v vs dry %v", real.Counts, res.Counts)
	}
	// A second dry run has nothing left to do.
	again := e.run(t, Options{DryRun: true})
	if again.Counts["episodes"] != 0 || again.Counts["movies"] != 0 || len(again.Changes) != 0 {
		t.Fatalf("second dry run = %+v", again)
	}
}

func TestSyncIncrementalMarkerAndStatus(t *testing.T) {
	e := newEnv(t)
	e.show("900", "Inc Show", sn(1, "2016-04-01", 1, 12))
	e.Mappings.AddShow(900, 1, 0, 95, 0)
	u1 := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	e.list.anime = []map[string]any{listEntry(95, 0, "TV", "CURRENT", 3, 12, with("updatedAt", u1.Unix()))}
	e.list.manga = []map[string]any{listEntry(96, 0, "MANGA", "CURRENT", 4, 0, with("updatedAt", u1.Add(-24*time.Hour).Unix()))}

	res, err := e.imp.Sync(ctx, e.user, "someone", SyncOptions{SinceLast: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["episodes"] != 3 || res.Counts["manga"] != 1 {
		t.Fatalf("first run = %v", res.Counts)
	}
	if m := e.imp.Marker(ctx, e.user); m == nil || !m.Equal(u1) {
		t.Fatalf("marker = %v", m)
	}
	// Status landed in kv.
	raw, err := e.Q.GetKV(ctx, "source:"+itoa(e.user)+":anilist")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Name     string         `json:"name"`
		OK       bool           `json:"ok"`
		LastRun  string         `json:"last_run"`
		Counts   map[string]int `json:"counts"`
		Failures []string       `json:"failures"`
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "anilist" || !st.OK || st.Counts["episodes"] != 3 || st.Failures == nil || st.LastRun != t0.Format(time.RFC3339) {
		t.Fatalf("status = %+v", st)
	}

	// Nothing edited since: nothing is processed, the marker stays.
	res, err = e.imp.Sync(ctx, e.user, "someone", SyncOptions{SinceLast: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Counts) != 0 {
		t.Fatalf("incremental no-op counts = %v", res.Counts)
	}
	// Progress moves on AniList: only the edited entry is processed.
	u2 := u1.Add(48 * time.Hour)
	e.list.anime = []map[string]any{listEntry(95, 0, "TV", "CURRENT", 5, 12, with("updatedAt", u2.Unix()))}
	res, err = e.imp.Sync(ctx, e.user, "someone", SyncOptions{SinceLast: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["episodes"] != 2 || res.Counts["manga"] != 0 {
		t.Fatalf("third run = %v", res.Counts)
	}
	if m := e.imp.Marker(ctx, e.user); !m.Equal(u2) {
		t.Fatalf("marker = %v", m)
	}
	// A full run ignores the marker.
	res, err = e.imp.Sync(ctx, e.user, "someone", SyncOptions{})
	if err != nil || res.Counts["manga"] != 1 {
		t.Fatalf("full run = %v %v", res, err)
	}
	// A dry run neither moves the marker nor writes a status.
	e.list.anime = []map[string]any{listEntry(95, 0, "TV", "CURRENT", 6, 12, with("updatedAt", u2.Add(time.Hour).Unix()))}
	if _, err := e.imp.Sync(ctx, e.user, "someone", SyncOptions{SinceLast: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if m := e.imp.Marker(ctx, e.user); !m.Equal(u2) {
		t.Fatalf("dry run moved the marker to %v", m)
	}
}

func TestSyncFailureIsRecorded(t *testing.T) {
	e := newEnv(t)
	e.imp.AniList = failing{}
	if _, err := e.imp.Sync(ctx, e.user, "someone", SyncOptions{}); err == nil {
		t.Fatal("expected an error")
	}
	raw, err := e.Q.GetKV(ctx, "source:"+itoa(e.user)+":anilist")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"ok":false`) || !strings.Contains(raw, "could not be read") {
		t.Fatalf("status = %s", raw)
	}
}

type failing struct{}

func (failing) GraphQL(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestOneBadEntryDoesNotStopTheImport(t *testing.T) {
	e := newEnv(t)
	e.Mappings.AddShow(1000, 1, 0, 97, 0) // TMDB has no show 1000
	e.list.anime = []map[string]any{listEntry(97, 0, "TV", "COMPLETED", 12, 12)}
	e.list.manga = []map[string]any{listEntry(98, 0, "MANGA", "CURRENT", 2, 0)}
	res := e.run(t, Options{})
	if len(res.Warnings) != 1 || res.Counts["manga"] != 1 {
		t.Fatalf("warnings = %v counts = %v", res.Warnings, res.Counts)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

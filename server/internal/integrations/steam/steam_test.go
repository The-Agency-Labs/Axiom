package steam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

// fakeIGDB maps Steam app ids to IGDB game ids and serves game metadata.
type fakeIGDB struct {
	byApp map[string]int
	err   error
}

func (f *fakeIGDB) ExternalGame(_ context.Context, id string, source int) (int, error) {
	if source != 1 {
		panic("not the Steam external game source")
	}
	return f.byApp[id], f.err
}

func (f *fakeIGDB) Game(_ context.Context, id string) (providers.Metadata, error) {
	return providers.Metadata{"media_id": id, "source": "igdb", "media_type": "game", "title": "Game " + id, "image": "https://img/" + id, "details": map[string]any{}}, nil
}

func (f *fakeIGDB) Search(context.Context, string, int, bool) (providers.SearchPage, error) {
	return providers.SearchPage{"results": []any{}}, nil
}

type fixture struct {
	imp   *Importer
	user  int64
	igdb  *fakeIGDB
	lib   *library.Service
	q     *db.Queries
	games []map[string]any
	// private makes the owned-games call fail with 403.
	private bool
	vanity  map[string]string
	calls   []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	f := &fixture{igdb: &fakeIGDB{byApp: map[string]int{}}, vanity: map[string]string{}, q: env.Q}
	env.Catalog.Reg.P.IGDB = f.igdb
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.URL.Path)
		if r.URL.Query().Get("key") != "secret" {
			http.Error(w, "forbidden", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/IPlayerService/GetOwnedGames/v0001/":
			if f.private {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"game_count": len(f.games), "games": f.games}})
		case "/ISteamUser/ResolveVanityURL/v0001/":
			if id, ok := f.vanity[r.URL.Query().Get("vanityurl")]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"success": 1, "steamid": id}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"success": 42}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	f.lib = library.New(env.Conn, env.Q, env.Catalog)
	f.lib.Now = env.Now
	f.imp = New(env.Catalog, f.lib, env.Q, providers.NewClient(nil), "secret")
	f.imp.BaseURL = srv.URL
	f.imp.Now = env.Now
	f.user = env.User(t, "owner")
	return f
}

func steamGame(app int, name string, forever, recent int) map[string]any {
	return map[string]any{"appid": app, "name": name, "playtime_forever": forever, "playtime_2weeks": recent}
}

const steamID = "76561198000000000"

func (f *fixture) entry(t *testing.T, igdbID string) db.Entry {
	t.Helper()
	item, err := f.q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: "igdb", ExternalID: igdbID, Kind: "game"})
	if err != nil {
		t.Fatalf("item %s: %v", igdbID, err)
	}
	e, err := f.lib.CurrentEntry(ctx, f.user, item.ID)
	if err != nil {
		t.Fatalf("entry %s: %v", igdbID, err)
	}
	return e
}

func TestGameStatus(t *testing.T) {
	for _, tc := range []struct {
		forever, recent int
		want            string
	}{{0, 0, library.Planning}, {100, 50, library.InProgress}, {100, 0, library.Paused}, {0, 5, library.Planning}} {
		if got := gameStatus(tc.forever, tc.recent); got != tc.want {
			t.Errorf("gameStatus(%d, %d) = %s, want %s", tc.forever, tc.recent, got, tc.want)
		}
	}
}

func TestImportGames(t *testing.T) {
	f := newFixture(t)
	f.games = []map[string]any{
		steamGame(730, "Counter-Strike 2", 1250, 120),
		steamGame(570, "Dota 2", 0, 0),
		steamGame(440, "Team Fortress 2", 500, 0),
		steamGame(999, "Unknown Game", 100, 0),
	}
	f.igdb.byApp = map[string]int{"730": 1, "570": 2, "440": 3}
	res, err := f.imp.Import(ctx, f.user, steamID)
	if err != nil {
		t.Fatal(err)
	}
	cs := f.entry(t, "1")
	if cs.Status != library.InProgress || cs.Progress != 1250 || cs.Notes != "Imported from Steam" {
		t.Fatalf("cs2 = %+v", cs)
	}
	if d := f.entry(t, "2"); d.Status != library.Planning || d.Progress != 0 {
		t.Fatalf("dota = %+v", d)
	}
	if tf := f.entry(t, "3"); tf.Status != library.Paused || tf.Progress != 500 {
		t.Fatalf("tf2 = %+v", tf)
	}
	// A game IGDB doesn't know is dropped quietly: no count, no warning.
	if res.Counts["games"] != 3 || res.Counts["unmatched"] != 0 {
		t.Fatalf("counts = %v", res.Counts)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func setup(t *testing.T, f *fixture, forever, recent int) catalog.Item {
	t.Helper()
	f.games = []map[string]any{steamGame(730, "Counter-Strike 2", forever, recent)}
	f.igdb.byApp = map[string]int{"730": 1}
	item, err := f.imp.Catalog.EnsureItem(ctx, "game", "igdb", "1")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestExistingGameRaisesPlaytimeAndAdvances(t *testing.T) {
	f := newFixture(t)
	item := setup(t, f, 1300, 100)
	if err := f.lib.SetProgress(ctx, f.user, item.ID, 1000, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	res, err := f.imp.Import(ctx, f.user, steamID)
	if err != nil {
		t.Fatal(err)
	}
	e := f.entry(t, "1")
	if e.Progress != 1300 || e.Status != library.InProgress || res.Counts["updated"] != 1 || res.Counts["games"] != 0 {
		t.Fatalf("e = %+v counts = %v", e, res.Counts)
	}
}

func TestPlaytimeNeverLowersAndCompletedStays(t *testing.T) {
	f := newFixture(t)
	item := setup(t, f, 1300, 100)
	if _, err := f.lib.Track(ctx, f.user, item.ID, library.Completed, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	if err := f.lib.SetProgress(ctx, f.user, item.ID, 2000, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	e := f.entry(t, "1")
	res, err := f.imp.Import(ctx, f.user, steamID)
	if err != nil {
		t.Fatal(err)
	}
	after := f.entry(t, "1")
	if after.Progress != e.Progress || after.Status != e.Status || len(res.Counts) != 0 {
		t.Fatalf("changed %+v -> %+v (%v)", e, after, res.Counts)
	}
}

func TestPlayedLongAgoPausesPlannedGame(t *testing.T) {
	f := newFixture(t)
	item := setup(t, f, 600, 0)
	if _, err := f.lib.Track(ctx, f.user, item.ID, library.Planning, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.imp.Import(ctx, f.user, steamID); err != nil {
		t.Fatal(err)
	}
	if e := f.entry(t, "1"); e.Progress != 600 || e.Status != library.Paused {
		t.Fatalf("e = %+v", e)
	}
	// An In progress game played long ago stays In progress.
	f2 := newFixture(t)
	item = setup(t, f2, 600, 0)
	if err := f2.lib.SetProgress(ctx, f2.user, item.ID, 100, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.imp.Import(ctx, f2.user, steamID); err != nil {
		t.Fatal(err)
	}
	if e := f2.entry(t, "1"); e.Progress != 600 || e.Status != library.InProgress {
		t.Fatalf("e = %+v", e)
	}
}

func TestPrivateProfileAndMissingKey(t *testing.T) {
	f := newFixture(t)
	f.private = true
	if _, err := f.imp.Import(ctx, f.user, steamID); err == nil || !strings.Contains(err.Error(), "private or invalid") {
		t.Fatalf("err = %v", err)
	}
	f.imp.Key = ""
	if _, err := f.imp.Import(ctx, f.user, steamID); err == nil || !strings.Contains(err.Error(), "API key not configured") {
		t.Fatalf("err = %v", err)
	}
	f.imp.Key = "wrong"
	if _, err := f.imp.Import(ctx, f.user, steamID); err == nil || !strings.Contains(err.Error(), "Invalid Steam API key") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoGamesIsAWarning(t *testing.T) {
	f := newFixture(t)
	res, err := f.imp.Import(ctx, f.user, steamID)
	if err != nil || len(res.Warnings) != 1 || len(res.Counts) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestResolveVanityName(t *testing.T) {
	f := newFixture(t)
	f.vanity["somename"] = steamID
	for _, in := range []string{steamID, "somename", "https://steamcommunity.com/id/somename/", "https://steamcommunity.com/profiles/" + steamID} {
		got, err := f.imp.Resolve(ctx, in)
		if err != nil || got != steamID {
			t.Errorf("Resolve(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := f.imp.Resolve(ctx, "nobody"); err == nil {
		t.Fatal("unknown vanity name should fail")
	}
	if got, _ := f.imp.Resolve(ctx, steamID); got != steamID {
		t.Fatal("a SteamID64 needs no lookup")
	}
}

func TestSyncSavesResolvedIDAndWritesStatus(t *testing.T) {
	f := newFixture(t)
	f.vanity["somename"] = steamID
	f.games = []map[string]any{steamGame(730, "Counter-Strike 2", 90, 10)}
	f.igdb.byApp = map[string]int{"730": 1}
	if _, err := f.imp.Sync(ctx, f.user); err != ErrNoSteamID {
		t.Fatalf("err = %v", err)
	}
	if err := f.imp.SaveID(ctx, f.user, "somename"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.imp.Sync(ctx, f.user); err != nil {
		t.Fatal(err)
	}
	if id, _ := f.imp.SavedID(ctx, f.user); id != steamID {
		t.Fatalf("saved id = %q", id)
	}
	raw, err := f.q.GetKV(ctx, "source:"+strconv.FormatInt(f.user, 10)+":steam")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Name     string         `json:"name"`
		OK       bool           `json:"ok"`
		Counts   map[string]int `json:"counts"`
		Failures []string       `json:"failures"`
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "steam" || !st.OK || st.Counts["games"] != 1 || st.Failures == nil {
		t.Fatalf("status = %s", raw)
	}
	users, err := f.imp.UsersWithSavedID(ctx)
	if err != nil || len(users) != 1 || users[0] != f.user {
		t.Fatalf("users = %v %v", users, err)
	}
	// A failure is recorded too.
	f.private = true
	if _, err := f.imp.Sync(ctx, f.user); err == nil {
		t.Fatal("expected an error")
	}
	raw, _ = f.q.GetKV(ctx, "source:"+strconv.FormatInt(f.user, 10)+":steam")
	if !strings.Contains(raw, `"ok":false`) || !strings.Contains(raw, "private or invalid") {
		t.Fatalf("status = %s", raw)
	}
}

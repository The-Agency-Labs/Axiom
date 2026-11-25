package jellyfin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/library"
)

// fakeJellyfin serves /Users and /Items for one Jellyfin user.
type fakeJellyfin struct {
	mu       sync.Mutex
	series   []map[string]any
	played   []map[string]any
	badAuth  int
	isPlayed int // /Items requests that carried Filters=IsPlayed
}

func (j *fakeJellyfin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if r.Header.Get("Authorization") != `MediaBrowser Token="test-key"` {
		j.badAuth++
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/Users":
		json.NewEncoder(w).Encode([]map[string]any{{"Id": "u-other", "Name": "someone"}, {"Id": "u1", "Name": "JFUser"}})
	case "/Items":
		q := r.URL.Query()
		if q.Get("userId") != "u1" || q.Get("Fields") != "ProviderIds,ProductionYear" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		items := j.series
		if q.Get("IncludeItemTypes") != "Series" {
			if q.Get("Filters") == "IsPlayed" {
				j.isPlayed++
			}
			items = j.played
		}
		start, _ := strconv.Atoi(q.Get("StartIndex"))
		limit, _ := strconv.Atoi(q.Get("Limit"))
		end := min(start+limit, len(items))
		if start > len(items) {
			start = len(items)
		}
		json.NewEncoder(w).Encode(map[string]any{"Items": items[start:end], "TotalRecordCount": len(items)})
	default:
		http.NotFound(w, r)
	}
}

func jfEpisode(id string, season, episode int, played bool, last string) map[string]any {
	return map[string]any{
		"Id": id, "Type": "Episode", "Name": "Ep", "SeriesName": "Friends", "SeriesId": "s1",
		"ParentIndexNumber": season, "IndexNumber": episode, "ProviderIds": map[string]string{},
		"UserData": map[string]any{"Played": played, "LastPlayedDate": last},
	}
}

func jfMovie(id, name, tmdb, last string) map[string]any {
	return map[string]any{
		"Id": id, "Type": "Movie", "Name": name, "ProductionYear": 1999,
		"ProviderIds": map[string]string{"Tmdb": tmdb},
		"UserData":    map[string]any{"Played": true, "LastPlayedDate": last},
	}
}

func (f *fixture) syncSetup(t *testing.T, j *fakeJellyfin) {
	t.Helper()
	ts := httptest.NewServer(j)
	t.Cleanup(ts.Close)
	f.svc.Config.JellyfinURL = ts.URL
	f.svc.Config.JellyfinKey = "test-key"
	f.svc.Config.JellyfinUsers = []string{"jfuser:OWNER"}
}

func (f *fixture) kvJSON(key string, dst any) {
	f.t.Helper()
	v, err := f.env.Q.GetKV(ctx, key)
	if err != nil {
		f.t.Fatalf("kv %s: %v", key, err)
	}
	if err := json.Unmarshal([]byte(v), dst); err != nil {
		f.t.Fatal(err)
	}
}

func TestImportHistoryAdditiveIncrementalAndTolerant(t *testing.T) {
	f := newFixture(t)
	f.friends()
	f.env.TMDB.AddMovie("603", "The Matrix", "1999-03-31")
	j := &fakeJellyfin{
		series: []map[string]any{{"Id": "s1", "Type": "Series", "ProviderIds": map[string]string{"Tmdb": "1668"}}},
		played: []map[string]any{
			// Scrambled: the import must replay oldest first.
			jfMovie("m1", "The Matrix", "603", "2024-05-03T20:00:00.0000000Z"),
			jfEpisode("e2", 1, 2, true, "2024-05-02T20:00:00.0000000Z"),
			jfMovie("bad", "Gone", "999999", "2024-05-04T20:00:00.0000000Z"), // TMDB 404
			jfEpisode("e1", 1, 1, true, "2024-05-01T20:00:00.0000000Z"),
			jfEpisode("e9", 1, 3, false, ""), // not played: ignored
		},
	}
	f.syncSetup(t, j)

	msg, err := f.svc.SyncUser(ctx, f.user, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.badAuth != 0 || j.isPlayed == 0 {
		t.Errorf("auth failures %d, IsPlayed requests %d", j.badAuth, j.isPlayed)
	}
	show := f.entry("tmdb", "1668")
	ws := f.jfWatches(show)
	if len(ws) != 2 || ws[0].Episode.Int64 != 1 || ws[0].WatchedAt != "2024-05-01T20:00:00Z" || ws[1].WatchedAt != "2024-05-02T20:00:00Z" {
		t.Fatalf("episode watches (real dates, oldest first) = %+v", ws)
	}
	mv := f.entry("tmdb", "603")
	if mv.Status != library.Completed {
		t.Errorf("movie = %s", mv.Status)
	}
	if mw := f.watches(mv); len(mw) != 1 || mw[0].WatchedAt != "2024-05-03T20:00:00Z" {
		t.Errorf("movie watches = %+v", mw)
	}

	var st Status
	f.kvJSON(fmt.Sprintf("source:%d:jellyfin", f.user.ID), &st)
	if st.Name != "jellyfin" || st.OK || st.Counts["Episode"] != 2 || st.Counts["Movie"] != 1 || len(st.Failures) != 1 || !strings.HasPrefix(st.Failures[0], "Gone (1999)") || st.Message != msg {
		t.Errorf("status = %+v", st)
	}
	if _, err := time.Parse(time.RFC3339, st.LastRun); err != nil {
		t.Errorf("last_run = %q", st.LastRun)
	}
	// The marker holds the newest play that went through; the failed item
	// (newer) stays eligible for the next run.
	marker, _ := f.env.Q.GetKV(ctx, fmt.Sprintf("jellyfin:%d:last_played", f.user.ID))
	if marker != "2024-05-03T20:00:00Z" {
		t.Errorf("marker = %q", marker)
	}

	// Incremental: only plays after the marker are processed (the failing
	// item is retried, the server still lists everything).
	j.mu.Lock()
	j.played = append(j.played, jfEpisode("e3", 1, 3, true, "2024-05-06T20:00:00.0000000Z"))
	j.mu.Unlock()
	f.env.Advance(time.Hour)
	if _, err := f.svc.SyncUser(ctx, f.user, false); err != nil {
		t.Fatal(err)
	}
	f.kvJSON(fmt.Sprintf("source:%d:jellyfin", f.user.ID), &st)
	if st.Counts["Episode"] != 1 || st.Counts["Movie"] != 0 || len(st.Failures) != 1 {
		t.Errorf("incremental status = %+v", st)
	}
	show = f.entry("tmdb", "1668")
	if got := len(f.jfWatches(show)); got != 3 {
		t.Errorf("watches after incremental = %d", got)
	}
	if got := f.seasonStatus(show, 1); got != library.Completed {
		t.Errorf("S1 finale played: %s", got)
	}

	// A full replay is additive: nothing duplicated, nothing lowered.
	before := len(f.watches(show))
	if _, err := f.svc.SyncUser(ctx, f.user, true); err != nil {
		t.Fatal(err)
	}
	show = f.entry("tmdb", "1668")
	if len(f.watches(show)) != before || f.seasonStatus(show, 1) != library.Completed || len(f.watches(f.entry("tmdb", "603"))) != 1 {
		t.Errorf("full replay changed state: %d -> %d watches", before, len(f.watches(show)))
	}
}

func TestSyncUserStatusWhenUnmappedOrUnreachable(t *testing.T) {
	f := newFixture(t)
	// Not configured at all.
	if _, err := f.svc.SyncUser(ctx, f.user, false); err != nil {
		t.Fatal(err)
	}
	var st Status
	f.kvJSON(fmt.Sprintf("source:%d:jellyfin", f.user.ID), &st)
	if st.OK || st.Message == "" || len(st.Failures) != 0 {
		t.Errorf("unconfigured status = %+v", st)
	}
	// Configured, but the Jellyfin user does not exist.
	j := &fakeJellyfin{}
	f.syncSetup(t, j)
	f.svc.Config.JellyfinUsers = []string{"ghost:owner"}
	if _, err := f.svc.SyncUser(ctx, f.user, false); err != nil {
		t.Fatal(err)
	}
	f.kvJSON(fmt.Sprintf("source:%d:jellyfin", f.user.ID), &st)
	if st.OK || len(st.Failures) != 1 || !strings.Contains(st.Failures[0], "ghost") {
		t.Errorf("unknown jellyfin user status = %+v", st)
	}
	// Wrong API key: reported, not raised.
	f.svc.Config.JellyfinUsers = []string{"jfuser:owner"}
	f.svc.Config.JellyfinKey = "wrong"
	if _, err := f.svc.SyncUser(ctx, f.user, false); err != nil {
		t.Fatal(err)
	}
	f.kvJSON(fmt.Sprintf("source:%d:jellyfin", f.user.ID), &st)
	if st.OK || len(st.Failures) != 1 {
		t.Errorf("bad key status = %+v", st)
	}
}

func TestSyncAllMapsUsersFromConfigAndSettings(t *testing.T) {
	f := newFixture(t)
	f.friends()
	j := &fakeJellyfin{
		series: []map[string]any{{"Id": "s1", "ProviderIds": map[string]string{"Tmdb": "1668"}}},
		played: []map[string]any{jfEpisode("e1", 1, 1, true, "2024-05-01T20:00:00Z")},
	}
	f.syncSetup(t, j)
	out, err := f.svc.SyncAll(ctx, false)
	if err != nil || !strings.Contains(out, "owner: 1 episodes") {
		t.Fatalf("SyncAll = %q, %v", out, err)
	}
	// Unconfigured is a quiet no-op.
	f.svc.Config.JellyfinURL = ""
	if out, _ := f.svc.SyncAll(ctx, false); out != "Jellyfin sync not configured" {
		t.Errorf("unconfigured = %q", out)
	}
}

func TestClientPagesThroughItems(t *testing.T) {
	j := &fakeJellyfin{}
	for i := 0; i < 7; i++ {
		j.played = append(j.played, jfMovie(fmt.Sprint("m", i), "M", "1", "2024-01-01T00:00:00Z"))
	}
	ts := httptest.NewServer(j)
	defer ts.Close()
	c, err := NewClient(ts.URL+"/", "test-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.PageSize = 3
	items, err := c.Items(ctx, "u1", "Movie,Episode", nil)
	if err != nil || len(items) != 7 {
		t.Fatalf("items = %d, %v", len(items), err)
	}
	if id, err := c.UserID(ctx, "jfuser"); err != nil || id != "u1" {
		t.Errorf("case-insensitive user lookup = %q, %v", id, err)
	}
	if _, err := NewClient("", "k", nil); err == nil {
		t.Error("missing URL must be an error")
	}
}

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// jellyfinFx is the overview fixture with a scriptable TMDB find, so the
// webhook can resolve episodes by IMDb id.
type jellyfinFx struct {
	*overviewFx
	find  *catalogtest.FindTMDB
	token string
}

func newJellyfinFx(t *testing.T) *jellyfinFx {
	t.Helper()
	f := newOverviewFx(t)
	find := catalogtest.NewFindTMDB(f.env.TMDB)
	f.env.UseTMDB(find)
	f.env.UseTVDB(f.env.TVDB)
	f.s.Catalog = f.env.Catalog
	f.s.Library = library.New(f.env.Conn, f.env.Q, f.env.Catalog)
	f.s.Library.Now = f.env.Now
	u, err := f.env.Q.GetUser(context.Background(), f.uid)
	if err != nil {
		t.Fatal(err)
	}
	return &jellyfinFx{overviewFx: f, find: find, token: u.WebhookToken}
}

func (f *jellyfinFx) post(path string, body any) int {
	f.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	case nil:
	default:
		raw, _ = json.Marshal(b)
	}
	resp, err := http.Post(f.ts.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (f *jellyfinFx) entryStatus(source, id string) (string, bool) {
	var status string
	err := f.env.Conn.QueryRow(`SELECT e.status FROM entries e JOIN items i ON i.id = e.item_id WHERE i.source = ? AND i.external_id = ?`, source, id).Scan(&status)
	return status, err == nil
}

func TestJellyfinWebhookUnknownTokenIs404(t *testing.T) {
	f := newJellyfinFx(t)
	if code := f.post("/webhook/jellyfin/nope", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("unknown token: %d", code)
	}
	if code := f.post("/webhook/jellyfin/"+f.token, ""); code != http.StatusBadRequest {
		t.Errorf("empty body: %d", code)
	}
}

func TestJellyfinWebhookPluginTemplateMovie(t *testing.T) {
	f := newJellyfinFx(t)
	f.env.TMDB.AddMovie("603", "The Matrix", "1999-03-31")
	path := "/webhook/jellyfin/" + f.token
	plain := func(notification, played, pos string) map[string]any {
		return map[string]any{
			"NotificationType": notification,
			"Item": map[string]any{
				"Type": "Movie", "Name": "The Matrix", "ProductionYear": "1999",
				"ProviderIds":           map[string]any{"Tmdb": "603", "Imdb": "", "Tvdb": ""},
				"UserData":              map[string]any{"Played": played},
				"PlaybackPositionTicks": pos, "RunTimeTicks": "1000",
			},
		}
	}
	if code := f.post(path, plain("PlaybackStart", "False", "0")); code != 200 {
		t.Fatalf("start: %d", code)
	}
	if s, _ := f.entryStatus("tmdb", "603"); s != library.InProgress {
		t.Errorf("after start = %q", s)
	}
	// 50% of the runtime: not watched yet.
	f.post(path, plain("PlaybackStop", "False", "500"))
	if s, _ := f.entryStatus("tmdb", "603"); s != library.InProgress {
		t.Errorf("stop at 50%% = %q", s)
	}
	// 80% is past the user's 75%.
	f.post(path, plain("PlaybackStop", "False", "800"))
	if s, _ := f.entryStatus("tmdb", "603"); s != library.Completed {
		t.Errorf("stop at 80%% = %q", s)
	}
	// Other notifications and an unmatched movie: still 200, nothing created.
	if code := f.post(path, plain("ItemAdded", "False", "0")); code != 200 {
		t.Errorf("ignored notification: %d", code)
	}
	unmatched := plain("PlaybackStop", "True", "0")
	unmatched["Item"].(map[string]any)["ProviderIds"] = map[string]any{"Tmdb": "", "Imdb": "tt404", "Tvdb": ""}
	if code := f.post(path, unmatched); code != 200 {
		t.Errorf("unmatched: %d", code)
	}
	var n int
	f.env.Conn.QueryRow(`SELECT count(*) FROM entries`).Scan(&n)
	if n != 1 {
		t.Errorf("entries = %d", n)
	}
	var status string
	if err := f.env.Conn.QueryRow(`SELECT value FROM kv WHERE key = ?`, "source:"+strconv.FormatInt(f.uid, 10)+":jellyfin_webhook").Scan(&status); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal([]byte(status), &m)
	if m["ok"] != false || m["last_item"] != "The Matrix (1999)" {
		t.Errorf("webhook status = %v", m)
	}
}

func TestJellyfinWebhookLegacyTemplateEpisode(t *testing.T) {
	f := newJellyfinFx(t)
	eps := catalogtest.Eps(1, 2, "2000-01-01")
	eps[0].ID, eps[1].ID = 303821, 303822
	f.env.TVDB.AddShow("7000", "Friends", catalogtest.Season{Number: 1, AirDate: "2000-01-01", Episodes: eps})
	payload := map[string]any{
		"Event": "Stop",
		"Item": map[string]any{
			"Type": "Episode", "Name": "Pilot", "SeriesName": "Friends",
			"ProviderIds":       map[string]any{"Tvdb": "303821", "Imdb": "tt0583459"},
			"ParentIndexNumber": 1, "IndexNumber": 1,
			"UserData": map[string]any{"Played": true},
		},
	}
	// The TVDB episode id places it; TMDB is not involved.
	if code := f.post("/webhook/jellyfin/"+f.token, payload); code != 200 {
		t.Fatalf("status %d", code)
	}
	if s, ok := f.entryStatus("tvdb", "7000"); !ok || s != library.InProgress {
		t.Errorf("show = %q, %v", s, ok)
	}
	var n int
	f.env.Conn.QueryRow(`SELECT count(*) FROM watches WHERE source = 'jellyfin'`).Scan(&n)
	if n != 1 {
		t.Errorf("watches = %d", n)
	}
}

func TestJellyfinSyncEndpoint(t *testing.T) {
	f := newJellyfinFx(t)
	f.s.Jobs = jobs.New(f.env.Conn, f.env.Q, nil)
	// No session.
	if code := f.post("/api/v1/sources/jellyfin/sync", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", code)
	}
	// Not configured.
	if code, _, _ := f.c.do("POST", "/api/v1/sources/jellyfin/sync", nil, nil); code != http.StatusBadRequest {
		t.Errorf("unconfigured: %d", code)
	}
	// The service is built on first use, so configure a fresh server.
	f = newJellyfinFx(t)
	f.s.Jobs = jobs.New(f.env.Conn, f.env.Q, nil)
	f.s.Config.JellyfinURL, f.s.Config.JellyfinKey = "http://jellyfin.invalid", "k"
	code, body, _ := f.c.do("POST", "/api/v1/sources/jellyfin/sync?full=true", nil, nil)
	if code != http.StatusAccepted || body["queued"] != true {
		t.Fatalf("sync: %d %v", code, body)
	}
	var kind, payload string
	var uid int64
	if err := f.env.Conn.QueryRow(`SELECT kind, user_id, payload FROM jobs ORDER BY id DESC LIMIT 1`).Scan(&kind, &uid, &payload); err != nil {
		t.Fatal(err)
	}
	if kind != "jellyfin.sync" || uid != f.uid || payload != `{"full":true}` {
		t.Errorf("job = %s %d %s", kind, uid, payload)
	}
	// Without CSRF a cookie session is refused.
	f.c.csrf = ""
	if code, _, _ := f.c.do("POST", "/api/v1/sources/jellyfin/sync", nil, nil); code != http.StatusForbidden {
		t.Errorf("no csrf: %d", code)
	}
}

package httpapi

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // KOSync's x-auth-key is an md5 by protocol
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/reading/readingtest"
)

var readingNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

type readingFixture struct {
	t     *testing.T
	env   *readingtest.Env
	ts    *httptest.Server
	key   string // write token
	roKey string // read-only token
}

func newReadingFixture(t *testing.T) *readingFixture {
	t.Helper()
	env := readingtest.NewEnv(t, readingNow)
	authSvc := auth.NewService(env.Q, 14)
	s := &Server{
		Config: config.Config{}, DB: env.Conn, Queries: env.Q, Auth: authSvc,
		Catalog: env.Catalog, Library: env.Library,
		Web: fstest.MapFS{"200.html": {Data: []byte("<html></html>")}},
	}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	prev := readingGo
	readingGo = func(f func()) { f() }
	t.Cleanup(func() { readingGo = prev })

	rw, err := authSvc.CreateToken(context.Background(), env.User, "koreader", auth.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := authSvc.CreateToken(context.Background(), env.User, "viewer", auth.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	return &readingFixture{t: t, env: env, ts: ts, key: rw.Key, roKey: ro.Key}
}

type readingResp struct {
	Status int
	Header http.Header
	Raw    []byte
}

func (r readingResp) obj(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Raw, &m); err != nil {
		t.Fatalf("not an object (status %d): %s", r.Status, r.Raw)
	}
	return m
}

func (r readingResp) list(t *testing.T) []any {
	t.Helper()
	var l []any
	if err := json.Unmarshal(r.Raw, &l); err != nil {
		t.Fatalf("not a list (status %d): %s", r.Status, r.Raw)
	}
	return l
}

func (f *readingFixture) do(method, path string, body any, headers map[string]string) readingResp {
	f.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, f.ts.URL+path, rd)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return readingResp{resp.StatusCode, resp.Header, raw}
}

func (f *readingFixture) api(method, path string, body any) readingResp {
	return f.do(method, "/api/v1"+path, body, map[string]string{"Authorization": "Bearer " + f.key})
}

func readingMD5(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

func (f *readingFixture) kosync(method, path string, body any, key string) readingResp {
	return f.do(method, path, body, map[string]string{"x-auth-user": "owner", "x-auth-key": readingMD5(key)})
}

func readingWant(t *testing.T, r readingResp, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status = %d, want %d: %s", r.Status, status, r.Raw)
	}
}

// ---------------------------------------------------------------- KOSync

func TestKOSyncProtocol(t *testing.T) {
	f := newReadingFixture(t)

	hc := f.do("GET", "/kosync/healthcheck", nil, nil)
	readingWant(t, hc, 200)
	if hc.Header.Get("Content-Type") != "application/vnd.koreader.v1+json" || hc.obj(t)["state"] != "OK" {
		t.Errorf("healthcheck: %v %s", hc.Header, hc.Raw)
	}

	create := f.do("POST", "/kosync/users/create", `{"username":"x","password":"y"}`, nil)
	readingWant(t, create, 402)
	if create.obj(t)["code"] != float64(2002) {
		t.Errorf("create: %s", create.Raw)
	}

	// Auth: both headers, md5 of the API token, case-insensitive.
	for name, hdr := range map[string]map[string]string{
		"none":       nil,
		"wrong key":  {"x-auth-user": "owner", "x-auth-key": readingMD5("nope")},
		"wrong user": {"x-auth-user": "someone", "x-auth-key": readingMD5(f.key)},
		"no user":    {"x-auth-key": readingMD5(f.key)},
		"raw token":  {"x-auth-user": "owner", "x-auth-key": f.key},
	} {
		r := f.do("GET", "/kosync/users/auth", nil, hdr)
		readingWant(t, r, 401)
		if r.obj(t)["code"] != float64(2001) || r.obj(t)["message"] != "Unauthorized" || r.Header.Get("Content-Type") != "application/vnd.koreader.v1+json" {
			t.Errorf("%s: %s", name, r.Raw)
		}
	}
	ok := f.do("GET", "/kosync/users/auth", nil, map[string]string{"x-auth-user": "owner", "x-auth-key": strings.ToUpper(readingMD5(f.key))})
	readingWant(t, ok, 200)
	if ok.obj(t)["authorized"] != "OK" {
		t.Errorf("auth: %s", ok.Raw)
	}
	// A read-only token may log in (and read) but not push.
	readingWant(t, f.kosync("GET", "/kosync/users/auth", nil, f.roKey), 200)

}

func TestKOSyncProgress(t *testing.T) {
	f := newReadingFixture(t)
	hash := "ABCDEF0123456789ABCDEF0123456789"
	put := map[string]any{"document": hash, "progress": "42", "percentage": 0.5, "device": "Kindle", "device_id": "dev-1"}

	readingWant(t, f.do("PUT", "/kosync/syncs/progress", put, nil), 401)
	ro := f.kosync("PUT", "/kosync/syncs/progress", put, f.roKey)
	readingWant(t, ro, 403)
	if ro.obj(t)["message"] != "Token is read-only" {
		t.Errorf("read-only: %s", ro.Raw)
	}
	for name, body := range map[string]any{
		"no document":    map[string]any{"percentage": 0.5},
		"not json":       "{nope",
		"bad percentage": map[string]any{"document": hash, "percentage": "lots"},
		"array":          `[1,2]`,
	} {
		r := f.kosync("PUT", "/kosync/syncs/progress", body, f.key)
		readingWant(t, r, 403)
		if r.obj(t)["code"] != float64(2003) {
			t.Errorf("%s: %s", name, r.Raw)
		}
	}

	r := f.kosync("PUT", "/kosync/syncs/progress", put, f.key)
	readingWant(t, r, 200)
	out := r.obj(t)
	if out["document"] != strings.ToLower(hash) {
		t.Errorf("document = %v", out["document"])
	}
	ts, isNum := out["timestamp"].(float64)
	if !isNum || int64(ts) != readingNow.Unix() {
		t.Errorf("timestamp = %v, want %d", out["timestamp"], readingNow.Unix())
	}

	// GET: lowercased lookup, all fields, same timestamp.
	for _, h := range []string{hash, strings.ToLower(hash)} {
		g := f.kosync("GET", "/kosync/syncs/progress/"+h, nil, f.key)
		readingWant(t, g, 200)
		got := g.obj(t)
		want := map[string]any{"document": strings.ToLower(hash), "progress": "42", "percentage": 0.5, "device": "Kindle", "device_id": "dev-1", "timestamp": ts}
		if len(got) != len(want) {
			t.Errorf("keys = %v", got)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s = %v, want %v", k, got[k], v)
			}
		}
	}
	// A read-only token can read.
	readingWant(t, f.kosync("GET", "/kosync/syncs/progress/"+hash, nil, f.roKey), 200)
	// Unknown document: empty object. Unauthenticated: 401.
	unknown := f.kosync("GET", "/kosync/syncs/progress/ffffffffffffffffffffffffffffffff", nil, f.key)
	readingWant(t, unknown, 200)
	if len(unknown.obj(t)) != 0 {
		t.Errorf("unknown = %s", unknown.Raw)
	}
	readingWant(t, f.do("GET", "/kosync/syncs/progress/"+hash, nil, nil), 401)

	// Percentage is clamped to 0..1; a later push moves the timestamp.
	f.env.Advance(time.Minute)
	f.kosync("PUT", "/kosync/syncs/progress", map[string]any{"document": hash, "progress": "99", "percentage": 7}, f.key)
	g := f.kosync("GET", "/kosync/syncs/progress/"+hash, nil, f.key).obj(t)
	if g["percentage"] != float64(1) || g["progress"] != "99" || g["device"] != "" {
		t.Errorf("after clamp: %v", g)
	}
	if g["timestamp"].(float64) != ts+60 {
		t.Errorf("timestamp = %v, want the push time (+60 s)", g["timestamp"])
	}
}

func TestKOSyncProgressUpdatesLinkedBook(t *testing.T) {
	f := newReadingFixture(t)
	f.env.HC.Add(readingtestBook("1", "Dune", "Frank Herbert", 500))
	item, err := f.env.Catalog.EnsureItem(context.Background(), "book", "hardcover", "1")
	if err != nil {
		t.Fatal(err)
	}
	doc, _, _ := f.env.Reading.GetOrCreateDocument(context.Background(), f.env.User, "h1", readingDocFields("Dune"))
	if _, err := f.env.Reading.LinkDocument(context.Background(), doc, item); err != nil {
		t.Fatal(err)
	}
	f.kosync("PUT", "/kosync/syncs/progress", map[string]any{"document": "h1", "progress": "p", "percentage": 0.5}, f.key)
	e, err := f.env.Library.CurrentEntry(context.Background(), f.env.User, item.ID)
	if err != nil || e.Status != library.InProgress || e.Progress != 250 {
		t.Errorf("entry = %+v %v", e, err)
	}
}

// ---------------------------------------------------------------- KOReader plugin

func TestKOReaderUpload(t *testing.T) {
	f := newReadingFixture(t)
	f.env.HC.Add(readingtestBook("1", "Dune", "Frank Herbert", 500))
	at := readingNow.Add(-2 * time.Hour).Unix()
	// Exactly what the plugin sends: omitted total_pages when null.
	body := `{"device":"Kindle","books":[{"md5":"ABC123","title":"Dune 01 - Dune","authors":"Herbert, Frank","total_pages":300,
	  "page_stats":[{"page":1,"start_time":` + readingItoa(at) + `,"duration":60,"total_pages":300},{"page":2,"start_time":` + readingItoa(at+60) + `,"duration":30}]}]}`

	readingWant(t, f.do("POST", "/api/v1/reading/koreader", body, nil), 401)
	ro := f.do("POST", "/api/v1/reading/koreader", body, map[string]string{"Authorization": "Bearer " + f.roKey})
	readingWant(t, ro, 403)

	r := f.api("POST", "/reading/koreader", body)
	readingWant(t, r, 200)
	books := r.obj(t)["books"].([]any)
	b := books[0].(map[string]any)
	if len(b) != 4 || b["md5"] != "abc123" || b["new_page_events"] != float64(2) || b["document_id"] == nil {
		t.Errorf("result = %v", b)
	}
	// The book was auto-linked in the background (a high-confidence match).
	if b["linked"] != false {
		t.Errorf("linked flag reflects the state at upload: %v", b)
	}
	again := f.api("POST", "/reading/koreader", body).obj(t)["books"].([]any)[0].(map[string]any)
	if again["new_page_events"] != float64(0) || again["linked"] != true {
		t.Errorf("resend = %v", again)
	}
	docs := f.api("GET", "/reading/documents", nil).list(t)
	doc := docs[0].(map[string]any)
	if doc["item"] == nil || doc["item"].(map[string]any)["media_id"] != "1" {
		t.Errorf("not auto-linked: %v", doc)
	}
	if _, has := doc["suggestions"]; has {
		t.Errorf("linked document has suggestions: %v", doc)
	}

	// Validation.
	readingWant(t, f.api("POST", "/reading/koreader", `{"device":"x"}`), 422)
	readingWant(t, f.api("POST", "/reading/koreader", `{"books":[{"title":"no md5"}]}`), 422)
	readingWant(t, f.api("POST", "/reading/koreader", `{"books":[{"md5":"a","page_stats":[{"page":1}]}]}`), 422)
	empty := f.api("POST", "/reading/koreader", `{"books":[]}`)
	readingWant(t, empty, 200)
}

// ---------------------------------------------------------------- documents, linking

func (f *readingFixture) upload(md5, title, authors string, pages ...int64) map[string]any {
	f.t.Helper()
	at := readingNow.Add(-3 * time.Hour).Unix()
	stats := []map[string]any{}
	for i, p := range pages {
		stats = append(stats, map[string]any{"page": p, "start_time": at + int64(i)*100, "duration": 60})
	}
	r := f.api("POST", "/reading/koreader", map[string]any{"device": "Kindle", "books": []any{
		map[string]any{"md5": md5, "title": title, "authors": authors, "total_pages": 300, "page_stats": stats},
	}})
	readingWant(f.t, r, 200)
	return r.obj(f.t)["books"].([]any)[0].(map[string]any)
}

func TestDocumentsListSuggestionsLinkMergeIgnore(t *testing.T) {
	f := newReadingFixture(t)
	f.env.HC.Add(readingtestBook("3", "God Emperor of Dune", "Frank Herbert", 400)).
		Add(readingtestBook("1", "Dune", "Frank Herbert", 500)).
		Add(readingtestBook("9", "Dune Study Guide", "SuperSummary", 50))
	// Dune's author differs from the document's: not auto-linked, only suggested.
	a := f.upload("aaa", "Dune 01 - Dune", "Someone Else", 1, 2, 3)
	b := f.upload("bbb", "Some Notes", "KOReader", 1)
	c := f.upload("ccc", "Dune (copy)", "Someone Else", 4)
	aID, bID, cID := readingItoa(int64(a["document_id"].(float64))), readingItoa(int64(b["document_id"].(float64))), readingItoa(int64(c["document_id"].(float64)))

	list := f.api("GET", "/reading/documents?unlinked=true", nil).list(t)
	if len(list) != 3 {
		t.Fatalf("unlinked = %d: %v", len(list), list)
	}
	byTitle := map[string]map[string]any{}
	for _, d := range list {
		m := d.(map[string]any)
		byTitle[m["title"].(string)] = m
	}
	dune := byTitle["Dune 01 - Dune"]
	sug, _ := dune["suggestions"].([]any)
	if len(sug) == 0 || sug[0].(map[string]any)["title"] != "Dune" {
		t.Errorf("suggestions = %v", dune["suggestions"])
	}
	for _, s := range sug {
		if s.(map[string]any)["title"] == "Dune Study Guide" {
			t.Errorf("study guide suggested: %v", sug)
		}
	}
	if dune["pages_read"] != float64(3) || dune["sessions"] != float64(1) || dune["ignored"] != false || dune["merged_into"] != nil {
		t.Errorf("document = %v", dune)
	}
	if nosug := f.api("GET", "/reading/documents?unlinked=1&suggestions=false", nil).list(t); nosug[0].(map[string]any)["suggestions"] != nil {
		t.Errorf("suggestions=false still has suggestions")
	}
	readingWant(t, f.api("GET", "/reading/documents?unlinked=maybe", nil), 422)

	// The suggestions endpoint returns the whole re-ranked provider list.
	full := f.api("GET", "/reading/documents/"+aID+"/suggestions", nil).list(t)
	if len(full) != 3 || full[0].(map[string]any)["title"] != "Dune" {
		t.Errorf("suggestions endpoint = %v", full)
	}
	for _, k := range []string{"image", "media_id", "media_type", "source", "title"} {
		if _, ok := full[0].(map[string]any)[k]; !ok {
			t.Errorf("suggestion lacks %q", k)
		}
	}
	readingWant(t, f.api("GET", "/reading/documents/9999/suggestions", nil), 404)
	readingWant(t, f.api("GET", "/reading/documents/abc/suggestions", nil), 422)
	f.env.HC.Err = errors.New("down")
	down := f.api("GET", "/reading/documents/"+aID+"/suggestions", nil)
	readingWant(t, down, 502)
	if down.obj(t)["detail"] != "Search provider unavailable" {
		t.Errorf("502 detail: %s", down.Raw)
	}
	// The list degrades to empty suggestions.
	degraded := f.api("GET", "/reading/documents?unlinked=true", nil).list(t)
	if len(degraded) != 3 || len(degraded[0].(map[string]any)["suggestions"].([]any)) != 0 {
		t.Errorf("degraded list = %v", degraded)
	}
	f.env.HC.Err = nil

	// Ignore: hidden from the unlinked list, still in the full list; reversible.
	ig := f.api("POST", "/reading/documents/"+bID+"/ignore", nil)
	readingWant(t, ig, 200)
	if ig.obj(t)["ignored"] != true {
		t.Errorf("ignore: %s", ig.Raw)
	}
	if n := len(f.api("GET", "/reading/documents?unlinked=true&suggestions=false", nil).list(t)); n != 2 {
		t.Errorf("unlinked after ignore = %d", n)
	}
	if n := len(f.api("GET", "/reading/documents?suggestions=false", nil).list(t)); n != 3 {
		t.Errorf("all after ignore = %d", n)
	}
	un := f.api("POST", "/reading/documents/"+bID+"/ignore", map[string]any{"ignored": false})
	if un.obj(t)["ignored"] != false {
		t.Errorf("unignore: %s", un.Raw)
	}
	f.api("POST", "/reading/documents/"+bID+"/ignore", nil)

	// Link by hand.
	lk := f.api("POST", "/reading/documents/"+aID+"/link", map[string]any{"source": "hardcover", "media_id": "1"})
	readingWant(t, lk, 200)
	item := lk.obj(t)["item"].(map[string]any)
	if item["media_id"] != "1" || item["media_type"] != "book" || item["title"] != "Dune" || item["source"] != "hardcover" {
		t.Errorf("link: %s", lk.Raw)
	}
	for _, k := range []string{"id", "media_id", "source", "media_type", "title", "image", "season_number", "episode_number"} {
		if _, ok := item[k]; !ok {
			t.Errorf("item lacks %q", k)
		}
	}
	readingWant(t, f.api("POST", "/reading/documents/"+aID+"/link", map[string]any{"source": "hardcover"}), 422)
	readingWant(t, f.api("POST", "/reading/documents/9999/link", map[string]any{"source": "hardcover", "media_id": "1"}), 404)
	readingWant(t, f.api("POST", "/reading/documents/"+aID+"/link", map[string]any{"source": "nowhere", "media_id": "1"}), 400)
	bad := f.api("POST", "/reading/documents/"+aID+"/link", map[string]any{"source": "hardcover", "media_id": "404"})
	readingWant(t, bad, 502)
	// The library entry exists and moved to In progress.
	sess := f.api("GET", "/reading/sessions", nil).list(t)
	for _, s := range sess {
		m := s.(map[string]any)
		if m["document_id"] == float64(int64(a["document_id"].(float64))) && (m["item"] == nil || m["item"].(map[string]any)["media_id"] != "1") {
			t.Errorf("session lacks the linked item: %v", m)
		}
	}

	// Merge c into a: c disappears from the list, a's totals grow.
	mg := f.api("POST", "/reading/documents/"+cID+"/merge", map[string]any{"into": int64(a["document_id"].(float64))})
	readingWant(t, mg, 200)
	if mg.obj(t)["merged_into"] != a["document_id"] {
		t.Errorf("merge: %s", mg.Raw)
	}
	docs := f.api("GET", "/reading/documents?suggestions=false", nil).list(t)
	if len(docs) != 2 {
		t.Fatalf("after merge = %d docs", len(docs))
	}
	for _, d := range docs {
		m := d.(map[string]any)
		if m["id"] == a["document_id"] && (m["pages_read"] != float64(4) || m["sessions"] != float64(2)) {
			t.Errorf("rolled-up totals = %v", m)
		}
	}
	readingWant(t, f.api("POST", "/reading/documents/"+aID+"/merge", map[string]any{"into": int64(a["document_id"].(float64))}), 400)
	readingWant(t, f.api("POST", "/reading/documents/"+aID+"/merge", map[string]any{"into": 9999}), 404)
	readingWant(t, f.api("POST", "/reading/documents/"+aID+"/merge", map[string]any{}), 422)
	readingWant(t, f.api("POST", "/reading/documents/9999/merge", map[string]any{"into": 1}), 404)

	// Write endpoints refuse read-only tokens and anonymous callers.
	for _, p := range []string{"/link", "/merge", "/ignore"} {
		readingWant(t, f.do("POST", "/api/v1/reading/documents/"+aID+p, "{}", map[string]string{"Authorization": "Bearer " + f.roKey}), 403)
		readingWant(t, f.do("POST", "/api/v1/reading/documents/"+aID+p, "{}", nil), 401)
	}
	// Reading is fine with a read-only token.
	readingWant(t, f.do("GET", "/api/v1/reading/documents", nil, map[string]string{"Authorization": "Bearer " + f.roKey}), 200)
}

func TestDocumentsAutoLinkEndpoint(t *testing.T) {
	f := newReadingFixture(t)
	// Upload with providers down: the attempt isn't recorded, so the endpoint retries.
	f.env.HC.Err, f.env.OL.Err = errors.New("down"), errors.New("down")
	f.upload("aaa", "Dune", "Frank Herbert", 1)
	f.env.HC.Err, f.env.OL.Err = nil, nil
	f.env.HC.Add(readingtestBook("1", "Dune", "Frank Herbert", 500))
	f.upload("qqq", "KOReader Quickstart Guide", "N/A", 1)
	r := f.api("POST", "/reading/documents/auto-link", nil)
	readingWant(t, r, 200)
	for _, d := range f.api("GET", "/reading/documents?suggestions=false", nil).list(t) {
		if m := d.(map[string]any); m["document_hash"] == "qqq" && m["ignored"] != true {
			t.Errorf("quickstart guide not ignored: %v", m)
		}
	}
	if got := r.obj(t); got["linked"] != float64(1) || got["tried"] != float64(1) {
		t.Errorf("auto-link = %v", got)
	}
}

// ---------------------------------------------------------------- sessions, stats

func TestSessionsAndStats(t *testing.T) {
	f := newReadingFixture(t)
	f.upload("aaa", "A Book", "An Author", 1, 2, 3)

	list := f.api("GET", "/reading/sessions", nil).list(t)
	if len(list) != 1 {
		t.Fatalf("sessions = %v", list)
	}
	s := list[0].(map[string]any)
	for _, k := range []string{"id", "source", "title", "item", "document_id", "started_at", "ended_at", "minutes", "pages_read", "chapters_read", "start_page", "end_page", "device"} {
		if _, ok := s[k]; !ok {
			t.Errorf("session lacks %q: %v", k, s)
		}
	}
	if s["source"] != "koreader" || s["pages_read"] != float64(3) || s["device"] != "Kindle" || s["item"] != nil {
		t.Errorf("session = %v", s)
	}
	if !strings.HasSuffix(s["started_at"].(string), "+00:00") {
		t.Errorf("started_at = %v, want Python isoformat", s["started_at"])
	}

	// Range filters and errors.
	readingWant(t, f.api("GET", "/reading/sessions?start=2026-11-26&end=2026-11-26", nil), 200)
	if n := len(f.api("GET", "/reading/sessions?start=2026-11-26&end=2026-11-27", nil).list(t)); n != 0 {
		t.Errorf("future range returned %d", n)
	}
	if n := len(f.api("GET", "/reading/sessions?limit=0", nil).list(t)); n != 0 {
		t.Errorf("limit=0 returned %d", n)
	}
	bad := f.api("GET", "/reading/sessions?start=2026-11-26&end=2026-11-01", nil)
	readingWant(t, bad, 400)
	if bad.obj(t)["detail"] != "start must be before end" {
		t.Errorf("detail = %s", bad.Raw)
	}
	big := f.api("GET", "/reading/stats?start=2000-01-01&end=2026-11-25", nil)
	readingWant(t, big, 400)
	if big.obj(t)["detail"] != "Range too large" {
		t.Errorf("detail = %s", big.Raw)
	}
	readingWant(t, f.api("GET", "/reading/stats?start=yesterday", nil), 422)
	readingWant(t, f.api("GET", "/reading/sessions?limit=x", nil), 422)
	readingWant(t, f.do("GET", "/api/v1/reading/stats", nil, nil), 401)

	st := f.api("GET", "/reading/stats", nil).obj(t)
	for _, k := range []string{"start", "end", "read_today", "today", "current_streak", "longest_streak", "totals", "days"} {
		if _, ok := st[k]; !ok {
			t.Errorf("stats lacks %q", k)
		}
	}
	if st["start"] != "2026-10-27" || st["end"] != "2026-11-25" || st["read_today"] != true || st["current_streak"] != float64(1) {
		t.Errorf("stats = %v", st)
	}
	totals := st["totals"].(map[string]any)
	if totals["pages"] != float64(3) || totals["sessions"] != float64(1) || totals["days_read"] != float64(1) || totals["days_in_range"] != float64(30) {
		t.Errorf("totals = %v", totals)
	}
	if days := st["days"].([]any); len(days) != 30 || days[29].(map[string]any)["pages"] != float64(3) {
		t.Errorf("days = %v", days)
	}
	if st["today"].(map[string]any)["date"] != "2026-11-25" {
		t.Errorf("today = %v", st["today"])
	}
	// A range that excludes today has no today row.
	old := f.api("GET", "/reading/stats?start=2026-11-01&end=2026-11-10", nil).obj(t)
	if old["today"] != nil || old["read_today"] != false {
		t.Errorf("old range: today=%v read_today=%v", old["today"], old["read_today"])
	}
}

func TestCreateSessionEndpoint(t *testing.T) {
	f := newReadingFixture(t)
	f.env.HC.Add(readingtestBook("1", "Dune", "Frank Herbert", 500))
	item, _ := f.env.Catalog.EnsureItem(context.Background(), "book", "hardcover", "1")

	good := map[string]any{"source": "paperback", "item_id": item.ID, "started_at": "2026-11-25T08:00:00Z", "ended_at": "2026-11-25T08:30:00+00:00", "pages_read": 12, "start_page": 10, "end_page": 22, "device": "iPhone"}
	r := f.api("POST", "/reading/sessions", good)
	readingWant(t, r, 200)
	s := r.obj(t)
	if s["minutes"] != float64(30) || s["title"] != "Dune" || s["source"] != "paperback" || s["document_id"] != nil || s["item"].(map[string]any)["media_id"] != "1" {
		t.Errorf("session = %v", s)
	}
	// Defaults: manual source, naive datetimes.
	r = f.api("POST", "/reading/sessions", map[string]any{"started_at": "2026-11-25T08:00:00", "ended_at": "2026-11-25T08:01:00", "title": "Paper book"})
	readingWant(t, r, 200)
	if r.obj(t)["source"] != "manual" || r.obj(t)["item"] != nil {
		t.Errorf("defaults: %s", r.Raw)
	}
	readingWant(t, f.api("POST", "/reading/sessions", map[string]any{"source": "bogus", "started_at": "2026-11-25T08:00:00Z", "ended_at": "2026-11-25T09:00:00Z"}), 400)
	readingWant(t, f.api("POST", "/reading/sessions", map[string]any{"started_at": "2026-11-25T09:00:00Z", "ended_at": "2026-11-25T08:00:00Z"}), 400)
	readingWant(t, f.api("POST", "/reading/sessions", map[string]any{"item_id": 9999, "started_at": "2026-11-25T08:00:00Z", "ended_at": "2026-11-25T09:00:00Z"}), 404)
	readingWant(t, f.api("POST", "/reading/sessions", map[string]any{"ended_at": "2026-11-25T09:00:00Z"}), 422)
	readingWant(t, f.api("POST", "/reading/sessions", map[string]any{"started_at": "soon", "ended_at": "later"}), 422)
	readingWant(t, f.do("POST", "/api/v1/reading/sessions", good, map[string]string{"Authorization": "Bearer " + f.roKey}), 403)
}

// ---------------------------------------------------------------- Triple P

func TestBookNotesEndpoints(t *testing.T) {
	f := newReadingFixture(t)
	ctx := context.Background()
	f.env.OL.Add(readingtestBook("OL1M", "Deep Work", "Cal Newport", 300))
	f.env.OL.Books[0].Synopsis = "Focus."
	f.env.OL.Books[0].TOC = []string{"Part 1"}
	item, _ := f.env.Catalog.EnsureItem(ctx, "book", "openlibrary", "OL1M")
	e, _ := f.env.Library.Track(ctx, f.env.User, item.ID, library.InProgress, library.SourceManual, readingNow)
	path := "/books/" + readingItoa(e.ID) + "/notes"

	r := f.api("GET", path, nil)
	readingWant(t, r, 200)
	n := r.obj(t)
	if len(n) != 7 {
		t.Errorf("keys = %v", n)
	}
	if n["preview"] != "Focus.\n\nContents:\n  Part 1" || n["predict"] != "" || n["purpose"] != nil || n["purpose_note"] != "" || n["reflect"] != "" || n["prompt_predict"] != true || n["updated_at"] == "" {
		t.Errorf("notes = %v", n)
	}

	put := f.api("PUT", path, map[string]any{"predict": "It's about focus", "purpose": "educate", "purpose_note": "for work"})
	readingWant(t, put, 200)
	n = put.obj(t)
	if n["predict"] != "It's about focus" || n["purpose"] != "educate" || n["purpose_note"] != "for work" || n["prompt_predict"] != false || n["preview"] != "Focus.\n\nContents:\n  Part 1" {
		t.Errorf("after put = %v", n)
	}
	// Persisted; unknown fields ignored; null clears purpose; subset keeps the rest.
	put = f.api("PUT", path, map[string]any{"reflect": "Good", "purpose": nil, "whatever": 1})
	n = put.obj(t)
	if put.Status != 200 || n["reflect"] != "Good" || n["purpose"] != nil || n["predict"] != "It's about focus" {
		t.Errorf("second put = %s", put.Raw)
	}
	if got := f.api("GET", path, nil).obj(t); got["reflect"] != "Good" || got["predict"] != "It's about focus" {
		t.Errorf("get after put = %v", got)
	}
	// PUT on a fresh entry creates the row seeded.
	f.env.HC.Add(readingtestBook("2", "Dune", "Frank Herbert", 500))
	f.env.HC.Books[0].Synopsis = "Spice."
	dune, _ := f.env.Catalog.EnsureItem(ctx, "book", "hardcover", "2")
	e2, _ := f.env.Library.Track(ctx, f.env.User, dune.ID, library.Planning, library.SourceManual, readingNow)
	seeded := f.api("PUT", "/books/"+readingItoa(e2.ID)+"/notes", map[string]any{"predict": "x"}).obj(t)
	if seeded["preview"] != "Spice." || seeded["prompt_predict"] != false {
		t.Errorf("seeded on put = %v", seeded)
	}

	// Validation and access.
	readingWant(t, f.api("PUT", path, map[string]any{"purpose": "inform"}), 422)
	readingWant(t, f.api("PUT", path, map[string]any{"predict": 5}), 422)
	readingWant(t, f.api("PUT", path, map[string]any{"purpose": 5}), 422)
	readingWant(t, f.api("PUT", path, map[string]any{"predict": nil}), 422)
	readingWant(t, f.api("PUT", path, `[1]`), 422)
	readingWant(t, f.api("GET", "/books/9999/notes", nil), 404)
	readingWant(t, f.api("GET", "/books/abc/notes", nil), 422)
	readingWant(t, f.do("GET", "/api/v1"+path, nil, nil), 401)
	readingWant(t, f.do("GET", "/api/v1"+path, nil, map[string]string{"Authorization": "Bearer " + f.roKey}), 200)
	readingWant(t, f.do("PUT", "/api/v1"+path, `{"predict":"x"}`, map[string]string{"Authorization": "Bearer " + f.roKey}), 403)
}

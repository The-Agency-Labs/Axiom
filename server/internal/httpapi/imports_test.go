package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/integrations/sourcestatus"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// importFx is the overview fixture plus a job manager and fake providers.
type importFx struct {
	*overviewFx
}

func newImportFx(t *testing.T) *importFx {
	t.Helper()
	f := &importFx{newOverviewFx(t)}
	f.s.Jobs = jobs.New(f.env.Conn, f.env.Q, nil)
	return f
}

func (f *importFx) jobCount(kind string) int {
	f.t.Helper()
	var n int
	if err := f.env.Conn.QueryRow(`SELECT count(*) FROM jobs WHERE kind = ?`, kind).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *importFx) sources(c *client, hdr map[string]string) map[string]map[string]any {
	f.t.Helper()
	code, _, arr := c.do("GET", "/api/v1/sources", nil, hdr)
	if code != 200 {
		f.t.Fatalf("GET /sources: %d", code)
	}
	out := map[string]map[string]any{}
	var order []string
	for _, a := range arr {
		m := a.(map[string]any)
		out[m["name"].(string)] = m
		order = append(order, m["name"].(string))
	}
	if strings.Join(order, ",") != "jellyfin,jellyfin_webhook,anilist,goodreads,steam" {
		f.t.Fatalf("source order = %v", order)
	}
	return out
}

func kindsOf(m map[string]any) string {
	var out []string
	for _, k := range m["kinds"].([]any) {
		out = append(out, k.(string))
	}
	return strings.Join(out, ",")
}

func TestSourcesDefaults(t *testing.T) {
	f := newImportFx(t)
	if code, _, _ := newClient(t, f.ts.URL).do("GET", "/api/v1/sources", nil, nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	src := f.sources(f.c, nil)
	for name, want := range map[string]bool{"jellyfin": false, "jellyfin_webhook": true, "anilist": false, "goodreads": true, "steam": false} {
		if src[name]["configured"] != want {
			t.Errorf("%s configured = %v, want %v", name, src[name]["configured"], want)
		}
		if v, ok := src[name]["status"]; !ok || v != nil {
			t.Errorf("%s status = %v, want null", name, v)
		}
	}
	if kindsOf(src["anilist"]) != "anime,anime_movie,manga" || kindsOf(src["goodreads"]) != "book" || kindsOf(src["steam"]) != "game" || kindsOf(src["jellyfin"]) != "tv,anime,movie,anime_movie" {
		t.Errorf("kinds = %v", src)
	}
	// A read-only token may read.
	if code, _, _ := newClient(t, f.ts.URL).do("GET", "/api/v1/sources", nil, f.token("read")); code != 200 {
		t.Fatalf("read token: %d", code)
	}
}

func TestSourcesConfiguredAndStatus(t *testing.T) {
	f := newImportFx(t)
	f.s.Config = config.Config{
		JellyfinURL: "http://jf.invalid", JellyfinKey: "k", JellyfinUsers: []string{"jfuser:alice"},
		AniListUsers: []string{"someone:alice"}, SteamKey: "secret",
	}
	src := f.sources(f.c, nil)
	for _, name := range []string{"jellyfin", "anilist", "steam", "goodreads", "jellyfin_webhook"} {
		if src[name]["configured"] != true {
			t.Errorf("%s should be configured", name)
		}
	}
	// A user that isn't mapped sees jellyfin and anilist as not configured.
	f.s.Config.JellyfinUsers = []string{"jfuser:bob"}
	f.s.Config.AniListUsers = []string{"someone:bob"}
	src = f.sources(f.c, nil)
	if src["jellyfin"]["configured"] != false || src["anilist"]["configured"] != false {
		t.Errorf("unmapped user: %v / %v", src["jellyfin"], src["anilist"])
	}
	// Without the Jellyfin URL and key even a mapped user isn't configured.
	f.s.Config.JellyfinUsers = []string{"jfuser:alice"}
	f.s.Config.JellyfinURL = ""
	if f.sources(f.c, nil)["jellyfin"]["configured"] != false {
		t.Error("jellyfin needs its URL")
	}

	for _, name := range []string{"anilist", "jellyfin"} {
		st := sourcestatus.New(name, overviewT0, map[string]int{"episodes": 3}, []string{"a: b"}, "1 problem", false)
		if err := sourcestatus.Write(context.Background(), f.env.Q, f.uid, st); err != nil {
			t.Fatal(err)
		}
	}
	src = f.sources(f.c, nil)
	st, ok := src["anilist"]["status"].(map[string]any)
	if !ok || st["name"] != "anilist" || st["ok"] != false || st["message"] != "1 problem" || st["last_run"] != overviewT0.Format("2006-01-02T15:04:05Z") ||
		num(st["counts"].(map[string]any)["episodes"]) != 3 || len(st["failures"].([]any)) != 1 {
		t.Fatalf("anilist status = %v", src["anilist"]["status"])
	}
	if src["jellyfin"]["status"] == nil || src["steam"]["status"] != nil {
		t.Errorf("statuses = %v / %v", src["jellyfin"]["status"], src["steam"]["status"])
	}
	// The failures list is capped at 50.
	many := make([]string, 80)
	for i := range many {
		many[i] = "x"
	}
	if got := sourcestatus.New("steam", overviewT0, nil, many, "", false); len(got.Failures) != 50 {
		t.Errorf("failures = %d", len(got.Failures))
	}
}

// aniListFake serves a one-entry list through the catalog's AniList slot.
type aniListFake struct{ catalog.AniList }

func (aniListFake) GraphQL(context.Context, string, map[string]any) (json.RawMessage, error) {
	entry := `{"status":"CURRENT","score":0,"progress":4,"repeat":0,"notes":"","updatedAt":0,
	  "startedAt":{"year":null,"month":null,"day":null},"completedAt":{"year":null,"month":null,"day":null},
	  "media":{"id":501,"idMal":null,"format":"MANGA","episodes":null,"chapters":30,"title":{"userPreferred":"A Manga"},
	  "coverImage":{"large":"https://img/m"},"startDate":{"year":2020,"month":1,"day":1},"endDate":{"year":null,"month":null,"day":null}}}`
	coll := `{"lists":[{"isCustomList":false,"entries":[` + entry + `]}]}`
	return json.RawMessage(`{"anime":{"lists":[]},"manga":` + coll + `}`), nil
}

func TestAniListSyncEndpoint(t *testing.T) {
	f := newImportFx(t)
	if code, _, _ := f.c.do("POST", "/api/v1/sources/anilist/sync", nil, nil); code != 400 {
		t.Fatalf("not mapped: %d", code)
	}
	f.s.Config.AniListUsers = []string{"someone:alice"}
	f.env.Catalog.Reg.P.AniList = aniListFake{}

	// Read-only tokens may not start syncs.
	if code, _, _ := newClient(t, f.ts.URL).do("POST", "/api/v1/sources/anilist/sync", nil, f.token("read")); code != 403 {
		t.Fatalf("read token: %d", code)
	}
	if code, _, _ := newClient(t, f.ts.URL).do("POST", "/api/v1/sources/anilist/sync", nil, nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}

	// Dry run: runs now, returns the counts, writes nothing.
	code, res, _ := f.c.do("POST", "/api/v1/sources/anilist/sync?dry_run=1", nil, nil)
	if code != 200 || res["dry_run"] != true || num(res["counts"].(map[string]any)["manga"]) != 1 || len(res["changes"].([]any)) != 1 {
		t.Fatalf("dry run: %d %v", code, res)
	}
	var entries int
	_ = f.env.Conn.QueryRow(`SELECT count(*) FROM entries`).Scan(&entries)
	if entries != 0 || f.jobCount("anilist.sync") != 0 {
		t.Fatalf("dry run wrote: entries=%d jobs=%d", entries, f.jobCount("anilist.sync"))
	}
	if f.sources(f.c, nil)["anilist"]["status"] != nil {
		t.Fatal("a dry run must not write the source status")
	}

	// A real request enqueues a full sync.
	code, res, _ = f.c.do("POST", "/api/v1/sources/anilist/sync", nil, nil)
	if code != 200 || num(res["job_id"]) == 0 || f.jobCount("anilist.sync") != 1 {
		t.Fatalf("enqueue: %d %v", code, res)
	}
	job, err := f.env.Q.GetJob(context.Background(), int64(num(res["job_id"])))
	if err != nil || job.UserID.Int64 != f.uid || !strings.Contains(job.Payload, `"full":true`) {
		t.Fatalf("job = %+v %v", job, err)
	}
}

func multipartUpload(t *testing.T, f *importFx, c *client, field string, data []byte, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if field != "" {
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile(field, "goodreads_library_export.csv")
		_, _ = fw.Write(data)
		_ = mw.Close()
		return rawPost(t, f, c, "/api/v1/imports/goodreads", mw.FormDataContentType(), &buf, hdr)
	}
	return rawPost(t, f, c, "/api/v1/imports/goodreads", "text/csv", bytes.NewReader(data), hdr)
}

func rawPost(t *testing.T, f *importFx, c *client, path, contentType string, body io.Reader, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.ts.URL+path, body)
	req.Header.Set("Content-Type", contentType)
	if c.csrf != "" {
		req.Header.Set("X-CSRFToken", c.csrf)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

type hardcoverFake struct{ catalog.Hardcover }

func (hardcoverFake) Search(_ context.Context, q string, _ int) (providers.SearchPage, error) {
	if q != "9780000000001" {
		return providers.SearchPage{"results": []any{}}, nil
	}
	return providers.SearchPage{"results": []any{map[string]any{"media_id": "77", "title": "A Book", "image": "https://img/b"}}}, nil
}

func (hardcoverFake) Book(_ context.Context, id string) (providers.Metadata, error) {
	return providers.Metadata{"media_id": id, "title": "A Book", "image": "https://img/b", "max_progress": 250, "details": map[string]any{}}, nil
}

func TestGoodreadsUploadEndpoint(t *testing.T) {
	f := newImportFx(t)
	f.env.Catalog.Reg.P.Hardcover = hardcoverFake{}
	csv := []byte("Book Id,Title,Author,ISBN,ISBN13,My Rating,Number of Pages,Date Read,Date Added,Bookshelves,Exclusive Shelf,Private Notes\n" +
		"1,A Book,An Author,,=\"9780000000001\",5,250,2024/02/03,2024/01/01,,read,\"two\nlines\"\n" +
		"2,Other,Au,,9789999999999,,10,,,,to-read,\n")

	code, res := multipartUpload(t, f, f.c, "file", csv, nil)
	if code != 200 || num(res["job_id"]) == 0 || f.jobCount("goodreads.import") != 1 {
		t.Fatalf("upload: %d %v", code, res)
	}

	// Run the job's work the way a worker would.
	job, err := f.env.Q.GetJob(context.Background(), int64(num(res["job_id"])))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct{ Key string }
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil || payload.Key == "" || job.UserID.Int64 != f.uid {
		t.Fatalf("job = %+v", job)
	}
	if _, err := f.s.integrations().Goodreads.RunUpload(context.Background(), f.uid, payload.Key); err != nil {
		t.Fatal(err)
	}
	st := f.sources(f.c, nil)["goodreads"]["status"].(map[string]any)
	counts := st["counts"].(map[string]any)
	if st["name"] != "goodreads" || num(counts["books"]) != 1 || num(counts["unmatched"]) != 1 || st["ok"] != false || len(st["failures"].([]any)) != 1 {
		t.Fatalf("status = %v", st)
	}
	item, err := f.env.Q.GetItemByKey(context.Background(), db.GetItemByKeyParams{Source: "hardcover", ExternalID: "77", Kind: "book"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.s.Library.CurrentEntry(context.Background(), f.uid, item.ID)
	if err != nil || e.Status != "Completed" || e.Score.Float64 != 10 || e.Progress != 250 || e.Notes != "two\nlines" {
		t.Fatalf("entry = %+v %v", e, err)
	}
}

func TestGoodreadsUploadValidation(t *testing.T) {
	f := newImportFx(t)
	csv := []byte("Title\nx\n")
	if code, _ := multipartUpload(t, f, newClient(t, f.ts.URL), "file", csv, nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	if code, _ := multipartUpload(t, f, newClient(t, f.ts.URL), "file", csv, f.token("read")); code != 403 {
		t.Fatalf("read token: %d", code)
	}
	if code, _ := multipartUpload(t, f, f.c, "other", csv, nil); code != 422 {
		t.Fatalf("wrong field: %d", code)
	}
	if code, _ := multipartUpload(t, f, f.c, "", csv, nil); code != 400 {
		t.Fatalf("not multipart: %d", code)
	}
	if code, _ := multipartUpload(t, f, f.c, "file", []byte{0xff, 0xfe, 0x80, 0x81}, nil); code != 400 {
		t.Fatalf("binary: %d", code)
	}
	big := bytes.Repeat([]byte("a,b\n"), (10<<20)/4+10)
	if code, _ := multipartUpload(t, f, f.c, "file", big, nil); code != 413 {
		t.Fatalf("too big: %d", code)
	}
	if f.jobCount("goodreads.import") != 0 {
		t.Fatal("a rejected upload must not enqueue a job")
	}
	// A bearer token with write scope may upload (no CSRF needed).
	if code, _ := multipartUpload(t, f, newClient(t, f.ts.URL), "file", csv, f.token("write")); code != 200 {
		t.Fatalf("write token: %d", code)
	}
}

func TestSteamEndpoints(t *testing.T) {
	f := newImportFx(t)
	if code, _, _ := f.c.do("POST", "/api/v1/imports/steam", map[string]string{"steam_id": "76561198000000000"}, nil); code != 400 {
		t.Fatalf("no api key: %d", code)
	}
	f.s.Config.SteamKey = "secret"
	if code, _, _ := f.c.do("POST", "/api/v1/sources/steam/sync", nil, nil); code != 400 {
		t.Fatalf("nothing saved: %d", code)
	}
	if code, _, _ := f.c.do("POST", "/api/v1/imports/steam", map[string]string{"steam_id": "  "}, nil); code != 422 {
		t.Fatalf("empty id: %d", code)
	}
	if code, _, _ := newClient(t, f.ts.URL).do("POST", "/api/v1/imports/steam", map[string]string{"steam_id": "x"}, f.token("read")); code != 403 {
		t.Fatalf("read token: %d", code)
	}
	code, res, _ := f.c.do("POST", "/api/v1/imports/steam", map[string]string{"steam_id": " somevanity "}, nil)
	if code != 200 || num(res["job_id"]) == 0 || f.jobCount("steam.sync") != 1 {
		t.Fatalf("import: %d %v", code, res)
	}
	if v, err := f.env.Q.GetKV(context.Background(), "steam:"+readingItoa(f.uid)+":id"); err != nil || v != "somevanity" {
		t.Fatalf("saved id = %q %v", v, err)
	}
	// Now a manual sync can be enqueued.
	if code, res, _ = f.c.do("POST", "/api/v1/sources/steam/sync", nil, nil); code != 200 || num(res["job_id"]) == 0 || f.jobCount("steam.sync") != 2 {
		t.Fatalf("sync: %d %v", code, res)
	}
}

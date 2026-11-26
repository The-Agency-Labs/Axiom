package httpapi

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/the-agency-labs/axiom/server/internal/calendar"
	"github.com/the-agency-labs/axiom/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

type calFx struct {
	*overviewFx
	calToken string
}

func (f *calFx) release(item db.Item, season, number int, at string) {
	f.t.Helper()
	p := db.InsertReleaseParams{ItemID: item.ID, At: at}
	if season >= 0 {
		p.Season = sql.NullInt64{Int64: int64(season), Valid: true}
	}
	if number >= 0 {
		p.Number = sql.NullInt64{Int64: int64(number), Valid: true}
	}
	if err := f.env.Q.InsertRelease(context.Background(), p); err != nil {
		f.t.Fatal(err)
	}
}

func (f *calFx) track(item db.Item, status string) {
	f.t.Helper()
	if _, err := f.lib.Track(context.Background(), f.uid, item.ID, status, library.SourceManual, overviewT0); err != nil {
		f.t.Fatal(err)
	}
}

func newCalFx(t *testing.T) *calFx {
	f := &calFx{overviewFx: newOverviewFx(t)}
	u, err := f.env.Q.GetUser(context.Background(), f.uid)
	if err != nil {
		t.Fatal(err)
	}
	f.calToken = u.CalendarToken.String
	return f
}

// feed fetches the .ics and returns the unfolded lines.
func (f *calFx) feed(token string) (int, http.Header, []string) {
	f.t.Helper()
	resp, err := http.Get(f.ts.URL + "/calendar/" + token + ".ics")
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return resp.StatusCode, resp.Header, nil
	}
	body := string(raw)
	if !strings.HasSuffix(body, "\r\n") || strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		f.t.Fatalf("lines must end in CRLF only: %q", body)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSuffix(body, "\r\n"), "\r\n") {
		if len(l) > 75 {
			f.t.Errorf("line over 75 octets (%d): %q", len(l), l)
		}
		if strings.HasPrefix(l, " ") && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	return 200, resp.Header, lines
}

type vevent map[string]string

func parseEvents(lines []string) []vevent {
	var out []vevent
	var cur vevent
	for _, l := range lines {
		switch l {
		case "BEGIN:VEVENT":
			cur = vevent{}
		case "END:VEVENT":
			out = append(out, cur)
			cur = nil
		default:
			if cur != nil {
				k, v, _ := strings.Cut(l, ":")
				cur[k] = v
			}
		}
	}
	return out
}

func unescapeText(s string) string {
	return strings.NewReplacer(`\n`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

func TestCalendarFeed(t *testing.T) {
	f := newCalFx(t)
	longTitle := "Éé Long, Show; Title \\ with é multibyte characters to force folding of lines\nsecond line"
	show := f.show("100", "Plain Show", catalogtest.Season{Number: 1, AirDate: "2024-06-01", Episodes: catalogtest.Eps(1, 3, "2024-06-10")})
	long := f.show("101", "x", catalogtest.Season{Number: 1, AirDate: "2024-06-01", Episodes: catalogtest.Eps(1, 1, "2024-06-10")})
	if _, err := f.env.Conn.Exec(`UPDATE items SET title = ? WHERE id = ?`, longTitle, long.ID); err != nil {
		t.Fatal(err)
	}
	dropped := f.show("102", "Dropped Show", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 1, "2024-06-10")})
	movie := f.movie("300", "Plain Movie")
	manga := f.countItem("manga", "mangaupdates", "1", 10)
	for _, it := range []db.Item{show, long, movie, manga} {
		f.track(it, library.InProgress)
	}
	f.track(dropped, library.Dropped)

	f.release(show, 1, 1, "2024-06-10T11:59:59Z") // date only
	f.release(show, 1, 2, "2024-06-11T11:59:59Z")
	f.release(show, 1, 3, "2024-06-12T20:30:00Z") // timed
	f.release(show, 1, 4, "2024-05-10T11:59:59Z") // 22 days back: in
	f.release(show, 1, 5, "2024-04-01T11:59:59Z") // too old
	f.release(show, 1, 6, "2025-06-01T11:59:59Z") // too far ahead
	f.release(long, 1, 1, "2024-06-15T11:59:59Z")
	f.release(dropped, 1, 1, "2024-06-10T11:59:59Z")
	f.release(movie, -1, -1, "2024-07-01T11:59:59Z")
	f.release(manga, -1, 12, "2024-06-20T11:59:59Z")

	code, hdr, lines := f.feed(f.calToken)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed: %d %v", code, hdr)
	}
	if lines[0] != "BEGIN:VCALENDAR" || lines[1] != "VERSION:2.0" || lines[len(lines)-1] != "END:VCALENDAR" {
		t.Errorf("calendar frame: %v", lines[:3])
	}
	byUID := map[string]vevent{}
	for _, e := range parseEvents(lines) {
		byUID[e["UID"]] = e
	}
	uid := func(it db.Item, s, n string) string { return itoa(int(it.ID)) + "-" + s + "-" + n + "@axiom" }

	e := byUID[uid(show, "1", "1")]
	if e == nil {
		t.Fatalf("no event for episode 1: %v", byUID)
	}
	if got := unescapeText(e["SUMMARY"]); got != "Plain Show S01E01 – Episode 1" {
		t.Errorf("summary = %q", got)
	}
	if e["DTSTART;VALUE=DATE"] != "20240610" || e["DTEND;VALUE=DATE"] != "20240611" {
		t.Errorf("all-day = %v", e)
	}
	if e := byUID[uid(show, "1", "3")]; e["DTSTART"] != "20240612T203000Z" || e["DTEND"] != "20240612T203000Z" || e["DTSTAMP"] != "20240601T120000Z" {
		t.Errorf("timed = %v", e)
	}
	if byUID[uid(show, "1", "4")] == nil {
		t.Error("release 22 days ago missing")
	}
	for _, n := range []string{"5", "6"} {
		if byUID[uid(show, "1", n)] != nil {
			t.Errorf("episode %s is outside the window", n)
		}
	}
	if e := byUID[uid(long, "1", "1")]; e == nil || unescapeText(e["SUMMARY"]) != longTitle+" S01E01 – Episode 1" {
		t.Errorf("escaped summary = %v", e)
	}
	if byUID[uid(dropped, "1", "1")] != nil {
		t.Error("dropped show must be omitted")
	}
	if e := byUID[uid(movie, "n", "n")]; e == nil || e["SUMMARY"] != "Plain Movie" || e["DTSTART;VALUE=DATE"] != "20240701" {
		t.Errorf("movie = %v", e)
	}
	if e := byUID[uid(manga, "n", "12")]; e == nil || e["SUMMARY"] != "manga 1 Ch 12" {
		t.Errorf("manga = %v", e)
	}
	_, _, again := f.feed(f.calToken)
	if strings.Join(again, "\n") != strings.Join(lines, "\n") {
		t.Error("feed is not stable between requests")
	}
}

func TestCalendarTokenAuth(t *testing.T) {
	f := newCalFx(t)
	if code, _, _ := f.feed("nope"); code != 404 {
		t.Errorf("unknown token: %d", code)
	}
	if code, _, _ := f.feed(""); code != 404 {
		t.Errorf("empty token: %d", code)
	}
	resp, err := http.Get(f.ts.URL + "/calendar/" + f.calToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("without .ics: %d", resp.StatusCode)
	}
	if code, _, _ := f.feed(f.calToken); code != 200 {
		t.Errorf("valid token: %d", code)
	}
	if _, err := f.env.Conn.Exec(`UPDATE users SET is_active = 0 WHERE id = ?`, f.uid); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.feed(f.calToken); code != 404 {
		t.Errorf("inactive user: %d", code)
	}
}

func TestCalendarRegenerateToken(t *testing.T) {
	f := newCalFx(t)
	anon := newClient(t, f.ts.URL)
	if code, _, _ := anon.do("POST", "/api/v1/settings/calendar-token", nil, f.overviewFx.token("write")); code != 403 {
		t.Errorf("api token must be refused, got %d", code)
	}
	before := getObj(t, f.c, "/api/v1/settings", nil)
	code, body, _ := f.c.do("POST", "/api/v1/settings/calendar-token", nil, nil)
	if code != 200 {
		t.Fatalf("regenerate: %d %v", code, body)
	}
	// Rotating the calendar token leaves the Jellyfin webhook URL alone.
	if body["webhook_url_path"] != before["webhook_url_path"] {
		t.Errorf("webhook path changed: %v -> %v", before["webhook_url_path"], body["webhook_url_path"])
	}
	path, _ := body["calendar_url_path"].(string)
	if !strings.HasPrefix(path, "/calendar/") || path == "/calendar/"+f.calToken+".ics" {
		t.Fatalf("path = %q", path)
	}
	if code, _, _ := f.feed(f.calToken); code != 404 {
		t.Errorf("old token still works: %d", code)
	}
	resp, err := http.Get(f.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("new token: %d", resp.StatusCode)
	}
	s := getObj(t, f.c, "/api/v1/settings", nil)
	if s["calendar_url_path"] != path {
		t.Errorf("settings path %v != %v", s["calendar_url_path"], path)
	}
}

func TestCalendarJSON(t *testing.T) {
	f := newCalFx(t)
	f.setTZ("America/Toronto") // UTC-4 in June
	show := f.show("100", "Plain Show", catalogtest.Season{Number: 1, AirDate: "2024-06-01", Episodes: catalogtest.Eps(1, 3, "2024-06-10")})
	movie := f.movie("300", "Plain Movie")
	manga := f.countItem("manga", "mangaupdates", "1", 10)
	for _, it := range []db.Item{show, movie, manga} {
		f.track(it, library.InProgress)
	}
	f.release(show, 1, 1, "2024-06-10T11:59:59Z")
	f.release(show, 1, 2, "2024-06-11T02:30:00Z") // 22:30 local on the 10th
	f.release(show, 1, 3, "2024-06-20T11:59:59Z")
	f.release(movie, -1, -1, "2024-07-01T11:59:59Z")
	f.release(manga, -1, 3, "2024-06-12T11:59:59Z")

	get := func(q string) []any {
		t.Helper()
		code, _, raw := f.c.do("GET", "/api/v1/calendar"+q, nil, nil)
		if code != 200 {
			t.Fatalf("calendar%s: %d", q, code)
		}
		return raw
	}
	ev := get("?start=2024-06-10&end=2024-06-12")
	if len(ev) != 3 {
		t.Fatalf("window events = %v", ev)
	}
	first := ev[0].(map[string]any)
	if first["date"] != "2024-06-10" || first["title"] != "Plain Show S01E01 – Episode 1" || first["media_type"] != "tv" ||
		num(first["item_id"]) != int(show.ID) || num(first["season"]) != 1 || num(first["episode"]) != 1 || first["all_day"] != true {
		t.Errorf("first = %v", first)
	}
	second := ev[1].(map[string]any) // timed: local date, not the UTC one
	if second["date"] != "2024-06-10" || second["all_day"] != false || second["time"] != "2024-06-10T22:30:00-04:00" {
		t.Errorf("second = %v", second)
	}
	if m := ev[2].(map[string]any); m["media_type"] != "manga" || m["season"] != nil || num(m["episode"]) != 3 {
		t.Errorf("manga = %v", m)
	}
	if got := get("?start=2024-06-11&end=2024-06-19"); len(got) != 1 {
		t.Errorf("narrow window = %v", got)
	}
	if got := get("?start=2024-06-01&end=2024-07-01"); len(got) != 5 {
		t.Errorf("wide window = %d", len(got))
	}
	if err := f.env.Q.UpsertUserMediaSetting(context.Background(), db.UpsertUserMediaSettingParams{UserID: f.uid, Kind: "manga", Enabled: 0, Sort: "activity", Status: "In progress"}); err != nil {
		t.Fatal(err)
	}
	if got := get("?start=2024-06-01&end=2024-07-01"); len(got) != 4 {
		t.Errorf("manga should be omitted: %d", len(got))
	}
	for _, q := range []string{"?start=nope", "?start=2024-06-10&end=2024-06-01", "?start=2020-01-01&end=2024-01-01"} {
		if code, _, _ := f.c.do("GET", "/api/v1/calendar"+q, nil, nil); code != 422 {
			t.Errorf("%s: %d", q, code)
		}
	}
	if code, _, _ := newClient(t, f.ts.URL).do("GET", "/api/v1/calendar", nil, nil); code != 401 {
		t.Errorf("anon: %d", code)
	}
	if code, _, _ := newClient(t, f.ts.URL).do("GET", "/api/v1/calendar", nil, f.overviewFx.token("read")); code != 200 {
		t.Errorf("read token: %d", code)
	}
}

func TestICalEncoder(t *testing.T) {
	long := strings.Repeat("é", 100) // 2 octets each
	out := string(calendar.Encode("-//T//EN", "Name, with; chars", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), []calendar.Event{
		{UID: "1-n-n@axiom", Summary: "a,b;c\\d\r\ne\nf" + long, Start: time.Date(2024, 3, 4, 11, 59, 59, 0, time.UTC), AllDay: true},
	}))
	for _, l := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if len(l) > 75 {
			t.Errorf("line of %d octets", len(l))
		}
		if !utf8.ValidString(l) {
			t.Errorf("fold split a UTF-8 sequence: %q", l)
		}
	}
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(unfolded, `SUMMARY:a\,b\;c\\d\ne\nf`+long+"\r\n") {
		t.Errorf("escaping: %q", unfolded)
	}
	if !strings.Contains(unfolded, "X-WR-CALNAME:Name\\, with\\; chars\r\n") || !strings.Contains(unfolded, "DTSTART;VALUE=DATE:20240304\r\n") {
		t.Errorf("unexpected: %q", unfolded)
	}
}

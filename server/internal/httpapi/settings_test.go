package httpapi

import (
	"context"
	"strings"
	"testing"
)

func TestSettingsGetDefaults(t *testing.T) {
	f := newOverviewFx(t)
	s := getObj(t, f.c, "/api/v1/settings", nil)
	if s["username"] != "alice" || s["is_admin"] != true || s["timezone"] != "UTC" || s["language"] != "en" || num(s["watched_percent"]) != 75 || s["jellyfin_users"] != "" {
		t.Errorf("settings = %v", s)
	}
	media := s["media_types"].([]any)
	var kinds []string
	for _, m := range media {
		mm := m.(map[string]any)
		kinds = append(kinds, mm["kind"].(string))
		wantStatus := "Completed" // finished titles by year finished
		if mm["kind"] == "game" || mm["kind"] == "boardgame" {
			wantStatus = "All" // games have no status
		}
		if mm["enabled"] != true || mm["sort"] != "year" || mm["status"] != wantStatus {
			t.Errorf("default media setting = %v", mm)
		}
	}
	if strings.Join(kinds, ",") != "tv,anime,movie,anime_movie,manga,comic,book,game,boardgame" {
		t.Errorf("kinds = %v", kinds)
	}
	u, _ := f.env.Q.GetUser(context.Background(), f.uid)
	if s["webhook_url_path"] != "/webhook/jellyfin/"+u.WebhookToken || s["calendar_url_path"] != "/calendar/"+u.CalendarToken.String+".ics" || u.CalendarToken.String == u.WebhookToken {
		t.Errorf("paths = %v %v", s["webhook_url_path"], s["calendar_url_path"])
	}
}

func TestSettingsPatch(t *testing.T) {
	f := newOverviewFx(t)
	code, s, _ := f.c.do("PATCH", "/api/v1/settings", map[string]any{
		"timezone": "America/Toronto", "language": "fr", "jellyfin_users": " bob ,, carol ", "watched_percent": 120,
		"media_types": []map[string]any{{"kind": "comic", "enabled": false}, {"kind": "tv", "sort": "title", "status": "All"}},
	}, nil)
	if code != 200 || s["timezone"] != "America/Toronto" || s["language"] != "fr" || s["jellyfin_users"] != "bob, carol" || num(s["watched_percent"]) != 100 {
		t.Fatalf("patch: %d %v", code, s)
	}
	for _, m := range s["media_types"].([]any) {
		mm := m.(map[string]any)
		switch mm["kind"] {
		case "comic":
			if mm["enabled"] != false || mm["sort"] != "year" {
				t.Errorf("comic = %v", mm)
			}
		case "tv":
			if mm["enabled"] != true || mm["sort"] != "title" || mm["status"] != "All" {
				t.Errorf("tv = %v", mm)
			}
		}
	}
	// Persisted, partial updates leave the rest alone, low percent clamps up.
	_, s, _ = f.c.do("PATCH", "/api/v1/settings", map[string]any{"watched_percent": 10}, nil)
	if num(s["watched_percent"]) != 50 || s["timezone"] != "America/Toronto" {
		t.Errorf("second patch = %v", s)
	}
	if me := getObj(t, f.c, "/api/v1/me", nil); me["timezone"] != "America/Toronto" {
		t.Errorf("me timezone = %v", me["timezone"])
	}

	for name, body := range map[string]map[string]any{
		"timezone":  {"timezone": "Mars/Phobos"},
		"empty tz":  {"timezone": ""},
		"language":  {"language": "not a language"},
		"kind":      {"media_types": []map[string]any{{"kind": "podcast", "enabled": true}}},
		"sort":      {"media_types": []map[string]any{{"kind": "tv", "sort": "vibes"}}},
		"status":    {"media_types": []map[string]any{{"kind": "tv", "status": "Maybe"}}},
		"bad value": {"watched_percent": "lots"},
	} {
		if code, b, _ := f.c.do("PATCH", "/api/v1/settings", body, nil); code != 422 {
			t.Errorf("%s: %d %v", name, code, b)
		}
	}
	// A rejected request changes nothing.
	f.c.do("PATCH", "/api/v1/settings", map[string]any{"language": "de", "media_types": []map[string]any{{"kind": "tv", "sort": "vibes"}}}, nil)
	if s := getObj(t, f.c, "/api/v1/settings", nil); s["language"] != "fr" {
		t.Errorf("language after rejected patch = %v", s["language"])
	}
}

func TestSettingsPassword(t *testing.T) {
	f := newOverviewFx(t)
	for name, tc := range map[string]struct {
		body map[string]string
		code int
	}{
		"wrong current": {map[string]string{"current": "nope", "new": "long-enough-1"}, 400},
		"too short":     {map[string]string{"current": "password-1", "new": "short"}, 422},
	} {
		if code, b, _ := f.c.do("POST", "/api/v1/settings/password", tc.body, nil); code != tc.code {
			t.Errorf("%s: %d %v", name, code, b)
		}
	}
	if code, b, _ := f.c.do("POST", "/api/v1/settings/password", map[string]string{"current": "password-1", "new": "new-password-2"}, nil); code != 200 || b["ok"] != true {
		t.Fatalf("change: %d %v", code, b)
	}
	// Old password stops working, the new one logs in.
	c := newClient(t, f.ts.URL)
	_, b, _ := c.do("GET", "/api/v1/auth/csrf", nil, nil)
	c.csrf = b["csrf"].(string)
	if code, _, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "password-1"}, nil); code != 401 {
		t.Errorf("old password login: %d", code)
	}
	f.login("alice", "new-password-2")
}

func TestSettingsWebhookToken(t *testing.T) {
	f := newOverviewFx(t)
	before, _ := f.env.Q.GetUser(context.Background(), f.uid)
	code, b, _ := f.c.do("POST", "/api/v1/settings/webhook-token", nil, nil)
	after, _ := f.env.Q.GetUser(context.Background(), f.uid)
	if code != 200 || after.WebhookToken == before.WebhookToken || len(after.WebhookToken) < 32 {
		t.Fatalf("regenerate: %d %v", code, b)
	}
	// Rotating the webhook token leaves the calendar URL alone.
	if b["webhook_url_path"] != "/webhook/jellyfin/"+after.WebhookToken || after.CalendarToken != before.CalendarToken || b["calendar_url_path"] != "/calendar/"+before.CalendarToken.String+".ics" {
		t.Errorf("paths = %v", b)
	}
}

func TestSettingsUsers(t *testing.T) {
	f := newOverviewFx(t)
	code, b, _ := f.c.do("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "bobs-password", "is_admin": false}, nil)
	if code != 200 || b["username"] != "bob" || b["is_admin"] != false || b["is_active"] != true {
		t.Fatalf("create: %d %v", code, b)
	}
	bobID := int(b["id"].(float64))
	if _, ok := b["password_hash"]; ok {
		t.Errorf("response leaks the hash")
	}
	if code, _, _ := f.c.do("POST", "/api/v1/users", map[string]any{"username": "BOB", "password": "bobs-password"}, nil); code != 409 {
		t.Errorf("duplicate: %d", code)
	}
	if code, _, _ := f.c.do("POST", "/api/v1/users", map[string]any{"username": "carol", "password": "short"}, nil); code != 422 {
		t.Errorf("short password: %d", code)
	}
	if code, _, _ := f.c.do("POST", "/api/v1/users", map[string]any{"username": " ", "password": "long-enough-1"}, nil); code != 422 {
		t.Errorf("blank username: %d", code)
	}
	if _, _, arr := f.c.do("GET", "/api/v1/users", nil, nil); len(arr) != 2 {
		t.Errorf("users = %v", arr)
	}

	// Admins can't deactivate or demote themselves.
	for _, body := range []map[string]any{{"is_active": false}, {"is_admin": false}} {
		if code, b, _ := f.c.do("PATCH", "/api/v1/users/"+itoa(int(f.uid)), body, nil); code != 400 {
			t.Errorf("self %v: %d %v", body, code, b)
		}
	}
	if code, _, _ := f.c.do("PATCH", "/api/v1/users/"+itoa(int(f.uid)), map[string]any{"is_active": true, "is_admin": true}, nil); code != 200 {
		t.Errorf("self no-op: %d", code)
	}
	if code, _, _ := f.c.do("PATCH", "/api/v1/users/9999", map[string]any{"is_active": false}, nil); code != 404 {
		t.Errorf("missing user: %d", code)
	}

	// Bob (not admin) cannot manage users; deactivating him ends his sessions.
	bob := f.login("bob", "bobs-password")
	if code, _, _ := bob.do("GET", "/api/v1/users", nil, nil); code != 403 {
		t.Errorf("non-admin list: %d", code)
	}
	if code, _, _ := bob.do("POST", "/api/v1/users", map[string]any{"username": "x", "password": "long-enough-1"}, nil); code != 403 {
		t.Errorf("non-admin create: %d", code)
	}
	if code, _, _ := bob.do("GET", "/api/v1/settings", nil, nil); code != 200 {
		t.Errorf("non-admin settings: %d", code)
	}
	if s := getObj(t, bob, "/api/v1/settings", nil); s["is_admin"] != false {
		t.Errorf("bob settings = %v", s)
	}
	code, b, _ = f.c.do("PATCH", "/api/v1/users/"+itoa(bobID), map[string]any{"is_admin": true}, nil)
	if code != 200 || b["is_admin"] != true {
		t.Fatalf("promote: %d %v", code, b)
	}
	code, b, _ = f.c.do("PATCH", "/api/v1/users/"+itoa(bobID), map[string]any{"is_active": false, "is_admin": false}, nil)
	if code != 200 || b["is_active"] != false || b["is_admin"] != false {
		t.Fatalf("deactivate: %d %v", code, b)
	}
	if code, _, _ := bob.do("GET", "/api/v1/settings", nil, nil); code != 401 {
		t.Errorf("deactivated session: %d", code)
	}
	c := newClient(t, f.ts.URL)
	_, cb, _ := c.do("GET", "/api/v1/auth/csrf", nil, nil)
	c.csrf = cb["csrf"].(string)
	if code, _, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "bobs-password"}, nil); code != 401 {
		t.Errorf("deactivated login: %d", code)
	}
}

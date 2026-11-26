package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/the-agency-labs/axiom/server/internal/auth"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/db"
)

func testServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	s := &Server{Config: config.Config{}, DB: conn, Queries: q, Auth: auth.NewService(q, 14),
		Web: fstest.MapFS{"200.html": {Data: []byte("<html></html>")}}}
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	return ts, s
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any, headers map[string]string) (int, map[string]any, []any) {
	c.t.Helper()
	var rd bytes.Buffer
	if body != nil {
		json.NewEncoder(&rd).Encode(body)
	}
	req, _ := http.NewRequest(method, c.base+path, &rd)
	if c.csrf != "" {
		req.Header.Set("X-CSRFToken", c.csrf)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)
	var obj map[string]any
	var arr []any
	if json.Unmarshal(raw, &obj) != nil {
		json.Unmarshal(raw, &arr)
	}
	return resp.StatusCode, obj, arr
}

func TestAuthFlow(t *testing.T) {
	ts, s := testServer(t)
	if _, err := s.Auth.CreateUser(context.Background(), "alice", "password-1", true); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, ts.URL)

	// Anonymous.
	if code, body, _ := c.do("GET", "/api/v1/tokens", nil, nil); code != 401 || body["detail"] != "Unauthorized" {
		t.Fatalf("anon tokens: %d %v", code, body)
	}
	code, body, _ := c.do("GET", "/api/v1/auth/csrf", nil, nil)
	if code != 200 || body["csrf"] == "" || body["authenticated"] != false {
		t.Fatalf("csrf: %d %v", code, body)
	}
	// Login without the header fails CSRF.
	if code, body, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "password-1"}, nil); code != 403 {
		t.Fatalf("login without csrf: %d %v", code, body)
	}
	c.csrf = body["csrf"].(string)
	if code, body, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "bad"}, nil); code != 401 || body["detail"] != "Invalid username or password" {
		t.Fatalf("bad login: %d %v", code, body)
	}
	code, body, _ = c.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "password-1"}, nil)
	if code != 200 || body["username"] != "alice" || body["csrf"] == "" {
		t.Fatalf("login: %d %v", code, body)
	}
	c.csrf = body["csrf"].(string) // login rotates the token

	if code, body, _ := c.do("GET", "/api/v1/auth/csrf", nil, nil); body["authenticated"] != true || body["username"] != "alice" {
		t.Fatalf("csrf after login: %d %v", code, body)
	}

	// Token management (session only, CSRF on unsafe).
	if code, body, _ := c.do("POST", "/api/v1/tokens", map[string]string{"name": "kindle", "scope": "write"}, map[string]string{"X-CSRFToken": "wrong"}); code != 403 {
		t.Fatalf("create token with bad csrf: %d %v", code, body)
	}
	code, body, _ = c.do("POST", "/api/v1/tokens", map[string]string{"name": "kindle", "scope": "write"}, nil)
	if code != 200 || body["scope"] != "write" || body["revoked"] != false || body["last_used_at"] != nil || !strings.HasPrefix(body["key"].(string), "axm_") {
		t.Fatalf("create token: %d %v", code, body)
	}
	key, id := body["key"].(string), int(body["id"].(float64))
	for _, k := range []string{"id", "name", "scope", "prefix", "created_at", "last_used_at", "revoked", "key"} {
		if _, ok := body[k]; !ok {
			t.Errorf("token JSON missing %q", k)
		}
	}
	if code, _, arr := c.do("GET", "/api/v1/tokens", nil, nil); code != 200 || len(arr) != 1 {
		t.Fatalf("list tokens: %d %v", code, arr)
	}
	if code, body, _ := c.do("POST", "/api/v1/tokens", map[string]string{"name": "x", "scope": "admin"}, nil); code != 422 {
		t.Fatalf("bad scope: %d %v", code, body)
	}

	// The bearer token works without a cookie, but cannot manage tokens.
	bare := newClient(t, ts.URL)
	hdr := map[string]string{"Authorization": "Bearer " + key}
	if code, body, _ := bare.do("GET", "/api/v1/tokens", nil, hdr); code != 403 || body["detail"] != "Manage tokens from the web app" {
		t.Fatalf("token managing tokens: %d %v", code, body)
	}
	if code, _, _ := bare.do("POST", "/api/v1/auth/logout", nil, hdr); code != 200 { // bearer skips CSRF
		t.Fatalf("bearer logout: %d", code)
	}

	// Revoke, then the key stops working.
	if code, body, _ := c.do("DELETE", "/api/v1/tokens/99999", nil, nil); code != 404 || body["detail"] != "Not Found" {
		t.Fatalf("revoke missing: %d %v", code, body)
	}
	code, body, _ = c.do("DELETE", "/api/v1/tokens/"+itoa(id), nil, nil)
	if code != 200 || body["revoked"] != true {
		t.Fatalf("revoke: %d %v", code, body)
	}
	if code, _, _ := bare.do("GET", "/api/v1/tokens", nil, hdr); code != 401 {
		t.Fatalf("revoked key: %d", code)
	}

	// Logout.
	if code, body, _ := c.do("POST", "/api/v1/auth/logout", nil, nil); code != 200 || body["ok"] != true {
		t.Fatalf("logout: %d %v", code, body)
	}
	if code, _, _ := c.do("GET", "/api/v1/tokens", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

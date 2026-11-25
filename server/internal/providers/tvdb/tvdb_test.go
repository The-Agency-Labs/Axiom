package tvdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// server fakes TVDB: login hands out "tok1", "tok2", ... and the data
// endpoints accept only the newest token.
type server struct {
	mu      sync.Mutex
	logins  int
	gets    int
	bodies  []string
	status  map[string]int // path -> forced status (checked after auth)
	payload map[string][]byte
}

func (s *server) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path == "/login" {
		b, _ := io.ReadAll(r.Body)
		s.bodies = append(s.bodies, string(b))
		s.logins++
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok" + string(rune('0'+s.logins))}})
		return
	}
	s.gets++
	if r.Header.Get("Authorization") != "Bearer tok"+string(rune('0'+s.logins)) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthorized"}`))
		return
	}
	if st, ok := s.status[r.URL.Path]; ok {
		w.WriteHeader(st)
		w.Write([]byte(`{"status":"failure","data":null}`))
		return
	}
	body, ok := s.payload[r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Write(body)
}

func newClient(t *testing.T, s *server, key string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	c := New(providers.NewClient(nil), config.Config{TVDBKey: key})
	c.BaseURL = srv.URL
	return c
}

func TestEpisode(t *testing.T) {
	s := &server{payload: map[string][]byte{"/episodes/3254641": fixture(t, "episode.json")}}
	c := newClient(t, s, "key")
	ep, err := c.Episode(context.Background(), "3254641")
	if err != nil {
		t.Fatal(err)
	}
	if providers.Int(ep, "episode_id") != 3254641 || providers.Int(ep, "series_id") != 121361 ||
		providers.Int(ep, "season_number") != 1 || providers.Int(ep, "episode_number") != 1 || len(ep) != 4 {
		t.Errorf("ep = %v", ep)
	}
	if s.bodies[0] != `{"apikey":"key"}` {
		t.Errorf("login body = %s", s.bodies[0])
	}
	// The token is reused.
	if _, err := c.Episode(context.Background(), "3254641"); err != nil || s.logins != 1 {
		t.Errorf("logins = %d, err = %v", s.logins, err)
	}
}

func TestEpisodeNotFoundIsAMiss(t *testing.T) {
	s := &server{payload: map[string][]byte{}}
	c := newClient(t, s, "key")
	ep, err := c.Episode(context.Background(), "1")
	if ep != nil || err != nil {
		t.Errorf("got (%v, %v), want (nil, nil)", ep, err)
	}
}

func TestEpisodeIncompleteIsAMiss(t *testing.T) {
	s := &server{payload: map[string][]byte{"/episodes/5": []byte(`{"data":{"id":5,"seriesId":1,"number":2}}`)}}
	c := newClient(t, s, "key")
	if ep, err := c.Episode(context.Background(), "5"); ep != nil || err != nil {
		t.Errorf("got (%v, %v)", ep, err)
	}
}

func TestEpisodeServerError(t *testing.T) {
	s := &server{status: map[string]int{"/episodes/9": http.StatusForbidden}}
	c := newClient(t, s, "key")
	_, err := c.Episode(context.Background(), "9")
	var pe *providers.Error
	if err == nil || !asError(err, &pe) || pe.Status != http.StatusForbidden || pe.Provider != Label {
		t.Errorf("err = %v", err)
	}
}

func asError(err error, target **providers.Error) bool {
	pe, ok := err.(*providers.Error)
	if ok {
		*target = pe
	}
	return ok
}

func TestExpiredTokenRefreshesOnce(t *testing.T) {
	s := &server{payload: map[string][]byte{"/episodes/3254641": fixture(t, "episode.json")}}
	c := newClient(t, s, "key")
	if _, err := c.AccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Server rotates its token: the cached one now gets a 401.
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	ep, err := c.Episode(context.Background(), "3254641")
	if err != nil || ep == nil {
		t.Fatalf("got (%v, %v)", ep, err)
	}
	if s.logins != 3 {
		t.Errorf("logins = %d, want a re-login", s.logins)
	}
}

func TestNoKeyMeansNoLookup(t *testing.T) {
	s := &server{}
	c := newClient(t, s, "")
	if tok, err := c.AccessToken(context.Background()); tok != "" || err != nil {
		t.Errorf("token = %q, %v", tok, err)
	}
	if ep, err := c.Episode(context.Background(), "1"); ep != nil || err != nil {
		t.Errorf("episode = %v, %v", ep, err)
	}
	if id, err := c.SeriesTMDBID(context.Background(), "1"); id != "" || err != nil {
		t.Errorf("id = %q, %v", id, err)
	}
	if s.logins != 0 || s.gets != 0 {
		t.Errorf("requests made: logins=%d gets=%d", s.logins, s.gets)
	}
}

func TestSeriesTMDBID(t *testing.T) {
	s := &server{payload: map[string][]byte{
		"/series/121361/extended": fixture(t, "series_extended.json"),
		"/series/1/extended":      []byte(`{"data":{"remoteIds":null}}`),
		"/series/2/extended":      []byte(`{"data":{"remoteIds":[{"id":"77","type":99,"sourceName":"TheMovieDB.com"}]}}`),
	}}
	c := newClient(t, s, "key")
	ctx := context.Background()
	if id, err := c.SeriesTMDBID(ctx, "121361"); err != nil || id != "1399" {
		t.Errorf("id = %q, %v", id, err)
	}
	if id, err := c.SeriesTMDBID(ctx, "1"); err != nil || id != "" {
		t.Errorf("no remote ids: %q, %v", id, err)
	}
	if id, err := c.SeriesTMDBID(ctx, "2"); err != nil || id != "77" {
		t.Errorf("matched by source name: %q, %v", id, err)
	}
	if _, err := c.SeriesTMDBID(ctx, "404"); err == nil {
		t.Error("a series 404 is an error, as in the Django app")
	}
}

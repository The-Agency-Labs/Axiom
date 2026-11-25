package mappings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

func newStore(t *testing.T, url string) *Store {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn, db.New(conn), providers.NewClient(nil))
	s.url = url
	return s
}

func sample(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/fribb_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadAndLookups(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "")
	n, err := s.Load(ctx, strings.NewReader(sample(t)))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("loaded %d rows, want 6", n)
	}

	m, err := s.ByMAL(ctx, 59978)
	if err != nil || m == nil {
		t.Fatalf("by mal: %v %v", m, err)
	}
	if m.AnilistID.Int64 != 182255 || m.TmdbTv.Int64 != 209867 || m.TmdbSeason.Int64 != 2 ||
		m.TmdbOffset != 28 || m.TvdbID.Int64 != 424536 || m.Kind != "TV" || m.TmdbMovie.Valid {
		t.Fatalf("bad row %+v", m)
	}
	// The TVDB season and offset of the cour: what AniList entries are placed by.
	if !m.TvdbSeason.Valid || m.TvdbSeason.Int64 != 2 || m.TvdbOffset != 12 {
		t.Fatalf("tvdb placement %+v", m)
	}
	first, _ := s.ByMAL(ctx, 52991)
	if first == nil || !first.TvdbSeason.Valid || first.TvdbSeason.Int64 != 1 || first.TvdbOffset != 0 {
		t.Fatalf("first cour %+v", first)
	}
	// A row with a TVDB id but no TVDB season (the dataset omits it): season unknown, not 0.
	if m, _ := s.ByMAL(ctx, 300); m == nil || m.TvdbSeason.Valid {
		t.Fatalf("missing tvdb season read as %+v", m)
	}
	// ...and the other cour of the series claims season 2, found by TVDB id.
	if others, _ := s.OtherTVDB(ctx, 424536, first.ID); len(others) != 1 || others[0].TvdbSeason.Int64 != 2 {
		t.Fatalf("other cours %+v", others)
	}
	// list-valued mal_id takes the first element; movie rows have no season
	m, _ = s.ByAniList(ctx, 431)
	if m == nil || m.MalID.Int64 != 431 || m.TmdbMovie.Int64 != 4935 || m.TmdbTv.Valid || m.TmdbSeason.Valid {
		t.Fatalf("movie row %+v", m)
	}
	// string ids parse; a themoviedb_id that is not an object is ignored
	m, _ = s.ByMAL(ctx, 300)
	if m == nil || m.TvdbID.Int64 != 70973 || m.TmdbTv.Valid || m.TmdbSeason.Int64 != 2 {
		t.Fatalf("string row %+v", m)
	}
	// null / odd shapes
	m, _ = s.ByMAL(ctx, 77)
	if m == nil || m.TmdbTv.Valid || m.TmdbMovie.Int64 != 4935 || m.TmdbOffset != 0 {
		t.Fatalf("odd row %+v", m)
	}
	if m, _ := s.ByAniList(ctx, 999999); m != nil {
		t.Fatalf("unexpected %+v", m)
	}

	shows, _ := s.ByTMDBTV(ctx, 209867)
	if len(shows) != 2 || shows[0].MalID.Int64 != 52991 || shows[1].MalID.Int64 != 59978 {
		t.Fatalf("by tmdb tv %+v", shows)
	}
	others, _ := s.OtherTMDBTV(ctx, 209867, shows[0].ID)
	if len(others) != 1 || others[0].MalID.Int64 != 59978 {
		t.Fatalf("others %+v", others)
	}
	films, _ := s.ByTMDBMovie(ctx, 4935)
	if len(films) != 2 {
		t.Fatalf("by tmdb movie %+v", films)
	}
	tvdb, _ := s.ByTVDB(ctx, 424536)
	if len(tvdb) != 2 {
		t.Fatalf("by tvdb %+v", tvdb)
	}
	if m, _ := s.ForAniListMedia(ctx, 0, 52991); m == nil || m.AnilistID.Int64 != 154587 {
		t.Fatalf("fallback to mal: %+v", m)
	}
	if m, _ := s.Lookup(ctx, "anilist", "154587"); m == nil || m.MalID.Int64 != 52991 {
		t.Fatalf("lookup anilist: %+v", m)
	}
	if m, _ := s.Lookup(ctx, "mal", "abc"); m != nil {
		t.Fatal("non-numeric id matched")
	}

	ids, err := s.AnimeTMDBIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids.Shows) != 2 || len(ids.Films) != 1 {
		t.Fatalf("ids %+v", ids)
	}
	if _, ok := ids.Shows["26209"]; !ok {
		t.Fatal("missing show 26209")
	}
	if _, ok := ids.Films["4935"]; !ok {
		t.Fatal("missing film 4935")
	}
	// Anime shows are told apart by TVDB id too.
	for _, id := range []string{"72025", "424536", "70973"} {
		if _, ok := ids.TVDBShows[id]; !ok {
			t.Fatalf("missing anime tvdb series %s in %v", id, ids.TVDBShows)
		}
	}
}

func TestLoadReplacesAndKeepsOldOnError(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "")
	if _, err := s.Load(ctx, strings.NewReader(sample(t))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, strings.NewReader(`[{"mal_id": 1, "type": "TV"}, {"mal_id": `)); err == nil {
		t.Fatal("expected a truncated dataset to fail")
	}
	if m, _ := s.ByMAL(ctx, 290); m == nil {
		t.Fatal("old data lost after failed load")
	}
	if m, _ := s.ByMAL(ctx, 1); m != nil {
		t.Fatal("partial data committed")
	}
	if n, err := s.Load(ctx, strings.NewReader(`[{"mal_id": 1, "type": "TV"}]`)); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if m, _ := s.ByMAL(ctx, 290); m != nil {
		t.Fatal("old rows survived a refresh")
	}
}

func TestLoadBatches(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, "")
	var b strings.Builder
	b.WriteString("[")
	const total = batchSize*2 + 17
	for i := 1; i <= total; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		b.WriteString(`{"type":"TV","mal_id":` + strconv.Itoa(i) + `,"themoviedb_id":{"tv":` + strconv.Itoa(i+100000) + `}}`)
	}
	b.WriteString("]")
	n, err := s.Load(ctx, strings.NewReader(b.String()))
	if err != nil || n != total {
		t.Fatalf("n=%d err=%v", n, err)
	}
	ids, _ := s.AnimeTMDBIDs(ctx)
	if len(ids.Shows) != total {
		t.Fatalf("shows %d", len(ids.Shows))
	}
}

func TestEnsureLoadedRefreshAndStaleness(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(sample(t)))
	}))
	defer srv.Close()
	s := newStore(t, srv.URL)

	if stale, _ := s.IsStale(ctx); !stale {
		t.Fatal("empty table must be stale")
	}
	if err := s.EnsureLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureLoaded(ctx); err != nil || calls.Load() != 1 {
		t.Fatalf("second EnsureLoaded downloaded again (calls=%d, err=%v)", calls.Load(), err)
	}
	stamp, err := s.refreshedAt(ctx)
	if err != nil || stamp == "" {
		t.Fatalf("kv stamp %q %v", stamp, err)
	}

	if n, err := s.RefreshIfStale(ctx); err != nil || n != 0 {
		t.Fatalf("fresh data refreshed: n=%d err=%v", n, err)
	}
	// a week and a day later it is stale
	s.now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if stale, _ := s.IsStale(ctx); !stale {
		t.Fatal("old data must be stale")
	}
	if n, err := s.RefreshIfStale(ctx); err != nil || n != 6 || calls.Load() != 2 {
		t.Fatalf("n=%d calls=%d err=%v", n, calls.Load(), err)
	}
}

func TestEnsureLoadedDownloadFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	s := newStore(t, srv.URL)
	if err := s.EnsureLoaded(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if m, err := s.Lookup(context.Background(), "mal", "290"); m != nil || err != nil {
		t.Fatalf("lookup with no data: %v %v", m, err)
	}
}

func TestRegisterJobs(t *testing.T) {
	s := newStore(t, "")
	m := jobs.New(s.conn, s.q, nil)
	s.RegisterJobs(m) // panics if registration is rejected
}

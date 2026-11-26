package migrate

import (
	"context"
	"crypto/md5" //nolint:gosec // KOSync sends md5(key)
	"crypto/pbkdf2"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

//go:embed testdata/django_schema.sql
var djangoSchema string

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// fixture is a tiny Django database (the real DDL, synthetic rows) and an
// empty Go database.
type fixture struct {
	t      *testing.T
	src    *sql.DB
	dst    *sql.DB
	nextID int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	src, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "django.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	src.SetMaxOpenConns(1)
	if _, err := src.Exec(djangoSchema); err != nil {
		t.Fatal(err)
	}
	dst, err := db.Open(context.Background(), filepath.Join(dir, "go.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dst.Close() })
	return &fixture{t: t, src: src, dst: dst}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.src.Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

// userDefaults gives every NOT NULL column of users_user a value its CHECK
// constraints accept.
func (f *fixture) user(id int, name string, over map[string]any) {
	f.t.Helper()
	rows, err := f.src.Query(`SELECT name, type FROM pragma_table_info('users_user') WHERE "notnull" = 1`)
	if err != nil {
		f.t.Fatal(err)
	}
	vals := map[string]any{"id": id, "username": name, "password": "!", "date_joined": "2024-01-01 00:00:00", "token": "tok-" + name}
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			f.t.Fatal(err)
		}
		if _, ok := vals[col]; ok {
			continue
		}
		switch {
		case col == "calendar_layout":
			vals[col] = "grid"
		case col == "lists_sort":
			vals[col] = "name"
		case col == "home_sort":
			vals[col] = "upcoming"
		case col == "list_detail_sort":
			vals[col] = "date_added"
		case col == "quick_watch_date":
			vals[col] = "current_date"
		case col == "last_search_type":
			vals[col] = "tv"
		case col == "week_start_day":
			vals[col] = "monday"
		case strings.HasSuffix(col, "_layout"):
			vals[col] = "grid"
		case strings.HasSuffix(col, "_sort"):
			vals[col] = "score"
		case strings.HasSuffix(col, "_status"):
			vals[col] = "All"
		case strings.HasSuffix(col, "_enabled") && col != "daily_digest_enabled" && col != "release_notifications_enabled":
			vals[col] = 1
		case col == "is_active":
			vals[col] = 1
		case strings.Contains(typ, "bool"), strings.Contains(typ, "integer"):
			vals[col] = 0
		default:
			vals[col] = ""
		}
	}
	rows.Close()
	for k, v := range over {
		vals[k] = v
	}
	var cols, ph []string
	var args []any
	for k, v := range vals {
		cols = append(cols, k)
		ph = append(ph, "?")
		args = append(args, v)
	}
	f.exec(`INSERT INTO users_user (`+strings.Join(cols, ",")+`) VALUES (`+strings.Join(ph, ",")+`)`, args...)
}

func (f *fixture) item(mediaType, source, mediaID, title string, season, episode any) int {
	f.t.Helper()
	f.nextID++
	f.exec(`INSERT INTO app_item (id, media_id, media_type, title, image, season_number, episode_number, source) VALUES (?, ?, ?, ?, 'http://img/x.jpg', ?, ?, ?)`,
		f.nextID, mediaID, mediaType, title, season, episode, source)
	return f.nextID
}

func (f *fixture) run(opts Options) (*Report, error) {
	f.t.Helper()
	opts.Source, opts.Target = f.src, f.dst
	if opts.Now == nil {
		opts.Now = func() time.Time { return now }
	}
	return Run(context.Background(), opts)
}

func (f *fixture) must(opts Options) *Report {
	f.t.Helper()
	rep, err := f.run(opts)
	if err != nil {
		f.t.Fatal(err)
	}
	return rep
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.dst.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (f *fixture) str(query string, args ...any) string {
	f.t.Helper()
	var s sql.NullString
	if err := f.dst.QueryRow(query, args...).Scan(&s); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
	return s.String
}

func hexSHA(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func hexMD5(v string) string { s := md5.Sum([]byte(v)); return hex.EncodeToString(s[:]) } //nolint:gosec

func TestUsersSettingsAndTokens(t *testing.T) {
	f := newFixture(t)
	hash, err := pbkdf2.Key(sha256.New, "correct horse", []byte("somesalt"), 1000, 32)
	if err != nil {
		t.Fatal(err)
	}
	djangoHash := "pbkdf2_sha256$1000$somesalt$" + base64.StdEncoding.EncodeToString(hash)
	f.user(1, "alice", map[string]any{
		"password": djangoHash, "is_superuser": 1, "is_staff": 0, "token": "web-tok-1", "plex_usernames": "ali, alice-tv ,",
		"tv_enabled": 0, "tv_sort": "title", "anime_status": "Completed", "language": "fr", "last_login": "2025-12-31 23:00:00.5",
	})
	f.user(2, "bob", map[string]any{"is_staff": 1, "is_active": 0, "token": "web-tok-2"})
	key := "enc_abcdefghijklmnopqrstuvwxyz012345678901234"
	f.exec(`INSERT INTO api_apitoken (id, name, scope, prefix, key_hash, key_md5, created_at, last_used_at, revoked_at, user_id)
		VALUES (1, 'assistant', 'write', 'enc_abcdef', ?, ?, '2025-01-02 03:04:05.678', '2025-06-01 00:00:00', NULL, 1)`, hexSHA(key), strings.ToUpper(hexMD5(key)))
	f.exec(`INSERT INTO api_apitoken (id, name, scope, prefix, key_hash, key_md5, created_at, revoked_at, user_id)
		VALUES (2, 'old', 'read', 'enc_zzzzzz', 'deadbeef', 'cafe', '2025-01-02 03:04:05', '2025-02-02 00:00:00', 1)`)

	rep := f.must(Options{Timezone: "America/Toronto", DisableKinds: []string{"comic"}})
	if rep.Counts["users"] != 2 || rep.Counts["api tokens"] != 2 {
		t.Fatalf("counts = %v", rep.Counts)
	}

	q := db.New(f.dst)
	ctx := context.Background()
	u, err := q.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.PasswordHash != djangoHash {
		t.Errorf("password hash was changed: %q", u.PasswordHash)
	}
	if ok, rehash := auth.VerifyPassword("correct horse", u.PasswordHash); !ok || !rehash {
		t.Errorf("migrated hash must verify and ask for a rehash: ok=%v rehash=%v", ok, rehash)
	}
	if u.IsAdmin != 1 || u.IsActive != 1 || u.Timezone != "America/Toronto" || u.Language != "fr" {
		t.Errorf("user = %+v", u)
	}
	if u.WebhookToken != "web-tok-1" || u.CalendarToken.String != "web-tok-1" {
		t.Errorf("webhook %q / calendar %q: the Django token must serve both URLs", u.WebhookToken, u.CalendarToken.String)
	}
	if u.JellyfinUsers != "ali,alice-tv" || u.CreatedAt != "2024-01-01T00:00:00Z" || u.LastLoginAt.String != "2025-12-31T23:00:00Z" {
		t.Errorf("jellyfin users %q created %q last login %q", u.JellyfinUsers, u.CreatedAt, u.LastLoginAt.String)
	}
	b, _ := q.GetUserByUsername(ctx, "bob")
	if b.IsAdmin != 1 || b.IsActive != 0 {
		t.Errorf("bob (staff, inactive) = %+v", b)
	}

	settings := map[string]db.UserMediaSetting{}
	rows, err := q.ListUserMediaSettings(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rows {
		settings[s.Kind] = s
	}
	if len(settings) != 9 {
		t.Fatalf("settings = %v", settings)
	}
	if settings["tv"].Enabled != 0 || settings["tv"].Sort != "title" || settings["movie"].Enabled != 1 {
		t.Errorf("tv/movie settings = %+v %+v", settings["tv"], settings["movie"])
	}
	if settings["comic"].Enabled != 0 {
		t.Errorf("comics must be off: %+v", settings["comic"])
	}
	if settings["anime"].Status != "Completed" || settings["anime_movie"].Status != "Completed" {
		t.Errorf("anime movie follows anime: %+v %+v", settings["anime"], settings["anime_movie"])
	}

	// The token keeps working: bearer lookup by sha256, KOSync by md5 + username.
	if _, err := q.GetAPITokenByHash(ctx, hexSHA(key)); err != nil {
		t.Errorf("bearer lookup: %v", err)
	}
	svc := auth.NewService(q, 14)
	if p := svc.AuthenticateMD5(ctx, "alice", hexMD5(key)); p == nil || p.Token.Scope != "write" || p.Token.Name != "assistant" {
		t.Errorf("KOSync auth failed: %+v", p)
	}
	if p := svc.AuthenticateMD5(ctx, "alice", "cafe"); p != nil {
		t.Errorf("a revoked token must stay revoked")
	}
	if got := f.str(`SELECT created_at FROM api_tokens WHERE name = 'assistant'`); got != "2025-01-02T03:04:05Z" {
		t.Errorf("created_at = %q", got)
	}
}

func TestShowsFoldIntoEntries(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	show := f.item("tv", "tmdb", "1000", "Plain Show", nil, nil)
	s1 := f.item("season", "tmdb", "1000", "Plain Show", 1, nil)
	s2 := f.item("season", "tmdb", "1000", "Plain Show", 2, nil)
	e11 := f.item("episode", "tmdb", "1000", "Plain Show", 1, 1)
	e12 := f.item("episode", "tmdb", "1000", "Plain Show", 1, 2)
	e13 := f.item("episode", "tmdb", "1000", "Plain Show", 1, 3)
	e21 := f.item("episode", "tmdb", "1000", "Plain Show", 2, 1)
	f.exec(`INSERT INTO app_tv (id, user_id, item_id, status, score, notes, created_at) VALUES (1, 1, ?, 'In progress', 8.5, 'tv note', '2025-02-01 10:00:00.1')`, show)
	f.exec(`INSERT INTO app_season (id, related_tv_id, user_id, item_id, status, score, notes, created_at) VALUES (1, 1, 1, ?, 'Completed', 9, 's1', '2025-02-01 10:00:00')`, s1)
	f.exec(`INSERT INTO app_season (id, related_tv_id, user_id, item_id, status, score, notes, created_at) VALUES (2, 1, 1, ?, 'In progress', NULL, '', '2025-02-01 10:00:00')`, s2)
	ep := func(id, season, item int, end any, notes string) {
		f.exec(`INSERT INTO app_episode (id, related_season_id, item_id, end_date, created_at, notes) VALUES (?, ?, ?, ?, '2025-04-01 10:00:00', ?)`, id, season, item, end, notes)
	}
	ep(1, 1, e11, "2025-03-01 20:00:00", "")
	ep(2, 1, e12, "2025-03-02 20:00:00.123456", "good one")
	ep(3, 1, e12, "2025-06-01 20:00:00", "") // a rewatch of the same episode
	ep(4, 1, e13, nil, "")                   // watched, date unknown
	ep(5, 2, e21, "2025-07-01 20:00:00", "")

	rep := f.must(Options{})
	if rep.Counts["entries tv"] != 1 || rep.Counts["watches (episodes)"] != 5 || rep.Counts["entry seasons"] != 2 {
		t.Fatalf("counts = %v", rep.Counts)
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 1 {
		t.Errorf("seasons and episodes must fold into the show, got %d items", f.count(`SELECT COUNT(*) FROM items`))
	}
	var status, score, notes, progress, started, progressed, finished, created sql.NullString
	if err := f.dst.QueryRow(`SELECT status, score, notes, progress, started_at, progressed_at, finished_at, created_at FROM entries`).
		Scan(&status, &score, &notes, &progress, &started, &progressed, &finished, &created); err != nil {
		t.Fatal(err)
	}
	if status.String != "In progress" || score.String != "8.5" || notes.String != "tv note" {
		t.Errorf("entry = %v %v %v", status, score, notes)
	}
	if progress.String != "4" {
		t.Errorf("progress = %s, want 4 distinct episodes (the rewatch is a second play of one episode)", progress.String)
	}
	if started.String != "2025-03-01T20:00:00Z" || progressed.String != "2025-07-01T20:00:00Z" || finished.Valid {
		t.Errorf("dates started=%v progressed=%v finished=%v", started, progressed, finished)
	}
	if created.String != "2025-02-01T10:00:00Z" {
		t.Errorf("created_at = %v", created)
	}
	if got := f.count(`SELECT COUNT(*) FROM watches WHERE season = 1 AND episode = 2 AND source = 'import'`); got != 2 {
		t.Errorf("rewatch plays of S1E2 = %d, want 2", got)
	}
	if got := f.str(`SELECT watched_at FROM watches WHERE season = 1 AND episode = 3`); got != "2025-04-01T10:00:00Z" {
		t.Errorf("a play without a date falls back to the row's creation time, got %q", got)
	}
	if got := f.str(`SELECT notes FROM watches WHERE season = 1 AND episode = 2 ORDER BY watched_at LIMIT 1`); got != "good one" {
		t.Errorf("episode note = %q", got)
	}
	if got := f.str(`SELECT status || '/' || COALESCE(score, '') || '/' || notes FROM entry_seasons WHERE season = 1`); got != "Completed/9.0/s1" {
		t.Errorf("season 1 = %q", got)
	}

	// A Completed show gets its finish date from the last play.
	f.exec(`UPDATE app_tv SET status = 'Completed'`)
	f.must(Options{Force: true})
	if got := f.str(`SELECT finished_at FROM entries`); got != "2025-07-01T20:00:00Z" {
		t.Errorf("finished_at = %q", got)
	}
}

func TestMoviesAndCountKindsKeepRuns(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	movie := f.item("movie", "tmdb", "50", "Some Film", nil, nil)
	f.exec(`INSERT INTO app_movie (id, user_id, item_id, status, score, notes, start_date, end_date, progress, progressed_at, created_at)
		VALUES (1, 1, ?, 'Completed', 7, '', NULL, '2024-03-03 19:00:00', 1, '2024-03-03 19:00:00', '2024-03-04 00:00:00')`, movie)
	f.exec(`INSERT INTO app_movie (id, user_id, item_id, status, score, notes, start_date, end_date, progress, progressed_at, created_at)
		VALUES (2, 1, ?, 'Completed', 9, 'second time', NULL, '2025-05-05 19:00:00', 0, '2025-05-05 19:00:00', '2025-05-06 00:00:00')`, movie)
	planned := f.item("movie", "tmdb", "51", "Planned Film", nil, nil)
	f.exec(`INSERT INTO app_movie (id, user_id, item_id, status, notes, progress, progressed_at, created_at)
		VALUES (3, 1, ?, 'Planning', '', 0, '2025-01-01 00:00:00', '2025-01-01 00:00:00')`, planned)
	manga := f.item("manga", "mal", "7", "A Manga", nil, nil)
	f.exec(`INSERT INTO app_manga (id, user_id, item_id, status, score, notes, start_date, end_date, progress, progressed_at, created_at)
		VALUES (1, 1, ?, 'Completed', 8, 'first read', '2023-01-01 05:00:00', '2023-02-01 05:00:00', 50, '2023-02-01 05:00:00', '2023-03-01 00:00:00')`, manga)
	f.exec(`INSERT INTO app_manga (id, user_id, item_id, status, notes, start_date, progress, progressed_at, created_at)
		VALUES (2, 1, ?, 'In progress', '', '2025-01-01 05:00:00', 12, '2025-02-01 05:00:00', '2025-01-01 00:00:00')`, manga)

	rep := f.must(Options{})
	if rep.Counts["entries movie"] != 3 || rep.Counts["entries manga"] != 2 || rep.Counts["watches (movies)"] != 2 {
		t.Fatalf("counts = %v", rep.Counts)
	}
	rows, err := f.dst.Query(`SELECT i.external_id, e.run, e.status, e.progress, COALESCE(e.finished_at, ''), COALESCE(e.started_at, '') FROM entries e JOIN items i ON i.id = e.item_id ORDER BY i.external_id, e.run`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, status, fin, start string
		var run, progress int
		if err := rows.Scan(&id, &run, &status, &progress, &fin, &start); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s r%d %s p%d %s|%s", id, run, status, progress, fin, start))
	}
	want := []string{
		"50 r1 Completed p1 2024-03-03T19:00:00Z|",
		"50 r2 Completed p1 2025-05-05T19:00:00Z|", // a completed movie always has progress 1
		"51 r1 Planning p0 |",
		"7 r1 Completed p50 2023-02-01T05:00:00Z|2023-01-01T05:00:00Z",
		"7 r2 In progress p12 |2025-01-01T05:00:00Z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := f.count(`SELECT COUNT(*) FROM watches w JOIN entries e ON e.id = w.entry_id WHERE w.season IS NULL AND e.item_id = ?`, movie); n != 2 {
		t.Errorf("movie plays = %d, want one per completed run", n)
	}
	if got := f.str(`SELECT watched_at FROM watches ORDER BY id LIMIT 1`); got != "2024-03-03T19:00:00Z" {
		t.Errorf("movie play date = %q", got)
	}
}

func TestAnimeClassification(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	f.exec(`INSERT INTO catalog_animemapping (mal_id, anilist_id, kind, tmdb_tv, tmdb_movie, tmdb_season, tmdb_offset, tvdb_id) VALUES (100, 200, 'TV', 2000, NULL, 1, 0, 9000)`)
	f.exec(`INSERT INTO catalog_animemapping (mal_id, anilist_id, kind, tmdb_tv, tmdb_movie, tmdb_season, tmdb_offset, tvdb_id) VALUES (300, 400, 'MOVIE', NULL, 3000, NULL, 0, NULL)`)
	animeShow := f.item("tv", "tmdb", "2000", "Mapped Show", nil, nil)
	plainShow := f.item("tv", "tmdb", "2001", "Live Action", nil, nil)
	animeFilm := f.item("movie", "tmdb", "3000", "Mapped Film", nil, nil)
	plainFilm := f.item("movie", "tmdb", "3001", "Plain Film", nil, nil)
	countAnime := f.item("anime", "mal", "100", "Count Based", nil, nil)
	for i, it := range []int{animeShow, plainShow} {
		f.exec(`INSERT INTO app_tv (id, user_id, item_id, status, notes, created_at) VALUES (?, 1, ?, 'Planning', '', '2025-01-01 00:00:00')`, i+1, it)
	}
	for i, it := range []int{animeFilm, plainFilm} {
		f.exec(`INSERT INTO app_movie (id, user_id, item_id, status, notes, progress, progressed_at, created_at) VALUES (?, 1, ?, 'Planning', '', 0, '2025-01-01 00:00:00', '2025-01-01 00:00:00')`, i+1, it)
	}
	f.exec(`INSERT INTO app_anime (id, user_id, item_id, status, score, notes, progress, progressed_at, created_at) VALUES (1, 1, ?, 'Completed', 9, '', 12, '2025-01-01 00:00:00', '2025-01-01 00:00:00')`, countAnime)

	// The loader fails: the Django copy of the mapping is used, with a warning.
	rep := f.must(Options{LoadMappings: func(context.Context) error { return errors.New("offline") }})
	if rep.Counts["anime mappings (django copy)"] != 2 || len(rep.Warnings) == 0 {
		t.Errorf("fallback not used: %v %v", rep.Counts, rep.Warnings)
	}
	kinds := map[string]string{}
	rows, err := f.dst.Query(`SELECT source || ':' || external_id, kind || '/' || kind_override FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		kinds[k] = v
	}
	rows.Close()
	want := map[string]string{
		"tmdb:2000": "anime/0", "tmdb:2001": "tv/0", "tmdb:3000": "anime_movie/0", "tmdb:3001": "movie/0", "mal:100": "anime/0",
	}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s = %q, want %q", k, kinds[k], v)
		}
	}
	// Mapped titles know their MAL / AniList / TVDB ids; the count-based anime stays on MAL.
	for _, id := range [][2]string{{"mal", "100"}, {"anilist", "200"}, {"tvdb", "9000"}} {
		if f.count(`SELECT COUNT(*) FROM item_ids WHERE item_id = ? AND source = ? AND external_id = ?`, animeShow, id[0], id[1]) != 1 {
			t.Errorf("missing item id %v on the mapped show", id)
		}
	}
	if f.count(`SELECT COUNT(*) FROM item_ids WHERE item_id = ? AND source = 'anilist' AND external_id = '200'`, countAnime) != 1 {
		t.Errorf("count-based anime should know its AniList id")
	}
	if got := f.str(`SELECT status || '/' || progress || '/' || score FROM entries WHERE item_id = ?`, countAnime); got != "Completed/12/9.0" {
		t.Errorf("count-based anime entry = %q", got)
	}
	if f.count(`SELECT COUNT(*) FROM watches WHERE entry_id IN (SELECT id FROM entries WHERE item_id = ?)`, countAnime) != 0 {
		t.Errorf("count-based anime must not get plays")
	}

	// A loader that works wins over the Django copy.
	f2 := newFixture(t)
	f2.user(1, "alice", nil)
	f2.item("tv", "tmdb", "2000", "Mapped Show", nil, nil)
	f2.must(Options{LoadMappings: func(context.Context) error {
		_, err := f2.dst.Exec(`INSERT INTO anime_mappings (mal_id, kind, tmdb_tv, tmdb_offset) VALUES (1, 'TV', 2000, 0)`)
		return err
	}})
	if f2.str(`SELECT kind FROM items`) != "anime" {
		t.Errorf("loaded mapping not used")
	}
}

func TestHistoryBecomesEvents(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	manga := f.item("manga", "mal", "7", "A Manga", nil, nil)
	other := f.item("manga", "mal", "8", "Imported Manga", nil, nil)
	for id, it := range map[int]int{1: manga, 2: other} {
		f.exec(`INSERT INTO app_manga (id, user_id, item_id, status, score, notes, progress, progressed_at, created_at) VALUES (?, 1, ?, 'Completed', 8.5, '', 25, '2025-03-01 00:00:00', '2025-01-01 00:00:00')`, id, it)
	}
	hist := func(id int, typ string, at string, status string, progress int, score any, user any) {
		f.exec(`INSERT INTO app_historicalmanga (id, score, progress, status, notes, history_date, history_type, history_user_id) VALUES (?, ?, ?, ?, '', ?, ?, ?)`,
			id, score, progress, status, at, typ, user)
	}
	hist(1, "+", "2025-01-01 10:00:00", "In progress", 0, nil, 1)
	hist(1, "~", "2025-02-01 10:00:00", "In progress", 10, nil, 1)
	hist(1, "~", "2025-03-01 10:00:00", "Completed", 25, 8.5, 1)
	hist(2, "+", "2025-01-01 09:00:00", "In progress", 5, nil, nil) // bulk import, no user
	hist(2, "~", "2025-01-02 09:00:00", "In progress", 6, nil, nil)
	hist(99, "+", "2025-01-01 09:00:00", "Planning", 0, nil, 1) // belongs to a deleted entry

	f.must(Options{})
	entry := f.count(`SELECT id FROM entries WHERE item_id = ?`, manga)
	rows, err := f.dst.Query(`SELECT at, field, COALESCE(old, '-'), COALESCE(new, '-') FROM entry_events WHERE entry_id = ? ORDER BY id`, entry)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var at, field, old, nw string
		if err := rows.Scan(&at, &field, &old, &nw); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{at, field, old, nw}, " "))
	}
	rows.Close()
	want := []string{
		"2025-01-01T10:00:00Z created - In progress",
		"2025-02-01T10:00:00Z progress 0 10",
		"2025-03-01T10:00:00Z status In progress Completed",
		"2025-03-01T10:00:00Z progress 10 25",
		"2025-03-01T10:00:00Z score - 8.5",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := f.count(`SELECT COUNT(*) FROM entry_events WHERE entry_id = (SELECT id FROM entries WHERE item_id = ?)`, other); n != 0 {
		t.Errorf("rows without a user are bulk-import noise and are skipped, got %d events", n)
	}
}

func TestAllHistoryFlag(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	manga := f.item("manga", "mal", "8", "Imported Manga", nil, nil)
	f.exec(`INSERT INTO app_manga (id, user_id, item_id, status, notes, progress, progressed_at, created_at) VALUES (1, 1, ?, 'In progress', '', 6, '2025-03-01 00:00:00', '2025-01-01 00:00:00')`, manga)
	f.exec(`INSERT INTO app_historicalmanga (id, progress, status, notes, history_date, history_type) VALUES (1, 5, 'In progress', '', '2025-01-01 09:00:00', '+')`)
	f.exec(`INSERT INTO app_historicalmanga (id, progress, status, notes, history_date, history_type) VALUES (1, 6, 'In progress', '', '2025-01-02 09:00:00', '~')`)
	f.must(Options{AllHistory: true})
	if got := f.count(`SELECT COUNT(*) FROM entry_events`); got != 3 {
		t.Errorf("events = %d, want created + progress up to 5 + progress 5 to 6", got)
	}
	if got := f.str(`SELECT COALESCE(old, 'NULL') || '>' || new FROM entry_events WHERE field = 'progress' ORDER BY id LIMIT 1`); got != "NULL>5" {
		t.Errorf("first progress event = %q", got)
	}
}

func TestReadingCatalogAndLists(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	f.user(2, "bob", nil)
	book := f.item("manga", "mal", "7", "Linked Item", nil, nil)
	show := f.item("tv", "tmdb", "1000", "Show On A List", nil, nil)
	season := f.item("season", "tmdb", "1000", "Show On A List", 1, nil)
	f.exec(`INSERT INTO reading_readingdocument (id, document_hash, title, authors, total_pages, progress, percentage, device, device_id, progress_updated_at, created_at, updated_at, item_id, user_id)
		VALUES (7, 'abc123', 'A Book', 'An Author', 300, '0.5', 0.25, 'Kindle', 'dev1', '2025-05-05 05:05:05', '2025-01-01 00:00:00', '2025-01-02 00:00:00', ?, 1)`, book)
	f.exec(`INSERT INTO reading_readingdocument (id, document_hash, title, authors, progress, device, device_id, created_at, updated_at, user_id)
		VALUES (9, 'def456', 'Other', '', '', '', '', '2025-01-01 00:00:00', '2025-01-02 00:00:00', 1)`)
	f.exec(`INSERT INTO reading_pageevent (id, page, start_time, duration_seconds, total_pages, document_id) VALUES (40, 3, '2025-02-02 02:02:02', 30, 300, 7)`)
	f.exec(`INSERT INTO reading_pageevent (id, page, start_time, duration_seconds, total_pages, document_id) VALUES (41, 4, '2025-02-02 02:02:40', 20, 300, 7)`)
	f.exec(`INSERT INTO reading_readingsession (id, source, title, started_at, ended_at, duration_seconds, pages_read, start_page, end_page, chapters_read, device, document_id, item_id, user_id)
		VALUES (60, 'koreader', 'A Book', '2025-02-02 02:02:02', '2025-02-02 02:03:00', 58, 2, 3, 5, 0, 'Kindle', 7, ?, 1)`, book)
	f.exec(`INSERT INTO catalog_person (id, source, external_id, name, native_name, image) VALUES (5, 'tmdb', '77', 'Some Actor', '', 'http://p/1.jpg')`)
	f.exec(`INSERT INTO catalog_credit (id, role, character, character_image, character_role, language, job, "order", episode_count, item_id, person_id) VALUES (1, 'cast', 'Hero', '', '', 'ja', '', 3, 12, ?, 5)`, show)
	f.exec(`INSERT INTO catalog_credit (id, role, character, character_image, character_role, language, job, "order", episode_count, item_id, person_id) VALUES (2, 'cast', 'Ghost', '', '', '', '', 0, NULL, ?, 5)`, season) // season items have no item row
	f.exec(`INSERT INTO catalog_creditfetch (id, fetched_at, count, error, item_id) VALUES (1, '2025-06-06 06:06:06', 1, '', ?)`, show)

	rep := f.must(Options{})
	for k, v := range map[string]int{
		"reading documents": 2, "page events": 2, "reading sessions": 1, "people": 1, "credits": 1, "credit fetches": 1,
	} {
		if rep.Counts[k] != v {
			t.Errorf("%s = %d, want %d (%v)", k, rep.Counts[k], v, rep.Counts)
		}
	}
	if got := f.str(`SELECT id || '|' || document_hash || '|' || item_id || '|' || progress || '|' || percentage || '|' || progress_updated_at FROM reading_documents WHERE id = 7`); got != fmt.Sprintf("7|abc123|%d|0.5|0.25|2025-05-05T05:05:05Z", book) {
		t.Errorf("document = %q", got)
	}
	if got := f.str(`SELECT COALESCE(item_id, 'none') FROM reading_documents WHERE id = 9`); got != "none" {
		t.Errorf("unlinked document = %q", got)
	}
	if got := f.str(`SELECT document_id || '|' || item_id || '|' || started_at FROM reading_sessions`); got != fmt.Sprintf("7|%d|2025-02-02T02:02:02Z", book) {
		t.Errorf("session = %q", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM page_events WHERE document_id = 7`); got != 2 {
		t.Errorf("page events = %d", got)
	}
	if got := f.str(`SELECT p.id || '|' || p.name || '|' || c.character || '|' || c.episode_count FROM credits c JOIN people p ON p.id = c.person_id`); got != "5|Some Actor|Hero|12" {
		t.Errorf("credit = %q", got)
	}
}

func TestTargetGuardAndForce(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	show := f.item("tv", "tmdb", "1000", "Plain Show", nil, nil)
	f.exec(`INSERT INTO app_tv (id, user_id, item_id, status, notes, created_at) VALUES (1, 1, ?, 'Planning', '', '2025-01-01 00:00:00')`, show)

	f.must(Options{})
	if _, err := f.run(Options{}); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatalf("second run = %v, want ErrTargetNotEmpty", err)
	}
	if n := f.count(`SELECT COUNT(*) FROM entries`); n != 1 {
		t.Fatalf("a refused run changed the target: %d entries", n)
	}
	rep, err := f.run(Options{Force: true})
	if err != nil {
		t.Fatalf("forced run: %v", err)
	}
	if n := f.count(`SELECT COUNT(*) FROM entries`) + f.count(`SELECT COUNT(*) FROM items`)*10 + f.count(`SELECT COUNT(*) FROM users`)*100; n != 111 {
		t.Errorf("forced run must replace, not add: entries+items*10+users*100 = %d", n)
	}
	if rep.Counts["entries tv"] != 1 {
		t.Errorf("counts = %v", rep.Counts)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	show := f.item("tv", "tmdb", "1000", "Plain Show", nil, nil)
	f.exec(`INSERT INTO app_tv (id, user_id, item_id, status, notes, created_at) VALUES (1, 1, ?, 'Planning', '', '2025-01-01 00:00:00')`, show)
	rep := f.must(Options{DryRun: true})
	if !rep.DryRun || rep.Counts["entries tv"] != 1 || rep.Counts["users"] != 1 {
		t.Errorf("report = %+v", rep)
	}
	for _, table := range []string{"users", "items", "entries", "api_tokens", "user_media_settings"} {
		if n := f.count(`SELECT COUNT(*) FROM ` + table); n != 0 {
			t.Errorf("dry run wrote %d rows to %s", n, table)
		}
	}
	// And the real run still works afterwards.
	f.must(Options{})
	if f.count(`SELECT COUNT(*) FROM entries`) != 1 {
		t.Errorf("real run after a dry run failed")
	}
}

func TestNotADjangoDatabase(t *testing.T) {
	f := newFixture(t)
	f.exec(`DROP TABLE app_item`)
	if _, err := f.run(Options{}); err == nil || !strings.Contains(err.Error(), "not an old Django") {
		t.Errorf("err = %v", err)
	}
}

// assumedFixture builds shows around the assumed-earlier-seasons heuristic.
func assumedFixture(t *testing.T) (*fixture, map[string]int) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	ids := map[string]int{}
	n := 0
	// show adds a tracked show with plays at the given (season, episode) pairs.
	show := func(key string, plays [][2]int, seasonRows map[int]string) {
		n++
		item := f.item("tv", "tmdb", fmt.Sprint(5000+n), "Show "+key, nil, nil)
		ids[key] = item
		f.exec(`INSERT INTO app_tv (id, user_id, item_id, status, notes, created_at) VALUES (?, 1, ?, 'In progress', '', '2025-06-01 00:00:00')`, n, item)
		seasonID := map[int]int{}
		have := map[int]bool{}
		for _, p := range plays {
			have[p[0]] = true
		}
		for s := range seasonRows {
			have[s] = true
		}
		for s := range have {
			si := f.item("season", "tmdb", fmt.Sprint(5000+n), "Show "+key, s, nil)
			st := seasonRows[s]
			if st == "" {
				st = "In progress"
			}
			f.nextID++
			f.exec(`INSERT INTO app_season (id, related_tv_id, user_id, item_id, status, notes, created_at) VALUES (?, ?, 1, ?, ?, '', '2025-06-01 00:00:00')`, f.nextID, n, si, st)
			seasonID[s] = f.nextID
		}
		for i, p := range plays {
			ei := f.item("episode", "tmdb", fmt.Sprint(5000+n), "Show "+key, p[0], p[1])
			f.nextID++
			f.exec(`INSERT INTO app_episode (id, related_season_id, item_id, end_date, created_at, notes) VALUES (?, ?, ?, ?, '2025-06-01 00:00:00', '')`,
				f.nextID, seasonID[p[0]], ei, fmt.Sprintf("2025-05-%02d 20:00:00", i+1))
		}
	}
	show("A", [][2]int{{3, 1}, {3, 2}}, nil)                    // started at season 3: seasons 1-2 assumed
	show("B", [][2]int{{1, 1}, {3, 1}}, nil)                    // has a season 1 watch: never touched
	show("C", [][2]int{{1, 1}, {1, 2}}, nil)                    // starts at 1
	show("D", [][2]int{{2, 1}}, map[int]string{1: "Completed"}) // season 1 already marked done
	show("E", [][2]int{{2, 1}}, nil)                            // mid-series but no episode data: deferred
	show("F", [][2]int{{0, 1}}, nil)                            // specials only
	return f, ids
}

func seedEpisodes(f *fixture, ids map[string]int) func(context.Context, int64) error {
	return func(ctx context.Context, item int64) error {
		if item != int64(ids["A"]) && item != int64(ids["D"]) {
			return nil
		}
		eps := []struct {
			season, number int
			air            string
		}{
			{1, 1, "2020-01-01"}, {1, 2, "2020-01-08"},
			{2, 1, "2025-12-01"}, // aired after the first real play: capped
			{2, 2, "2099-01-01"}, // not aired yet
			{3, 1, "2025-04-01"}, {3, 2, "2025-04-08"},
		}
		for _, e := range eps {
			if _, err := f.dst.ExecContext(ctx, `INSERT OR REPLACE INTO item_episodes (item_id, season, number, position, air_date) VALUES (?, ?, ?, ?, ?)`,
				item, e.season, e.number, e.number, e.air); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestAssumedSeasonsHeuristic(t *testing.T) {
	f, ids := assumedFixture(t)
	rep := f.must(Options{SyncSeasons: seedEpisodes(f, ids)})

	if len(rep.Assumed) != 1 || rep.Assumed[0].Title != "Show A" || rep.Assumed[0].FirstSeason != 3 {
		t.Fatalf("assumed = %+v", rep.Assumed)
	}
	if got := fmt.Sprint(rep.Assumed[0].Seasons); got != "[1 2]" || rep.Assumed[0].Episodes != 3 {
		t.Errorf("seasons %s episodes %d, want [1 2] and 3 (S2E2 has not aired)", got, rep.Assumed[0].Episodes)
	}
	if len(rep.Deferred) != 1 || rep.Deferred[0].Title != "Show E" {
		t.Errorf("deferred = %+v (only the show without episode data waits)", rep.Deferred)
	}

	entry := f.count(`SELECT id FROM entries WHERE item_id = ?`, ids["A"])
	rows, err := f.dst.Query(`SELECT season, episode, watched_at, source FROM watches WHERE entry_id = ? AND source = 'assumed' ORDER BY season, episode`, entry)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s, e int
		var at, src string
		if err := rows.Scan(&s, &e, &at, &src); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("S%dE%d %s", s, e, at))
	}
	rows.Close()
	want := []string{
		"S1E1 2020-01-01T12:00:00Z",
		"S1E2 2020-01-08T12:00:00Z",
		"S2E1 2025-05-01T19:59:59Z", // capped one second before the first real play
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("assumed watches = %v, want %v", got, want)
	}
	if got := f.str(`SELECT group_concat(season || ':' || status) FROM (SELECT * FROM entry_seasons WHERE entry_id = ? ORDER BY season)`, entry); got != "1:Completed,2:Completed,3:In progress" {
		t.Errorf("seasons = %q", got)
	}
	// Progress counts assumed episodes; dates and activity do not.
	if got := f.str(`SELECT progress || '|' || started_at || '|' || progressed_at FROM entries WHERE id = ?`, entry); got != "5|2025-05-01T20:00:00Z|2025-05-02T20:00:00Z" {
		t.Errorf("entry = %q", got)
	}
	for _, k := range []string{"B", "C", "D", "E", "F"} {
		if n := f.count(`SELECT COUNT(*) FROM watches WHERE source = 'assumed' AND entry_id IN (SELECT id FROM entries WHERE item_id = ?)`, ids[k]); n != 0 {
			t.Errorf("show %s was touched (%d assumed watches)", k, n)
		}
	}

	// Rerunning is a no-op for A, and picks E up once it has episode data.
	res, err := ApplyAssumed(context.Background(), Options{Target: f.dst, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assumed) != 0 || res.Episodes != 0 || len(res.Deferred) != 1 {
		t.Errorf("second pass = %+v", res)
	}
	ids["A"] = ids["E"] // seed E's episodes like A's
	res, err = ApplyAssumed(context.Background(), Options{Target: f.dst, Now: func() time.Time { return now }, SyncSeasons: seedEpisodes(f, ids)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assumed) != 1 || res.Assumed[0].Title != "Show E" || len(res.Deferred) != 0 {
		t.Errorf("third pass = %+v", res)
	}
}

func TestAssumedSeasonsSkipsParkedSeasonsAndDryRunReports(t *testing.T) {
	f, ids := assumedFixture(t)
	rep := f.must(Options{DryRun: true})
	var titles []string
	for _, a := range rep.Deferred {
		titles = append(titles, a.Title)
	}
	if strings.Join(titles, ",") != "Show A,Show E" {
		t.Errorf("a dry run lists the candidates: %v", titles)
	}
	if f.count(`SELECT COUNT(*) FROM watches`) != 0 {
		t.Errorf("dry run wrote plays")
	}

	// A season the owner dropped is left alone.
	f.must(Options{SkipAssumed: true})
	entry := f.count(`SELECT id FROM entries WHERE item_id = ?`, ids["A"])
	if _, err := f.dst.Exec(`INSERT INTO entry_seasons (entry_id, season, status, notes) VALUES (?, 1, 'Dropped', '')`, entry); err != nil {
		t.Fatal(err)
	}
	res, err := ApplyAssumed(context.Background(), Options{Target: f.dst, Now: func() time.Time { return now }, SyncSeasons: seedEpisodes(f, ids)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assumed) != 1 || fmt.Sprint(res.Assumed[0].Seasons) != "[2]" {
		t.Errorf("assumed = %+v, want only season 2", res.Assumed)
	}
	if got := f.str(`SELECT status FROM entry_seasons WHERE entry_id = ? AND season = 1`, entry); got != "Dropped" {
		t.Errorf("dropped season = %q", got)
	}
}

func TestEnrichHooksAreBestEffort(t *testing.T) {
	f := newFixture(t)
	f.user(1, "alice", nil)
	ok := f.item("tv", "tmdb", "1", "Fine", nil, nil)
	skip := f.item("tv", "tmdb", "2", "No Key", nil, nil)
	bad := f.item("manga", "mal", "3", "Fails", nil, nil)
	var refreshed, synced []int64
	rep := f.must(Options{
		RefreshItem: func(_ context.Context, id int64) error {
			switch int(id) {
			case skip:
				return ErrSkip
			case bad:
				return errors.New("provider down")
			}
			refreshed = append(refreshed, id)
			return nil
		},
		SyncSeasons: func(_ context.Context, id int64) error { synced = append(synced, id); return nil },
	})
	if fmt.Sprint(refreshed) != fmt.Sprint([]int64{int64(ok)}) || fmt.Sprint(synced) != fmt.Sprint([]int64{int64(ok), int64(skip)}) {
		t.Errorf("refreshed %v synced %v, want the item that refreshed, and both shows synced (skipping a refresh does not skip seasons)", refreshed, synced)
	}
	if rep.Counts["items refreshed"] != 1 || rep.Counts["items not refreshed (no provider key)"] != 1 || rep.Counts["enrich failures"] != 1 {
		t.Errorf("counts = %v", rep.Counts)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "provider down") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	if f.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Errorf("a failing refresh must not lose items")
	}
}

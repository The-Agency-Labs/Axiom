package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

func TestCreateUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	t.Setenv("DATABASE_PATH", path)
	t.Setenv("SECRET", "test-secret")

	if err := createUser([]string{"alice", "--admin"}, strings.NewReader("password-1\n")); err != nil {
		t.Fatal(err)
	}
	if err := createUser([]string{"bob"}, strings.NewReader("short\n")); err == nil {
		t.Fatal("short password accepted")
	}
	if err := createUser([]string{"alice"}, strings.NewReader("password-2\n")); err == nil {
		t.Fatal("duplicate username accepted")
	}

	conn, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	u, err := db.New(conn).GetUserByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.IsAdmin != 1 || u.IsActive != 1 || u.WebhookToken == "" {
		t.Fatalf("user: %+v", u)
	}
	if ok, _ := auth.VerifyPassword("password-1", u.PasswordHash); !ok {
		t.Fatal("stored hash does not verify")
	}
}

func TestMigrateDjangoCommand(t *testing.T) {
	dir := t.TempDir()
	schema, err := os.ReadFile(filepath.Join("..", "..", "internal", "migrate", "testdata", "django_schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "django.db")
	sdb, err := sql.Open("sqlite", "file:"+src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sdb.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	// One synthetic user; the CHECK constraints on layouts, sorts and
	// statuses need valid values.
	if _, err := sdb.Exec(`INSERT INTO users_user (id, password, is_superuser, username, first_name, last_name, email, is_staff, is_active, date_joined,
		last_search_type, anime_layout, manga_layout, movie_layout, season_layout, tv_layout, is_demo, game_layout, token, anime_enabled, book_enabled, game_enabled,
		manga_enabled, movie_enabled, season_enabled, tv_enabled, book_layout, calendar_layout, lists_sort, home_sort, anime_sort, book_sort, game_sort, manga_sort,
		movie_sort, season_sort, tv_sort, list_detail_sort, anime_status, book_status, game_status, manga_status, movie_status, season_status, tv_status,
		notification_urls, comic_enabled, comic_layout, comic_sort, comic_status, daily_digest_enabled, release_notifications_enabled, plex_usernames,
		clickable_media_cards, list_detail_status, quick_watch_date, date_format, time_format, boardgame_enabled, boardgame_layout, boardgame_sort,
		boardgame_status, progress_bar, hide_completed_recommendations, hide_zero_rating, watch_provider_region, obfuscate_unseen_episodes, profile_private,
		week_start_day, jellyfin_mark_played_enabled, jellyfin_mark_unplayed_enabled, home_hide_unreleased, language, show_adult_titles)
		VALUES (1, 'pbkdf2_sha256$1$s$aa', 1, 'owner', '', '', '', 1, 1, '2024-01-01 00:00:00', 'tv', 'grid', 'grid', 'grid', 'grid', 'grid', 0, 'grid', 'tok',
		1, 1, 1, 1, 1, 1, 1, 'grid', 'grid', 'name', 'upcoming', 'score', 'score', 'score', 'score', 'score', 'score', 'score', 'date_added', 'All', 'All', 'All',
		'All', 'All', 'All', 'All', '', 1, 'grid', 'score', 'All', 0, 0, '', 0, 'All', 'current_date', '', '', 1, 'grid', 'score', 'All', 0, 0, 0, '', 0, 0,
		'monday', 0, 0, 0, 'en', 0)`); err != nil {
		t.Fatal(err)
	}
	sdb.Close()
	mappings := filepath.Join(dir, "anime.json")
	if err := os.WriteFile(mappings, []byte(`[{"mal_id": 1, "anilist_id": 2, "type": "TV", "themoviedb_id": {"tv": 5}}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "go.db")
	t.Setenv("DATABASE_PATH", target)
	t.Setenv("SECRET", "test-secret")
	t.Setenv("TZ", "UTC")

	var out strings.Builder
	if err := migrateDjango(nil, &out); err == nil {
		t.Error("--from is required")
	}
	if err := migrateDjango([]string{"--from", src, "--dry-run", "--no-refresh", "--mappings-file", mappings}, &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.String(), "DRY RUN") || !strings.Contains(out.String(), "users") {
		t.Errorf("dry run output = %q", out.String())
	}
	out.Reset()
	if err := migrateDjango([]string{"--from", src, "--no-refresh", "--timezone", "America/Toronto"}, &out); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	conn, err := db.Open(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	u, err := db.New(conn).GetUserByUsername(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if u.WebhookToken != "tok" || u.Timezone != "America/Toronto" || u.IsAdmin != 1 {
		t.Errorf("user = %+v", u)
	}
	if ok, err := db.New(conn).AnimeMappingsExist(context.Background()); err != nil || !ok {
		t.Errorf("mappings from --mappings-file missing (%v)", err)
	}
}

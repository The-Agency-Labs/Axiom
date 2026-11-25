package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// kindFlags maps a Go media kind to the Django user columns prefix it takes
// its enabled/sort/status settings from. Anime movies follow anime.
var kindFlags = []struct{ kind, prefix string }{
	{"tv", "tv"}, {"anime", "anime"}, {"movie", "movie"}, {"anime_movie", "anime"},
	{"manga", "manga"}, {"comic", "comic"}, {"book", "book"}, {"game", "game"}, {"boardgame", "boardgame"},
}

var (
	validSorts    = map[string]bool{"activity": true, "score": true, "title": true, "progress": true, "start_date": true, "end_date": true}
	validStatuses = map[string]bool{"All": true, "Planning": true, "In progress": true, "Paused": true, "Dropped": true, "Completed": true}
)

func (m *migrator) users() error {
	rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM users_user ORDER BY id`)
	if err != nil {
		return err
	}
	disabled := map[string]bool{}
	for _, k := range m.opts.DisableKinds {
		disabled[strings.TrimSpace(k)] = true
	}
	now := stampOf(m.opts.Now())
	for _, r := range rows {
		token := r.str("token")
		if token == "" {
			return fmt.Errorf("user %q has no token", r.str("username"))
		}
		lang := r.str("language")
		if lang == "" {
			lang = "en"
		}
		created := r.stamp("date_joined")
		if !created.Valid {
			created = sql.NullString{String: now, Valid: true}
		}
		admin := 0
		if r.boolean("is_superuser") || r.boolean("is_staff") {
			admin = 1
		}
		var id int64
		err := m.tx.QueryRowContext(m.ctx, `SELECT id FROM users WHERE username = ?`, r.str("username")).Scan(&id)
		switch {
		case err == nil:
			// A createuser account of the same name: it becomes the migrated one.
			if _, err := m.exec(`UPDATE users SET password_hash = ?, is_admin = ?, is_active = ?, timezone = ?, language = ?,
				webhook_token = ?, calendar_token = ?, jellyfin_users = ?, created_at = ?, last_login_at = ? WHERE id = ?`,
				r.str("password"), admin, b2i(r.boolean("is_active")), m.opts.Timezone, lang, token, token,
				jellyfinUsers(r), created.String, r.stamp("last_login"), id); err != nil {
				return err
			}
			m.rep.warn("user %q already existed in the target and was overwritten", r.str("username"))
		case errors.Is(err, sql.ErrNoRows):
			id, err = m.insertID(`INSERT INTO users (username, password_hash, is_admin, is_active, timezone, language,
				webhook_token, calendar_token, jellyfin_users, watched_percent, created_at, last_login_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 75, ?, ?)`,
				r.str("username"), r.str("password"), admin, b2i(r.boolean("is_active")), m.opts.Timezone, lang,
				token, token, jellyfinUsers(r), created.String, r.stamp("last_login"))
			if err != nil {
				return fmt.Errorf("user %q: %w", r.str("username"), err)
			}
		default:
			return err
		}
		m.userMap[r.i64("id")] = id
		m.rep.count("users", 1)

		for _, k := range kindFlags {
			enabled := 1
			if col := k.prefix + "_enabled"; !r.null(col) && !r.boolean(col) {
				enabled = 0
			}
			if disabled[k.kind] {
				enabled = 0
			}
			sort, status := r.str(k.prefix+"_sort"), r.str(k.prefix+"_status")
			if !validSorts[sort] {
				sort = "activity"
			}
			if !validStatuses[status] {
				status = "In progress"
			}
			if _, err := m.exec(`INSERT INTO user_media_settings (user_id, kind, enabled, sort, status) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (user_id, kind) DO UPDATE SET enabled = excluded.enabled, sort = excluded.sort, status = excluded.status`,
				id, k.kind, enabled, sort, status); err != nil {
				return err
			}
		}
	}
	return nil
}

// jellyfinUsers is the list of media-server usernames Django matched to the
// user (the field is named plex_usernames but the webhook used it for Jellyfin too).
func jellyfinUsers(r row) string {
	var out []string
	for _, p := range strings.Split(r.str("plex_usernames"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// apiTokens copies the tokens as they are: only hashes exist, and the Go
// server hashes keys the same way (sha256 hex, md5 hex for KOSync).
func (m *migrator) apiTokens() error {
	if !hasTable(m.ctx, m.opts.Source, "api_apitoken") {
		return nil
	}
	rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM api_apitoken ORDER BY id`)
	if err != nil {
		return err
	}
	now := stampOf(m.opts.Now())
	for _, r := range rows {
		uid, ok := m.userMap[r.i64("user_id")]
		if !ok {
			m.rep.warn("api token %d belongs to an unknown user; skipped", r.i64("id"))
			continue
		}
		scope := r.str("scope")
		if scope != "read" && scope != "write" {
			scope = "read"
		}
		created := r.stamp("created_at")
		if !created.Valid {
			created = sql.NullString{String: now, Valid: true}
		}
		if _, err := m.exec(`INSERT INTO api_tokens (user_id, name, scope, prefix, key_hash, key_md5, created_at, last_used_at, revoked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uid, r.str("name"), scope, r.str("prefix"), r.str("key_hash"), strings.ToLower(r.str("key_md5")),
			created.String, r.stamp("last_used_at"), r.stamp("revoked_at")); err != nil {
			return fmt.Errorf("api token %q: %w", r.str("name"), err)
		}
		m.rep.count("api tokens", 1)
	}
	return nil
}

-- name: ListUserMediaSettings :many
SELECT * FROM user_media_settings WHERE user_id = ? ORDER BY kind;

-- name: UpsertUserMediaSetting :exec
INSERT INTO user_media_settings (user_id, kind, enabled, sort, status)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, kind) DO UPDATE SET enabled = excluded.enabled, sort = excluded.sort, status = excluded.status;

-- name: UpdateUserSettings :exec
UPDATE users SET timezone = ?, language = ?, jellyfin_users = ?, watched_percent = ? WHERE id = ?;

-- name: SetWebhookToken :exec
UPDATE users SET webhook_token = ? WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id;

-- name: SetUserFlags :exec
UPDATE users SET is_active = ?, is_admin = ? WHERE id = ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

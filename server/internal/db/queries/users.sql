-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: GetUserByWebhookToken :one
SELECT * FROM users WHERE webhook_token = ?;

-- name: GetUserByCalendarToken :one
SELECT * FROM users WHERE calendar_token = ?;

-- name: SetCalendarToken :exec
UPDATE users SET calendar_token = ? WHERE id = ?;

-- name: CreateUser :one
INSERT INTO users (username, password_hash, is_admin, webhook_token, calendar_token, created_at)
VALUES (?, ?, ?, ?, lower(hex(randomblob(32))), ?)
RETURNING *;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: TouchLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

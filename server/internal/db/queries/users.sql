-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: GetUserByWebhookToken :one
SELECT * FROM users WHERE webhook_token = ?;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (username, password_hash, is_admin, webhook_token, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = ? WHERE id = ?;

-- name: TouchLogin :exec
UPDATE users SET last_login_at = ? WHERE id = ?;

-- name: CreateSession :exec
INSERT INTO sessions (id_hash, user_id, created_at, expires_at, user_agent)
VALUES (?, ?, ?, ?, ?);

-- name: GetSessionUser :one
SELECT sqlc.embed(users), sessions.expires_at AS session_expires_at
FROM sessions JOIN users ON users.id = sessions.user_id
WHERE sessions.id_hash = ? AND sessions.expires_at > ?;

-- name: RenewSession :exec
UPDATE sessions SET expires_at = ? WHERE id_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ?;

-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, scope, prefix, key_hash, key_md5, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAPITokens :many
SELECT * FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC, id DESC;

-- name: GetAPITokenByHash :one
SELECT sqlc.embed(api_tokens), sqlc.embed(users)
FROM api_tokens JOIN users ON users.id = api_tokens.user_id
WHERE api_tokens.key_hash = ? AND api_tokens.revoked_at IS NULL;

-- name: GetAPITokenByMD5 :one
SELECT sqlc.embed(api_tokens), sqlc.embed(users)
FROM api_tokens JOIN users ON users.id = api_tokens.user_id
WHERE users.username = ? AND api_tokens.key_md5 = ? AND api_tokens.revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;

-- name: RevokeAPIToken :one
UPDATE api_tokens SET revoked_at = coalesce(revoked_at, ?)
WHERE id = ? AND user_id = ?
RETURNING *;

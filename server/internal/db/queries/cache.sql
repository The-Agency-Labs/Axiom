-- name: GetCache :one
SELECT value, expires_at FROM provider_cache WHERE key = ? AND expires_at > ?;

-- name: SetCache :exec
INSERT INTO provider_cache (key, value, expires_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at;

-- name: DeleteExpiredCache :execrows
DELETE FROM provider_cache WHERE expires_at <= ?;

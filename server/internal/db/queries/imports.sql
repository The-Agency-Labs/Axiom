-- Keys of kv that start with a prefix (sync markers, saved ids, source status).
-- name: ListKVByPrefix :many
SELECT key, value FROM kv WHERE substr(key, 1, length(sqlc.arg(prefix))) = sqlc.arg(prefix) ORDER BY key;

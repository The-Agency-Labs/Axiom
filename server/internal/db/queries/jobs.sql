-- name: EnqueueJob :one
INSERT INTO jobs (kind, user_id, payload, run_at) VALUES (?, ?, ?, ?) RETURNING id;

-- name: ClaimJob :one
UPDATE jobs SET status = 'running', started_at = ?1, attempts = attempts + 1
WHERE id = (
    SELECT j.id FROM jobs j WHERE j.status = 'queued' AND j.run_at <= ?1
    ORDER BY j.run_at, j.id LIMIT 1
)
RETURNING *;

-- name: FinishJob :exec
UPDATE jobs SET status = 'done', finished_at = ?, result = ? WHERE id = ?;

-- name: FailJob :exec
UPDATE jobs SET status = 'failed', finished_at = ?, result = ? WHERE id = ?;

-- name: RetryJob :exec
UPDATE jobs SET status = 'queued', run_at = ?, result = ? WHERE id = ?;

-- name: RequeueRunningJobs :execrows
UPDATE jobs SET status = 'queued' WHERE status = 'running';

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: SetKV :exec
INSERT INTO kv (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: GetKV :one
SELECT value FROM kv WHERE key = ?;

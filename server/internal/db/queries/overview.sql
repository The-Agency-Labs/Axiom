-- Reading sessions of a user whose start lies in [from_at, to_at), oldest first.
-- name: ListUserReadingSessions :many
SELECT * FROM reading_sessions
WHERE user_id = sqlc.arg(user_id) AND started_at >= sqlc.arg(from_at) AND started_at < sqlc.arg(to_at)
ORDER BY started_at, id;

-- name: GetLastReadingSession :one
SELECT * FROM reading_sessions WHERE user_id = ? ORDER BY started_at DESC, id DESC LIMIT 1;

-- name: GetLastItemReadingSession :one
SELECT * FROM reading_sessions WHERE user_id = ? AND item_id = ? ORDER BY started_at DESC, id DESC LIMIT 1;

-- Progress changes of every entry of a user, in order, for the year views.
-- name: ListUserProgressEvents :many
SELECT ev.entry_id, ev.at, ev.new
FROM entry_events ev JOIN entries e ON e.id = ev.entry_id
WHERE e.user_id = ? AND ev.field = 'progress'
ORDER BY ev.entry_id, ev.at, ev.id;

-- Plays of episodes between two times with the episode's runtime (minutes) when known.
-- name: ListUserEpisodePlays :many
SELECT w.entry_id, w.watched_at, CAST(COALESCE(ep.runtime, 0) AS INTEGER) AS runtime
FROM watches w
JOIN entries e ON e.id = w.entry_id
LEFT JOIN item_episodes ep ON ep.item_id = e.item_id AND ep.season = w.season AND ep.number = w.episode
WHERE e.user_id = sqlc.arg(user_id) AND w.season IS NOT NULL AND w.source != 'assumed'
  AND w.watched_at >= sqlc.arg(from_at) AND w.watched_at < sqlc.arg(to_at)
ORDER BY w.watched_at, w.id;

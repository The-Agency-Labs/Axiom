-- name: GetEntry :one
SELECT * FROM entries WHERE id = ?;

-- name: GetEntryForUser :one
SELECT * FROM entries WHERE id = ? AND user_id = ?;

-- The current entry is the highest run.
-- name: GetCurrentEntry :one
SELECT * FROM entries WHERE user_id = ? AND item_id = ? ORDER BY run DESC LIMIT 1;

-- name: ListEntryRuns :many
SELECT * FROM entries WHERE user_id = ? AND item_id = ? ORDER BY run;

-- name: InsertEntry :one
INSERT INTO entries (user_id, item_id, run, status, score, progress, progressed_at, started_at, finished_at, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateEntry :exec
UPDATE entries
SET status = ?, score = ?, progress = ?, progressed_at = ?, started_at = ?, finished_at = ?, notes = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteEntry :exec
DELETE FROM entries WHERE id = ?;

-- name: GetEntrySeason :one
SELECT * FROM entry_seasons WHERE entry_id = ? AND season = ?;

-- name: ListEntrySeasons :many
SELECT * FROM entry_seasons WHERE entry_id = ? ORDER BY season;

-- name: UpsertEntrySeason :exec
INSERT INTO entry_seasons (entry_id, season, status, score, notes) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (entry_id, season) DO UPDATE SET status = excluded.status, score = excluded.score, notes = excluded.notes;

-- name: InsertWatch :one
INSERT INTO watches (entry_id, season, episode, watched_at, source, notes) VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListEntryWatches :many
SELECT * FROM watches WHERE entry_id = ? ORDER BY season, episode, watched_at, id;

-- name: ListSeasonWatches :many
SELECT * FROM watches WHERE entry_id = ? AND season = ? ORDER BY episode, watched_at, id;

-- The newest watch of one episode, if any.
-- name: LatestEpisodeWatch :one
SELECT * FROM watches WHERE entry_id = ? AND season = ? AND episode = ?
ORDER BY watched_at DESC, id DESC LIMIT 1;

-- name: DeleteWatch :exec
DELETE FROM watches WHERE id = ?;

-- name: GetWatchForUser :one
SELECT w.id, w.entry_id, w.season, w.episode, w.watched_at, w.source, w.notes
FROM watches w JOIN entries e ON e.id = w.entry_id
WHERE w.id = ? AND e.user_id = ?;

-- name: UpdateWatchNotes :exec
UPDATE watches SET notes = ? WHERE id = ?;

-- name: InsertEntryEvent :exec
INSERT INTO entry_events (entry_id, at, field, old, new) VALUES (?, ?, ?, ?, ?);

-- name: ListEntryEvents :many
SELECT * FROM entry_events WHERE entry_id = ? ORDER BY at, id;

-- Latest run of every item a user tracks, with filters. kinds_json is a JSON array of kinds
-- ('' = any kind); status and search are optional.
-- name: ListLibraryEntries :many
SELECT sqlc.embed(e), sqlc.embed(i)
FROM entries e
JOIN items i ON i.id = e.item_id
WHERE e.user_id = sqlc.arg(user_id)
  AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
  AND (sqlc.arg(kinds_json) = '' OR i.kind IN (SELECT value FROM json_each(sqlc.arg(kinds_json))))
  AND (sqlc.narg(status) IS NULL OR e.status = sqlc.narg(status))
  AND (sqlc.narg(search) IS NULL OR instr(lower(i.title), lower(sqlc.narg(search))) > 0
       OR instr(lower(i.native_title), lower(sqlc.narg(search))) > 0
       OR instr(lower(e.notes), lower(sqlc.narg(search))) > 0)
ORDER BY COALESCE(e.progressed_at, e.created_at) DESC, e.id DESC;

-- Every run of every item a user tracks, for year views (activity counts every run).
-- name: ListUserEntries :many
SELECT sqlc.embed(e), sqlc.embed(i)
FROM entries e JOIN items i ON i.id = e.item_id
WHERE e.user_id = ?
ORDER BY e.id;

-- name: GetEntryWithItem :one
SELECT sqlc.embed(e), sqlc.embed(i)
FROM entries e JOIN items i ON i.id = e.item_id
WHERE e.id = ? AND e.user_id = ?;

-- name: ListNowEntries :many
-- In progress entries with something done since a moment: a play, a reading
-- session or a logged change. Dates stored on the entry don't count by
-- themselves (imports write them without anything having happened).
SELECT sqlc.embed(e), sqlc.embed(i)
FROM entries e JOIN items i ON i.id = e.item_id
WHERE e.user_id = sqlc.arg(user_id) AND e.status = 'In progress'
  AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
  AND (EXISTS (SELECT 1 FROM watches w WHERE w.entry_id = e.id AND w.source != 'assumed' AND w.watched_at >= CAST(sqlc.arg(since) AS TEXT))
    OR EXISTS (SELECT 1 FROM entry_events v WHERE v.entry_id = e.id AND v.at >= CAST(sqlc.arg(since) AS TEXT))
    OR EXISTS (SELECT 1 FROM reading_sessions r WHERE r.user_id = e.user_id AND r.item_id = e.item_id AND r.started_at >= CAST(sqlc.arg(since) AS TEXT)))
ORDER BY COALESCE(e.progressed_at, e.created_at) DESC, e.id DESC;

-- name: ListFinishedEntries :many
SELECT sqlc.embed(e), sqlc.embed(i)
FROM entries e JOIN items i ON i.id = e.item_id
WHERE e.user_id = sqlc.arg(user_id) AND e.status = 'Completed' AND e.finished_at IS NOT NULL
  AND (sqlc.arg(kinds_json) = '' OR i.kind IN (SELECT value FROM json_each(sqlc.arg(kinds_json))))
ORDER BY e.finished_at DESC, e.id DESC
LIMIT sqlc.arg(max_rows);

-- Seasons of in-progress shows (the "Now" page lists in-progress seasons, not shows).
-- name: ListNowSeasons :many
SELECT s.entry_id, s.season, s.status, s.score, s.notes
FROM entry_seasons s
JOIN entries e ON e.id = s.entry_id
WHERE e.user_id = ? AND e.status = 'In progress' AND s.status = 'In progress' AND s.season > 0
  AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id);

-- name: ListUserWatchesBetween :many
SELECT w.id, w.entry_id, w.season, w.episode, w.watched_at, w.source, w.notes
FROM watches w JOIN entries e ON e.id = w.entry_id
WHERE e.user_id = ? AND w.watched_at >= ? AND w.watched_at < ? AND w.source != 'assumed'
ORDER BY w.watched_at, w.id;

-- Activity feed: change events and plays of one user, newest first, pageable by (at, id).
-- name: ListActivityEvents :many
SELECT ev.id, ev.entry_id, ev.at, ev.field, ev.old, ev.new, sqlc.embed(i)
FROM entry_events ev
JOIN entries e ON e.id = ev.entry_id
JOIN items i ON i.id = e.item_id
WHERE e.user_id = sqlc.arg(user_id)
  AND ev.at >= sqlc.arg(from_at) AND ev.at < sqlc.arg(to_at)
  AND (ev.at < sqlc.arg(cursor_at) OR (ev.at = sqlc.arg(cursor_at) AND ev.id < sqlc.arg(cursor_id)))
ORDER BY ev.at DESC, ev.id DESC
LIMIT sqlc.arg(max_rows);

-- name: ListActivityWatches :many
SELECT w.id, w.entry_id, w.season, w.episode, w.watched_at, w.source, sqlc.embed(i)
FROM watches w
JOIN entries e ON e.id = w.entry_id
JOIN items i ON i.id = e.item_id
WHERE e.user_id = sqlc.arg(user_id)
  AND w.source != 'assumed'
  AND w.watched_at >= sqlc.arg(from_at) AND w.watched_at < sqlc.arg(to_at)
  AND (w.watched_at < sqlc.arg(cursor_at) OR (w.watched_at = sqlc.arg(cursor_at) AND w.id < sqlc.arg(cursor_id)))
ORDER BY w.watched_at DESC, w.id DESC
LIMIT sqlc.arg(max_rows);

-- Distinct episodes of every regular season of an entry that have been watched, for progress.
-- name: CountWatchedEpisodes :one
SELECT COUNT(*) FROM (
    SELECT DISTINCT season, episode FROM watches WHERE entry_id = ? AND season > 0
);

-- name: WatchBounds :one
SELECT MIN(watched_at) AS first_at, MAX(watched_at) AS last_at
FROM watches WHERE entry_id = ? AND season > 0 AND source != 'assumed';

-- name: ListCompletedSeasons :many
-- Every completed regular season of the user's shows of one kind, with the
-- season's own art and the dates of its first and last watch. Assumed watches
-- (seasons watched before Axiom, dated by air date) don't date a season.
SELECT e.id AS entry_id, i.id AS item_id, i.source, i.external_id, i.title, i.image AS show_image,
       es.season, es.score,
       CAST(COALESCE(s.title, '') AS TEXT) AS season_title,
       CAST(COALESCE(s.image, '') AS TEXT) AS season_image,
       CAST(COALESCE(s.episode_count, 0) AS INTEGER) AS episode_count,
       CAST(COALESCE((SELECT MIN(w.watched_at) FROM watches w WHERE w.entry_id = e.id AND w.season = es.season AND w.source <> 'assumed'), '') AS TEXT) AS started_at,
       CAST(COALESCE((SELECT MAX(w.watched_at) FROM watches w WHERE w.entry_id = e.id AND w.season = es.season AND w.source <> 'assumed'), '') AS TEXT) AS finished_at
FROM entry_seasons es
JOIN entries e ON e.id = es.entry_id
JOIN items i ON i.id = e.item_id
LEFT JOIN item_seasons s ON s.item_id = i.id AND s.number = es.season
WHERE e.user_id = ? AND i.kind = ? AND es.season > 0 AND es.status = 'Completed'
ORDER BY finished_at DESC, i.title, es.season;

-- name: DeleteEntrySeason :exec
DELETE FROM entry_seasons WHERE entry_id = ? AND season = ?;

-- name: UpdateEntryEventValues :exec
UPDATE entry_events SET old = ?, new = ? WHERE id = ?;

-- name: ListItemEntries :many
SELECT * FROM entries WHERE item_id = ? ORDER BY user_id, run, id;

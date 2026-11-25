-- Queries of the one-shot TMDB -> TVDB show conversion (internal/showconv).

-- name: ListShowItemsBySource :many
SELECT * FROM items WHERE source = ? AND kind IN ('tv', 'anime') ORDER BY id;

-- name: ListItemEntries :many
SELECT * FROM entries WHERE item_id = ? ORDER BY user_id, run, id;

-- name: MaxEntryRun :one
SELECT COALESCE(MAX(run), 0) FROM entries WHERE user_id = ? AND item_id = ?;

-- name: MoveEntry :exec
UPDATE entries SET item_id = ?, run = ?, progress = ? WHERE id = ?;

-- name: UpdateWatchEpisode :exec
UPDATE watches SET season = ?, episode = ?, notes = ? WHERE id = ?;

-- name: DeleteEntrySeasons :exec
DELETE FROM entry_seasons WHERE entry_id = ?;

-- name: MoveListItems :exec
UPDATE OR IGNORE list_items SET item_id = ? WHERE item_id = ?;

-- name: DeleteListItemsOfItem :exec
DELETE FROM list_items WHERE item_id = ?;

-- name: MoveReadingDocuments :exec
UPDATE reading_documents SET item_id = ? WHERE item_id = ?;

-- name: MoveReadingSessions :exec
UPDATE reading_sessions SET item_id = ? WHERE item_id = ?;

-- name: DeleteItem :exec
DELETE FROM items WHERE id = ?;

-- name: CountEpisodeWatches :one
SELECT COUNT(*) FROM watches w JOIN entries e ON e.id = w.entry_id
WHERE w.season IS NOT NULL AND w.episode IS NOT NULL
  AND e.item_id IN (SELECT id FROM items WHERE kind IN ('tv', 'anime'));

-- name: CountItemWatches :one
SELECT COUNT(*) FROM watches w JOIN entries e ON e.id = w.entry_id WHERE e.item_id = ?;

-- name: AddItemLink :exec
INSERT INTO item_links (user_id, item_id, source) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;

-- name: DeleteSourceLinks :exec
DELETE FROM item_links WHERE user_id = ? AND source = ?;

-- name: ListItemLinks :many
SELECT item_id, source FROM item_links WHERE user_id = ? ORDER BY item_id, source;

-- name: ListKindleItems :many
SELECT DISTINCT CAST(COALESCE(r.item_id, d.item_id) AS INTEGER) AS item_id FROM reading_documents d
LEFT JOIN reading_documents r ON r.id = d.merged_into
WHERE d.user_id = ? AND d.ignored = 0 AND COALESCE(r.item_id, d.item_id) IS NOT NULL;

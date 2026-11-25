-- name: CreateList :one
INSERT INTO lists (owner_id, name, description, visibility, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: GetListRow :one
SELECT l.id, l.owner_id, l.name, l.description, l.visibility, l.created_at,
       u.username AS owner_username,
       EXISTS (SELECT 1 FROM list_members m WHERE m.list_id = l.id AND m.user_id = sqlc.arg(viewer_id)) AS is_member
FROM lists l
JOIN users u ON u.id = l.owner_id
WHERE l.id = sqlc.arg(id);

-- Lists the user owns or collaborates on, with counts. has_item says whether item_id (0 = none) is in the list.
-- name: ListListsForUser :many
SELECT l.id, l.owner_id, l.name, l.description, l.visibility, l.created_at,
       u.username AS owner_username,
       (SELECT COUNT(*) FROM list_items li WHERE li.list_id = l.id) AS items_count,
       CAST(COALESCE((SELECT MAX(li.added_at) FROM list_items li WHERE li.list_id = l.id), '') AS TEXT) AS last_added_at,
       CAST(COALESCE((SELECT i.image FROM list_items li JOIN items i ON i.id = li.item_id
                  WHERE li.list_id = l.id ORDER BY li.added_at, li.item_id LIMIT 1), '') AS TEXT) AS image,
       EXISTS (SELECT 1 FROM list_items li WHERE li.list_id = l.id AND li.item_id = sqlc.arg(item_id)) AS has_item
FROM lists l
JOIN users u ON u.id = l.owner_id
WHERE l.owner_id = sqlc.arg(user_id)
   OR EXISTS (SELECT 1 FROM list_members m WHERE m.list_id = l.id AND m.user_id = sqlc.arg(user_id))
ORDER BY l.name COLLATE NOCASE, l.id;

-- name: UpdateList :exec
UPDATE lists SET name = ?, description = ?, visibility = ? WHERE id = ?;

-- name: DeleteList :exec
DELETE FROM lists WHERE id = ?;

-- name: ListListMembers :many
SELECT u.id, u.username
FROM list_members m
JOIN users u ON u.id = m.user_id
WHERE m.list_id = ?
ORDER BY u.username COLLATE NOCASE;

-- name: AddListMember :exec
INSERT OR IGNORE INTO list_members (list_id, user_id) VALUES (?, ?);

-- name: RemoveListMember :execrows
DELETE FROM list_members WHERE list_id = ? AND user_id = ?;

-- The items of a list with the viewer's current entry (highest run), if tracked.
-- name: ListListItems :many
SELECT sqlc.embed(items), li.added_at, li.position,
       e.id AS entry_id, e.status AS entry_status, e.score AS entry_score, e.progress AS entry_progress
FROM list_items li
JOIN items ON items.id = li.item_id
LEFT JOIN entries e ON e.item_id = li.item_id AND e.user_id = sqlc.arg(viewer_id)
    AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
WHERE li.list_id = sqlc.arg(list_id)
ORDER BY li.added_at DESC, li.item_id DESC;

-- Appends at the end of the manual order. Returns the row count (0 when already in the list).
-- name: AddListItem :execrows
INSERT OR IGNORE INTO list_items (list_id, item_id, added_at, position)
VALUES (?, ?, ?, (SELECT COALESCE(MAX(p.position), 0) + 1 FROM list_items p WHERE p.list_id = ?));

-- name: RemoveListItem :execrows
DELETE FROM list_items WHERE list_id = ? AND item_id = ?;

-- name: SetListItemPosition :exec
UPDATE list_items SET position = ? WHERE list_id = ? AND item_id = ?;

-- name: ListListItemIDs :many
SELECT item_id FROM list_items WHERE list_id = ? ORDER BY position, added_at, item_id;

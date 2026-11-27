-- name: GetSeries :one
SELECT * FROM series WHERE id = ?;

-- name: GetSeriesByKey :one
SELECT * FROM series WHERE source = ? AND external_id = ?;

-- Another series with the same normalised name (any source), to merge
-- Open Library strings into the series that already exists.
-- name: FindSeriesByNameKey :one
SELECT * FROM series WHERE name_key = ? ORDER BY (source = 'hardcover') DESC, id LIMIT 1;

-- name: UpsertSeries :one
INSERT INTO series (source, external_id, name, name_key, slug, books_count, refreshed_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, external_id) DO UPDATE SET
    name = excluded.name, name_key = excluded.name_key, slug = excluded.slug,
    books_count = excluded.books_count, refreshed_at = excluded.refreshed_at
RETURNING *;

-- Keeps is_primary of an existing row; the item link only ever fills in.
-- name: UpsertSeriesBook :exec
INSERT INTO series_books (series_id, position, item_id, source, external_id, title, image, year, is_primary)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (series_id, source, external_id) DO UPDATE SET
    position = excluded.position, title = excluded.title, image = excluded.image, year = excluded.year,
    item_id = COALESCE(series_books.item_id, excluded.item_id),
    is_primary = CASE WHEN excluded.is_primary = 1 THEN 1 ELSE series_books.is_primary END;

-- Marks which series is the primary one of an item: the given one, none other.
-- name: SetItemPrimarySeries :exec
UPDATE series_books SET is_primary = (series_id = sqlc.arg(series_id))
WHERE item_id = sqlc.arg(item_id);

-- Drops the books a provider no longer lists; keep is a JSON array of external ids.
-- name: DeleteSeriesBooksNotIn :exec
DELETE FROM series_books
WHERE series_id = sqlc.arg(series_id) AND source = sqlc.arg(source)
  AND external_id NOT IN (SELECT value FROM json_each(CAST(sqlc.arg(keep) AS TEXT)));

-- name: LinkSeriesBook :exec
UPDATE series_books SET item_id = ? WHERE item_id IS NULL AND source = ? AND external_id = ?;

-- Links every unlinked series book to the book item that has its id.
-- name: LinkAllSeriesBooks :exec
UPDATE series_books SET item_id = (
    SELECT i.id FROM item_ids x JOIN items i ON i.id = x.item_id AND i.kind = 'book'
    WHERE x.source = series_books.source AND x.external_id = series_books.external_id
    ORDER BY i.id LIMIT 1)
WHERE item_id IS NULL;

-- name: ListSeriesItemIDs :many
SELECT DISTINCT item_id FROM series_books WHERE series_id = ? AND item_id IS NOT NULL;

-- The primary series of an item (the flagged one, else the oldest).
-- name: GetItemPrimarySeries :one
SELECT s.id, s.name, sb.position
FROM series_books sb JOIN series s ON s.id = sb.series_id
WHERE sb.item_id = ?
ORDER BY sb.is_primary DESC, s.id
LIMIT 1;

-- name: UpdateItemMeta :exec
UPDATE items SET meta = ? WHERE id = ?;

-- Every book of a series with the viewer's current entry (highest run) if any.
-- name: ListSeriesBooksForUser :many
SELECT sb.position, sb.item_id, sb.source, sb.external_id, sb.title, sb.image, sb.year,
       e.id AS entry_id, e.status AS entry_status, e.progress AS entry_progress, e.score AS entry_score
FROM series_books sb
LEFT JOIN entries e ON e.user_id = sqlc.arg(user_id) AND e.item_id = sb.item_id
    AND e.run = (SELECT MAX(e2.run) FROM entries e2 WHERE e2.user_id = e.user_id AND e2.item_id = e.item_id)
WHERE sb.series_id = sqlc.arg(series_id)
ORDER BY sb.position, sb.title, sb.external_id;

-- The books of every series in which the viewer tracks at least one book.
-- name: ListUserSeriesBooks :many
SELECT s.id AS series_id, s.name, s.books_count, sb.position, sb.item_id, sb.source, sb.external_id, sb.title, sb.image,
       e.id AS entry_id, e.status AS entry_status
FROM series s
JOIN series_books sb ON sb.series_id = s.id
LEFT JOIN entries e ON e.user_id = sqlc.arg(user_id) AND e.item_id = sb.item_id
    AND e.run = (SELECT MAX(e2.run) FROM entries e2 WHERE e2.user_id = e.user_id AND e2.item_id = e.item_id)
WHERE s.id IN (
    SELECT sb2.series_id FROM series_books sb2
    JOIN entries e3 ON e3.item_id = sb2.item_id AND e3.user_id = sqlc.arg(user_id))
ORDER BY s.name_key, s.id, sb.position, sb.title;

-- Series whose normalised name contains the (LIKE-escaped, lower-cased) text,
-- with how many books of it the viewer tracks.
-- name: SearchSeriesByName :many
SELECT s.id, s.source, s.external_id, s.name, s.books_count,
       (SELECT COUNT(DISTINCT sb.item_id) FROM series_books sb
        JOIN entries e ON e.item_id = sb.item_id AND e.user_id = sqlc.arg(user_id)
        WHERE sb.series_id = s.id) AS tracked
FROM series s
WHERE s.name_key LIKE '%' || sqlc.arg(pattern) || '%' ESCAPE '\'
ORDER BY s.name_key
LIMIT 25;

-- How many books of a series the viewer tracks.
-- name: CountTrackedInSeries :one
SELECT COUNT(DISTINCT sb.item_id) FROM series_books sb
JOIN entries e ON e.item_id = sb.item_id AND e.user_id = ?
WHERE sb.series_id = ?;

-- name: SetItemSeriesCheck :exec
INSERT INTO item_series_checks (item_id, checked_at) VALUES (?, ?)
ON CONFLICT (item_id) DO UPDATE SET checked_at = excluded.checked_at;

-- Hardcover and Open Library books never asked for their series, or asked
-- before the cutoff and found nothing.
-- name: ListBooksNeedingSeries :many
SELECT i.* FROM items i
LEFT JOIN item_series_checks c ON c.item_id = i.id
WHERE i.kind = 'book' AND i.source IN ('hardcover', 'openlibrary')
  AND (c.item_id IS NULL
       OR (c.checked_at < sqlc.arg(cutoff) AND NOT EXISTS (SELECT 1 FROM series_books sb WHERE sb.item_id = i.id)))
ORDER BY (c.item_id IS NULL) DESC, i.id
LIMIT sqlc.arg(max);

-- name: ListStaleSeries :many
SELECT * FROM series WHERE source = 'hardcover' AND refreshed_at < ? ORDER BY refreshed_at LIMIT ?;

-- Claims the unlinked row at a position (a book listed by the provider under
-- another source) for an item, so one book is not listed twice.
-- name: LinkSeriesBookAtPosition :execrows
UPDATE series_books SET item_id = sqlc.arg(item_id)
WHERE series_id = sqlc.arg(series_id) AND item_id IS NULL AND position = sqlc.arg(position);

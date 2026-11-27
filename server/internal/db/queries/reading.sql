-- name: GetReadingDocument :one
SELECT * FROM reading_documents WHERE id = ? AND user_id = ?;

-- name: GetReadingDocumentByHash :one
SELECT * FROM reading_documents WHERE user_id = ? AND document_hash = ?;

-- name: InsertReadingDocument :one
INSERT INTO reading_documents (user_id, document_hash, title, authors, total_pages, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateReadingDocumentMeta :exec
UPDATE reading_documents
SET title = ?, authors = ?, total_pages = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateReadingDocumentProgress :exec
UPDATE reading_documents
SET progress = ?, percentage = ?, device = ?, device_id = ?, progress_updated_at = ?, updated_at = ?
WHERE id = ?;

-- name: SetReadingDocumentItem :exec
UPDATE reading_documents SET item_id = ?, link_checked_at = ?, updated_at = ? WHERE id = ?;

-- name: MarkReadingDocumentChecked :exec
UPDATE reading_documents SET link_checked_at = ? WHERE id = ?;

-- name: SetReadingDocumentIgnored :exec
UPDATE reading_documents SET ignored = ?, updated_at = ? WHERE id = ?;

-- name: SetReadingDocumentMerged :exec
UPDATE reading_documents SET merged_into = ?, updated_at = ? WHERE id = ?;

-- name: RepointMergedDocuments :exec
UPDATE reading_documents SET merged_into = sqlc.arg(new_target) WHERE merged_into = sqlc.arg(old_target);

-- name: ListReadingDocuments :many
SELECT * FROM reading_documents WHERE user_id = ? ORDER BY id DESC;

-- name: ReadingDocumentTotals :many
SELECT document_id,
       CAST(COALESCE(SUM(pages_read), 0) AS INTEGER) AS pages,
       CAST(COALESCE(SUM(duration_seconds), 0) AS INTEGER) AS seconds,
       COUNT(*) AS sessions
FROM reading_sessions
WHERE user_id = ? AND document_id IS NOT NULL
GROUP BY document_id;

-- name: InsertPageEvent :execrows
INSERT INTO page_events (document_id, page, start_time, duration_seconds, total_pages)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (document_id, page, start_time) DO NOTHING;

-- name: ListPageEvents :many
SELECT * FROM page_events WHERE document_id = ? ORDER BY start_time, id;

-- name: DeleteKOReaderSessions :exec
DELETE FROM reading_sessions WHERE document_id = ? AND source = 'koreader';

-- name: InsertReadingSession :one
INSERT INTO reading_sessions (user_id, source, document_id, item_id, title, started_at, ended_at,
                              duration_seconds, pages_read, start_page, end_page, chapters_read, device)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: SetDocumentSessionsItem :exec
UPDATE reading_sessions SET item_id = sqlc.narg(item_id)
WHERE document_id = sqlc.arg(document_id)
   OR document_id IN (SELECT d.id FROM reading_documents d WHERE d.merged_into = sqlc.arg(document_id));

-- name: FirstDocumentSessionStart :one
SELECT started_at FROM reading_sessions
WHERE document_id = sqlc.arg(document_id)
   OR document_id IN (SELECT d.id FROM reading_documents d WHERE d.merged_into = sqlc.arg(document_id))
ORDER BY started_at LIMIT 1;

-- name: LastDocumentSessionEnd :one
SELECT ended_at FROM reading_sessions
WHERE document_id = sqlc.arg(document_id)
   OR document_id IN (SELECT d.id FROM reading_documents d WHERE d.merged_into = sqlc.arg(document_id))
ORDER BY ended_at DESC LIMIT 1;

-- name: ListReadingSessions :many
SELECT sqlc.embed(reading_sessions), CAST(COALESCE(d.merged_into, d.id, 0) AS INTEGER) AS effective_document_id
FROM reading_sessions
LEFT JOIN reading_documents d ON d.id = reading_sessions.document_id
WHERE reading_sessions.user_id = sqlc.arg(user_id)
  AND reading_sessions.started_at >= sqlc.arg(from_ts)
  AND reading_sessions.started_at < sqlc.arg(to_ts)
ORDER BY reading_sessions.started_at DESC, reading_sessions.id DESC
LIMIT sqlc.arg(max_rows);

-- name: ListSessionsForStats :many
SELECT started_at, pages_read, chapters_read, duration_seconds
FROM reading_sessions
WHERE reading_sessions.user_id = ? AND started_at >= ? AND started_at < ?
  AND (document_id IS NULL OR document_id NOT IN (SELECT id FROM reading_documents WHERE ignored = 1));

-- name: ListReadSessionStarts :many
SELECT started_at FROM reading_sessions
WHERE reading_sessions.user_id = ? AND (pages_read > 0 OR chapters_read > 0)
  AND (document_id IS NULL OR document_id NOT IN (SELECT id FROM reading_documents WHERE ignored = 1));

-- name: GetBookNotes :one
SELECT * FROM book_notes WHERE entry_id = ?;

-- name: InsertBookNotes :exec
INSERT INTO book_notes (entry_id, preview, predict, purpose, purpose_note, reflect, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (entry_id) DO NOTHING;

-- name: UpdateBookNotes :exec
UPDATE book_notes
SET preview = ?, predict = ?, purpose = ?, purpose_note = ?, reflect = ?, updated_at = ?
WHERE entry_id = ?;

-- name: UpsertDocumentTOC :exec
INSERT INTO reading_document_toc (document_id, chapters, updated_at) VALUES (?, ?, ?)
ON CONFLICT (document_id) DO UPDATE SET chapters = excluded.chapters, updated_at = excluded.updated_at;

-- name: ListUserTOCs :many
-- Every table of contents of the user's books, newest first, with the item
-- the document (or the document it was merged into) is linked to.
SELECT CAST(COALESCE(r.item_id, d.item_id) AS INTEGER) AS item_id, d.total_pages, t.chapters
FROM reading_document_toc t
JOIN reading_documents d ON d.id = t.document_id
LEFT JOIN reading_documents r ON r.id = d.merged_into
WHERE d.user_id = ? AND COALESCE(r.item_id, d.item_id) IS NOT NULL
ORDER BY t.updated_at DESC;

-- name: ItemDevicePages :one
-- The page count the reader's device uses for a linked book: that of the file
-- (linked to it, or merged into one that is) read most recently.
SELECT d.total_pages FROM reading_documents d
LEFT JOIN reading_documents r ON r.id = d.merged_into
WHERE COALESCE(r.item_id, d.item_id) = CAST(sqlc.arg(item_id) AS INTEGER) AND d.total_pages > 0
ORDER BY COALESCE((SELECT MAX(e.start_time) FROM page_events e WHERE e.document_id = d.id), '') DESC, d.updated_at DESC, d.id DESC
LIMIT 1;

-- name: ListDevicePagedItems :many
SELECT DISTINCT CAST(COALESCE(r.item_id, d.item_id) AS INTEGER) AS item_id FROM reading_documents d
LEFT JOIN reading_documents r ON r.id = d.merged_into
WHERE COALESCE(r.item_id, d.item_id) IS NOT NULL AND d.total_pages > 0;

-- name: FurthestDocumentFraction :one
-- How far into the file reading has reached, 0..1 (-1 with no page events),
-- each page measured against the page count of its own layout.
SELECT CAST(COALESCE(MAX(CAST(e.page AS REAL) / COALESCE(NULLIF(e.total_pages, 0), d.total_pages)), -1) AS REAL) AS fraction
FROM page_events e JOIN reading_documents d ON d.id = e.document_id
WHERE e.document_id = ?;

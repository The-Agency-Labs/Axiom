-- +goose Up
-- Reading: ignored documents (KOReader's Quickstart Guide) are hidden from the
-- unlinked list; link_checked_at marks that auto-linking already ran for a
-- document so it is not retried on every upload.
ALTER TABLE reading_documents ADD COLUMN ignored INTEGER NOT NULL DEFAULT 0;
ALTER TABLE reading_documents ADD COLUMN link_checked_at TEXT;
CREATE INDEX reading_documents_merged ON reading_documents(merged_into);
CREATE INDEX page_events_document ON page_events(document_id, start_time);

-- +goose Down
DROP INDEX page_events_document;
DROP INDEX reading_documents_merged;
ALTER TABLE reading_documents DROP COLUMN link_checked_at;
ALTER TABLE reading_documents DROP COLUMN ignored;

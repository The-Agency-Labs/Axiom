-- +goose Up
-- A book's table of contents as KOReader sees it, sent by the Axiom
-- KOReader plugin while the book is open. chapters is JSON:
-- [{"title": "...", "start": 0.0-1.0, "depth": 1}], start = where the chapter
-- begins as a fraction of the book (independent of font size and edition).
CREATE TABLE reading_document_toc (
    document_id INTEGER PRIMARY KEY REFERENCES reading_documents(id) ON DELETE CASCADE,
    chapters    TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- +goose Down
DROP TABLE reading_document_toc;

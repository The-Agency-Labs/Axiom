-- +goose Up
-- Book series. A series lists every book it has, tracked or not, so the
-- series page can show gaps; item_id links a book to the items table when the
-- user (or anyone) has it.
CREATE TABLE series (
    id           INTEGER PRIMARY KEY,
    source       TEXT NOT NULL,              -- hardcover, or openlibrary for series built from free-text edition strings
    external_id  TEXT NOT NULL,              -- id at the source (openlibrary: the normalised name key)
    name         TEXT NOT NULL,              -- normalised display name ("The Red Rising Trilogy" -> "Red Rising")
    name_key     TEXT NOT NULL,              -- lower-cased comparison key of name, for search and merging
    slug         TEXT NOT NULL DEFAULT '',
    books_count  INTEGER NOT NULL DEFAULT 0, -- what the provider reports
    refreshed_at TEXT NOT NULL,
    UNIQUE (source, external_id)
);
CREATE INDEX series_name_key ON series(name_key);

CREATE TABLE series_books (
    series_id   INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    position    REAL NOT NULL,
    item_id     INTEGER REFERENCES items(id) ON DELETE SET NULL,
    source      TEXT NOT NULL,               -- source of the book's id
    external_id TEXT NOT NULL,
    title       TEXT NOT NULL,
    image       TEXT NOT NULL DEFAULT '',
    year        INTEGER,
    is_primary  INTEGER NOT NULL DEFAULT 0,  -- 1 = this is the primary series of the book (the provider's first)
    PRIMARY KEY (series_id, source, external_id)
);
CREATE INDEX series_books_item ON series_books(item_id);
CREATE INDEX series_books_book ON series_books(source, external_id);

-- When a book was last asked for its series, so books in no series are not
-- asked again every day.
CREATE TABLE item_series_checks (
    item_id    INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    checked_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE item_series_checks;
DROP INDEX series_books_book;
DROP INDEX series_books_item;
DROP TABLE series_books;
DROP INDEX series_name_key;
DROP TABLE series;

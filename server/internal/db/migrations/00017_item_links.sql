-- +goose Up
-- Titles an outside app tracks for a user (it is the source of truth for
-- their progress). Kindle books are derived from reading documents instead.
CREATE TABLE item_links (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source  TEXT NOT NULL, -- jellyfin, paperback, steam
    PRIMARY KEY (user_id, item_id, source)
);

-- +goose Down
DROP TABLE item_links;

-- +goose Up
-- Custom lists were removed from the app (owner doesn't use them); the
-- tables were empty.
DROP TABLE IF EXISTS list_items;
DROP TABLE IF EXISTS list_members;
DROP TABLE IF EXISTS lists;

-- +goose Down
CREATE TABLE lists (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    visibility  TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public'))
);
CREATE TABLE list_members (
    list_id INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (list_id, user_id)
);
CREATE TABLE list_items (
    list_id  INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    item_id  INTEGER NOT NULL REFERENCES items(id),
    added_at TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (list_id, item_id)
);
CREATE INDEX list_items_item ON list_items(item_id);
CREATE INDEX list_members_user ON list_members(user_id);
CREATE INDEX lists_owner ON lists(owner_id);

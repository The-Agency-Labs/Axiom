-- +goose Up
-- Lists: Django only had owner + collaborators; the rewrite adds a public
-- visibility (read-only for every logged-in user) and a manual order.
ALTER TABLE lists ADD COLUMN visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public'));
ALTER TABLE list_items ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
CREATE INDEX list_items_item ON list_items(item_id);
CREATE INDEX list_members_user ON list_members(user_id);
CREATE INDEX lists_owner ON lists(owner_id);

-- +goose Down
DROP INDEX lists_owner;
DROP INDEX list_members_user;
DROP INDEX list_items_item;
ALTER TABLE list_items DROP COLUMN position;
ALTER TABLE lists DROP COLUMN visibility;

-- +goose Up
-- Phase 3 additions.
--
-- watches.notes: Django kept a note per watched episode and the API returns it.
-- item_episodes.kind/score: the anime episode list shows TMDB's episode type
--   (standard, finale, ...) and rating, which the base table had no room for.
-- items.seasons_synced_at: seasons/episodes refresh at most every 24 h; items.refreshed_at
--   is the metadata refresh and moves independently.
-- Indexes: items by kind (library listings and the credits/releases backfills),
--   entries by item (who tracks an item), entry_seasons by status (finding dropped
--   seasons for the calendar), credit_fetches stays keyed by item.
ALTER TABLE watches ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE item_episodes ADD COLUMN kind TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE item_episodes ADD COLUMN score REAL;
ALTER TABLE items ADD COLUMN seasons_synced_at TEXT;

CREATE INDEX items_kind ON items(kind);
CREATE INDEX entries_item ON entries(item_id);
CREATE INDEX entry_seasons_status ON entry_seasons(entry_id, status);
CREATE INDEX releases_item ON releases(item_id);

-- +goose Down
DROP INDEX releases_item;
DROP INDEX entry_seasons_status;
DROP INDEX entries_item;
DROP INDEX items_kind;
ALTER TABLE items DROP COLUMN seasons_synced_at;
ALTER TABLE item_episodes DROP COLUMN score;
ALTER TABLE item_episodes DROP COLUMN kind;
ALTER TABLE watches DROP COLUMN notes;

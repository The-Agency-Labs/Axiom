-- +goose Up
-- Shows (tv and anime) are sourced from TheTVDB: its aired order is the one
-- Jellyfin numbers by, and it has the real seasons TMDB lumps together.
--
-- item_episodes.ext_id: the TVDB episode id of an episode of a TVDB show ('' for
--   TMDB shows), so a Jellyfin play identifies its episode without a lookup and
--   renumbering on TVDB cannot misplace it.
-- anime_mappings.tvdb_season / tvdb_offset: where each AniList/MAL cour sits in its
--   TVDB series (season 0 = specials; offset = episodes before the cour in that
--   season). The Fribb dataset carries them; the table is emptied so the next
--   mapping load (the first lookup, or the daily job) fills the new columns.
ALTER TABLE item_episodes ADD COLUMN ext_id TEXT NOT NULL DEFAULT '';
CREATE INDEX item_episodes_ext ON item_episodes(ext_id) WHERE ext_id != '';

ALTER TABLE anime_mappings ADD COLUMN tvdb_season INTEGER;
ALTER TABLE anime_mappings ADD COLUMN tvdb_offset INTEGER NOT NULL DEFAULT 0;
CREATE INDEX anime_mappings_tvdb ON anime_mappings(tvdb_id);
DELETE FROM anime_mappings;
DELETE FROM kv WHERE key = 'catalog:anime_mappings_refreshed';

-- +goose Down
DROP INDEX anime_mappings_tvdb;
ALTER TABLE anime_mappings DROP COLUMN tvdb_offset;
ALTER TABLE anime_mappings DROP COLUMN tvdb_season;
DROP INDEX item_episodes_ext;
ALTER TABLE item_episodes DROP COLUMN ext_id;

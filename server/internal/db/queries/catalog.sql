-- name: GetItem :one
SELECT * FROM items WHERE id = ?;

-- name: GetItemByKey :one
SELECT * FROM items WHERE source = ? AND external_id = ? AND kind = ?;

-- name: InsertItem :one
INSERT INTO items (kind, source, external_id, title, native_title, image, year, synopsis, total, kind_override, meta, refreshed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateItemMetadata :exec
UPDATE items
SET title = ?, native_title = ?, image = ?, year = ?, synopsis = ?, total = ?, meta = ?, refreshed_at = ?
WHERE id = ?;

-- Reclassification by the anime mapping (tv <-> anime, movie <-> anime_movie); never touches an overridden kind.
-- name: UpdateItemKindAuto :exec
UPDATE items SET kind = ? WHERE id = ? AND kind_override = 0;

-- name: UpdateItemTotal :exec
UPDATE items SET total = ? WHERE id = ?;

-- name: MarkSeasonsSynced :exec
UPDATE items SET seasons_synced_at = ? WHERE id = ?;

-- name: UpsertItemID :exec
INSERT INTO item_ids (item_id, source, external_id) VALUES (?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListItemIDs :many
SELECT * FROM item_ids WHERE item_id = ? ORDER BY source, external_id;

-- name: FindItemsByExternalID :many
SELECT i.* FROM items i
JOIN item_ids x ON x.item_id = i.id
WHERE x.source = ? AND x.external_id = ?
ORDER BY (i.source = x.source) DESC, i.id;

-- name: DeleteItemSeasons :exec
DELETE FROM item_seasons WHERE item_id = ?;

-- name: DeleteItemEpisodes :exec
DELETE FROM item_episodes WHERE item_id = ?;

-- name: InsertItemSeason :exec
INSERT INTO item_seasons (item_id, number, title, image, air_date, episode_count)
VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertItemEpisode :exec
INSERT INTO item_episodes (item_id, season, number, position, title, air_date, runtime, image, overview, kind, score, ext_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (item_id, season, number) DO UPDATE SET
    position = excluded.position, title = excluded.title, air_date = excluded.air_date,
    runtime = excluded.runtime, image = excluded.image, overview = excluded.overview,
    kind = excluded.kind, score = excluded.score, ext_id = excluded.ext_id;

-- name: ListItemSeasons :many
SELECT * FROM item_seasons WHERE item_id = ? ORDER BY number;

-- name: ListItemEpisodes :many
SELECT * FROM item_episodes WHERE item_id = ? ORDER BY season, position;

-- name: ListSeasonEpisodes :many
SELECT * FROM item_episodes WHERE item_id = ? AND season = ? ORDER BY position;

-- name: UpsertPerson :one
INSERT INTO people (source, external_id, name, native_name, image) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (source, external_id) DO UPDATE SET
    name = excluded.name,
    native_name = CASE WHEN excluded.native_name != '' THEN excluded.native_name ELSE people.native_name END,
    image = CASE WHEN excluded.image != '' THEN excluded.image ELSE people.image END
RETURNING id;

-- name: DeleteItemCredits :exec
DELETE FROM credits WHERE item_id = ?;

-- name: InsertCredit :exec
INSERT INTO credits (item_id, person_id, role, character, character_image, character_role, language, job, ord, episode_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpsertCreditFetch :exec
INSERT INTO credit_fetches (item_id, fetched_at, count, error) VALUES (?, ?, ?, ?)
ON CONFLICT (item_id) DO UPDATE SET fetched_at = excluded.fetched_at, count = excluded.count, error = excluded.error;

-- name: GetCreditFetch :one
SELECT * FROM credit_fetches WHERE item_id = ?;

-- Library items whose credits were never fetched, or whose last fetch failed before retry_before
-- (tv, movie and anime kinds only).
-- name: ListItemsMissingCredits :many
SELECT i.* FROM items i
WHERE i.id IN (SELECT item_id FROM entries)
  AND i.kind IN ('tv', 'movie', 'anime', 'anime_movie')
  AND NOT EXISTS (
        SELECT 1 FROM credit_fetches f
        WHERE f.item_id = i.id AND (f.error = '' OR f.fetched_at > sqlc.arg(retry_before)))
ORDER BY i.id
LIMIT sqlc.arg(max_rows);

-- name: ListItemCredits :many
SELECT c.id, c.item_id, c.person_id, c.role, c.character, c.character_image, c.character_role,
       c.language, c.job, c.ord, c.episode_count,
       p.source AS person_source, p.external_id AS person_external_id, p.name AS person_name,
       p.native_name AS person_native_name, p.image AS person_image
FROM credits c JOIN people p ON p.id = c.person_id
WHERE c.item_id = ?
ORDER BY c.ord, c.id;

-- name: DeleteItemReleases :exec
DELETE FROM releases WHERE item_id = ?;

-- name: InsertRelease :exec
INSERT INTO releases (item_id, season, number, at) VALUES (?, ?, ?, ?)
ON CONFLICT (item_id, season, number) DO UPDATE SET at = excluded.at;

-- name: ListItemReleases :many
SELECT * FROM releases WHERE item_id = ? ORDER BY at, season, number;

-- Items that are tracked by anyone and not manually entered: the set the release job refreshes.
-- name: ListTrackedItems :many
SELECT * FROM items
WHERE source != 'manual' AND id IN (SELECT item_id FROM entries)
ORDER BY id;

-- Upcoming releases of what a user is actively tracking: Paused/Dropped entries are skipped, and
-- so are the seasons of a show from the first Paused/Dropped season on.
-- name: ListReleasesForUser :many
SELECT r.id, r.item_id, r.season, r.number, r.at,
       i.kind, i.title, i.image, e.id AS entry_id, e.status AS entry_status
FROM releases r
JOIN items i ON i.id = r.item_id
JOIN entries e ON e.item_id = r.item_id AND e.user_id = sqlc.arg(user_id)
    AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
WHERE r.at >= sqlc.arg(from_at) AND r.at <= sqlc.arg(to_at)
  AND e.status NOT IN ('Paused', 'Dropped')
  AND (r.season IS NULL OR r.season < COALESCE(
        (SELECT MIN(s.season) FROM entry_seasons s
          WHERE s.entry_id = e.id AND s.status IN ('Paused', 'Dropped') AND s.season > 0),
        1000000))
ORDER BY r.at, r.item_id, r.season, r.number;

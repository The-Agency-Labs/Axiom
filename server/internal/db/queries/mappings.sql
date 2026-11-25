-- name: DeleteAnimeMappings :exec
DELETE FROM anime_mappings;

-- Inserts a batch of rows given as a JSON array of objects with the column names as keys.
-- name: InsertAnimeMappingBatch :exec
INSERT INTO anime_mappings (mal_id, anilist_id, kind, tmdb_tv, tmdb_movie, tmdb_season, tmdb_offset, tvdb_id, tvdb_season, tvdb_offset)
SELECT
    json_extract(j.value, '$.mal_id'),
    json_extract(j.value, '$.anilist_id'),
    COALESCE(json_extract(j.value, '$.kind'), ''),
    json_extract(j.value, '$.tmdb_tv'),
    json_extract(j.value, '$.tmdb_movie'),
    json_extract(j.value, '$.tmdb_season'),
    COALESCE(json_extract(j.value, '$.tmdb_offset'), 0),
    json_extract(j.value, '$.tvdb_id'),
    json_extract(j.value, '$.tvdb_season'),
    COALESCE(json_extract(j.value, '$.tvdb_offset'), 0)
FROM json_each(CAST(sqlc.arg(rows) AS TEXT)) AS j;

-- name: AnimeMappingsExist :one
SELECT EXISTS (SELECT 1 FROM anime_mappings);

-- name: GetAnimeMappingByMAL :one
SELECT * FROM anime_mappings WHERE mal_id = ? ORDER BY id LIMIT 1;

-- name: GetAnimeMappingByAniList :one
SELECT * FROM anime_mappings WHERE anilist_id = ? ORDER BY id LIMIT 1;

-- name: ListAnimeMappingsByTMDBTV :many
SELECT * FROM anime_mappings WHERE tmdb_tv = ? ORDER BY id;

-- name: ListAnimeMappingsByTMDBMovie :many
SELECT * FROM anime_mappings WHERE tmdb_movie = ? ORDER BY id;

-- name: ListAnimeMappingsByTVDB :many
SELECT * FROM anime_mappings WHERE tvdb_id = ? ORDER BY id;

-- name: ListAnimeTMDBIDs :many
SELECT tmdb_tv, tmdb_movie FROM anime_mappings WHERE tmdb_tv IS NOT NULL OR tmdb_movie IS NOT NULL;

-- name: ListAnimeTVDBIDs :many
SELECT DISTINCT tvdb_id FROM anime_mappings WHERE tvdb_id IS NOT NULL AND tvdb_id != 0;

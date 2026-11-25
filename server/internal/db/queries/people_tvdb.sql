-- A person recognised by an id another database shares: reuse the record, keep its name, fill a missing image.
-- name: EnsurePerson :one
INSERT INTO people (source, external_id, name, native_name, image) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (source, external_id) DO UPDATE SET
    image = CASE WHEN people.image = '' THEN excluded.image ELSE people.image END
RETURNING id;

-- name: FindPersonBySource :one
SELECT id FROM people WHERE source = ? AND external_id = ?;

-- Credits of a person on anything but a TMDB show (a film, or a show already on TVDB).
-- name: CountPersonCreditsOutsideTMDBShows :one
SELECT COUNT(*) FROM credits c
JOIN people p ON p.id = c.person_id
JOIN items i ON i.id = c.item_id
WHERE p.source = ? AND p.external_id = ? AND NOT (i.source = 'tmdb' AND i.kind IN ('tv', 'anime'));

-- People no credit refers to any more.
-- name: DeleteOrphanPeople :execrows
DELETE FROM people WHERE id NOT IN (SELECT person_id FROM credits);

-- name: CountPeople :one
SELECT COUNT(*) FROM people;

-- The episode of a TVDB show with this TVDB episode id.
-- name: FindEpisodeByExtID :one
SELECT * FROM item_episodes WHERE ext_id = ? AND ext_id != '' LIMIT 1;

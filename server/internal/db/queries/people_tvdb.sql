-- A person recognised by an id another database shares: reuse the record, keep its name, fill a missing image.
-- name: EnsurePerson :one
INSERT INTO people (source, external_id, name, native_name, image) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (source, external_id) DO UPDATE SET
    image = CASE WHEN people.image = '' THEN excluded.image ELSE people.image END
RETURNING id;

-- Credits of a person on anything but a TMDB show (a film, or a show already on TVDB).
-- People no credit refers to any more.
-- The episode of a TVDB show with this TVDB episode id.
-- name: FindEpisodeByExtID :one
SELECT * FROM item_episodes WHERE ext_id = ? AND ext_id != '' LIMIT 1;

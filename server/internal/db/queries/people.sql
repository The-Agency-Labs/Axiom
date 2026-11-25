-- People and credits API. "Library" = items whose latest-run entry for the user is
-- not Planning, restricted to the kinds that have cast (tv, movie, anime, anime_movie).

-- Latest-run entry of every cast-bearing item the user tracks (any status), with
-- whether its credits were ever fetched.
-- name: ListPeopleEntries :many
SELECT e.id AS entry_id, e.status, e.score,
       i.id AS item_id, i.kind, i.source, i.external_id, i.title, i.image,
       EXISTS (SELECT 1 FROM credit_fetches f WHERE f.item_id = i.id) AS fetched
FROM entries e
JOIN items i ON i.id = e.item_id
WHERE e.user_id = sqlc.arg(user_id)
  AND i.kind IN ('tv', 'movie', 'anime', 'anime_movie')
  AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
ORDER BY i.id;

-- People credited on at least min_titles distinct library titles, most titles first.
-- role and language filters are optional ('' = any).
-- name: ListTopPeople :many
SELECT c.person_id, COUNT(DISTINCT c.item_id) AS titles
FROM credits c
JOIN people p ON p.id = c.person_id
WHERE c.item_id IN (
        SELECT e.item_id FROM entries e JOIN items i ON i.id = e.item_id
        WHERE e.user_id = sqlc.arg(user_id) AND e.status != 'Planning'
          AND i.kind IN ('tv', 'movie', 'anime', 'anime_movie')
          AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id))
  AND (sqlc.arg(role) = '' OR c.role = sqlc.arg(role))
  AND (sqlc.arg(language) = '' OR c.language = sqlc.arg(language))
GROUP BY c.person_id
HAVING COUNT(DISTINCT c.item_id) >= sqlc.arg(min_titles)
ORDER BY titles DESC, p.name, p.id
LIMIT sqlc.arg(max_rows);

-- name: ListPeopleByIDs :many
SELECT * FROM people WHERE id IN (sqlc.slice('ids'));

-- Credits of the given people (all items); callers filter to the library in Go.
-- name: ListCreditsForPeople :many
SELECT c.person_id, c.item_id, c.role, c.character, c.character_image, c.language, c.job
FROM credits c
WHERE c.person_id IN (sqlc.slice('ids'))
ORDER BY c.ord, c.id;

-- name: GetPerson :one
SELECT * FROM people WHERE id = ?;

-- One person's credits with the item they belong to.
-- name: ListPersonCredits :many
SELECT c.role, c.character, c.character_image, c.language, c.job,
       i.id AS item_id, i.kind, i.source, i.external_id, i.title, i.image
FROM credits c JOIN items i ON i.id = c.item_id
WHERE c.person_id = ?
ORDER BY c.ord, c.id;

-- name: SearchPeopleByName :many
SELECT * FROM people WHERE name LIKE sqlc.arg(pattern) ESCAPE '\' ORDER BY name, id LIMIT 30;

-- Releases for the calendar feed of what a user is actively tracking: Paused/Dropped entries are
-- skipped (and a show's seasons from its first Paused/Dropped season on), media types the user
-- turned off are omitted. Episode titles come from item_episodes.
-- name: ListCalendarReleases :many
SELECT r.item_id, r.season, r.number, r.at,
       i.kind, i.title, i.image, i.source, i.external_id,
       COALESCE(ie.title, '') AS episode_title,
       CAST(COALESCE(json_extract(i.meta, '$.backdrop'), '') AS TEXT) AS backdrop
FROM releases r
JOIN items i ON i.id = r.item_id
JOIN entries e ON e.item_id = r.item_id AND e.user_id = sqlc.arg(user_id)
    AND e.run = (SELECT MAX(run) FROM entries WHERE user_id = e.user_id AND item_id = e.item_id)
LEFT JOIN user_media_settings ums ON ums.user_id = e.user_id AND ums.kind = i.kind
LEFT JOIN item_episodes ie ON ie.item_id = r.item_id AND ie.season = r.season AND ie.number = r.number
WHERE r.at >= sqlc.arg(from_at) AND r.at <= sqlc.arg(to_at)
  AND COALESCE(ums.enabled, 1) = 1
  AND e.status NOT IN ('Paused', 'Dropped')
  AND (r.season IS NULL OR r.season < COALESCE(
        (SELECT MIN(s.season) FROM entry_seasons s
          WHERE s.entry_id = e.id AND s.status IN ('Paused', 'Dropped') AND s.season > 0),
        1000000))
ORDER BY r.at, r.item_id, r.season, r.number;

-- +goose Up
-- A show still airing (or on hiatus, or with more announced) isn't completed
-- just because every aired episode is watched: it is In progress (caught up).
-- Its completed seasons keep their own status and dates.
UPDATE entries SET status = 'In progress', finished_at = NULL
WHERE status = 'Completed'
  AND item_id IN (
    SELECT id FROM items
    WHERE kind IN ('tv', 'anime')
      AND lower(json_extract(meta, '$.details.status')) IN
          ('continuing', 'returning series', 'currently airing', 'airing', 'releasing',
           'on hiatus', 'hiatus', 'upcoming', 'not yet aired', 'in production', 'planned'));

-- +goose Down
SELECT 1;

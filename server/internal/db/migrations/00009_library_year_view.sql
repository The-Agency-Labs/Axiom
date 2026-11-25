-- +goose Up
-- The library opens on finished titles grouped by the year they were
-- finished; games (no status) on every game grouped by the year added.
UPDATE user_media_settings SET sort = 'year', status = CASE WHEN kind = 'game' THEN 'All' ELSE 'Completed' END;

-- +goose Down
UPDATE user_media_settings SET sort = 'activity', status = 'In progress' WHERE sort = 'year';

-- +goose Up
-- Board games, like games, have no status: their library lists all of them by year added.
UPDATE user_media_settings SET status = 'All' WHERE kind = 'boardgame';

-- +goose Down
SELECT 1;

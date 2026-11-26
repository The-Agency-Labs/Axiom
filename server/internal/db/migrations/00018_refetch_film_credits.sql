-- +goose Up
-- Anime films get more character pictures now: fetch their cast again.
DELETE FROM credit_fetches WHERE item_id IN (SELECT id FROM items WHERE kind = 'anime_movie');

-- +goose Down
-- Nothing to restore: the casts are fetched again on demand.

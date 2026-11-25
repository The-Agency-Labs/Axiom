-- +goose Up
-- The calendar feed gets its own token, so sharing or rotating the calendar
-- URL never exposes or breaks the Jellyfin webhook URL. Existing users keep
-- their current calendar URL (Django used one token for both); rotating
-- either one now leaves the other alone.
ALTER TABLE users ADD COLUMN calendar_token TEXT;
UPDATE users SET calendar_token = webhook_token;
CREATE UNIQUE INDEX users_calendar_token ON users(calendar_token);

-- +goose Down
DROP INDEX users_calendar_token;
ALTER TABLE users DROP COLUMN calendar_token;

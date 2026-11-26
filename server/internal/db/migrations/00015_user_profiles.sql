-- +goose Up
-- Optional display name and profile picture. A separate table so the users
-- row (and its generated queries) stay as they are. The avatar is a small
-- data URL the browser has already resized.
CREATE TABLE user_profiles (
    user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL DEFAULT '',
    avatar       TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE user_profiles;

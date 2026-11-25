-- +goose Up
-- One-page KOReader sessions are accidental opens, not reading; new uploads
-- no longer create them (reading.MinSessionPages). Page events are kept.
DELETE FROM reading_sessions WHERE source = 'koreader' AND pages_read < 2;

-- +goose Down
-- Sessions are rebuilt from page events on the next upload; nothing to restore here.
SELECT 1;

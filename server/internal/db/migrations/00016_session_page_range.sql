-- +goose Up
-- KOReader sessions stored the first and last page turned to; make the range
-- the lowest and highest page of the session, from its page events.
UPDATE reading_sessions SET
    start_page = COALESCE((SELECT MIN(e.page) FROM page_events e WHERE e.document_id = reading_sessions.document_id
        AND e.start_time >= reading_sessions.started_at AND e.start_time <= reading_sessions.ended_at), start_page),
    end_page = COALESCE((SELECT MAX(e.page) FROM page_events e WHERE e.document_id = reading_sessions.document_id
        AND e.start_time >= reading_sessions.started_at AND e.start_time <= reading_sessions.ended_at), end_page)
WHERE source = 'koreader';

-- +goose Down
-- Nothing to restore: the old values are derivable from page_events as well.

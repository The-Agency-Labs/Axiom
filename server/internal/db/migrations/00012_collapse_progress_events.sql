-- +goose Up
-- Unmarking a season used to log one progress change per episode, all at the
-- same moment. Collapse each such burst (same entry, same timestamp) into one
-- row going from the first old value to the last new value.
UPDATE entry_events
SET new = (SELECT e2.new FROM entry_events e2
           WHERE e2.entry_id = entry_events.entry_id AND e2.at = entry_events.at AND e2.field = 'progress'
           ORDER BY e2.id DESC LIMIT 1)
WHERE field = 'progress'
  AND id = (SELECT MIN(e3.id) FROM entry_events e3
            WHERE e3.entry_id = entry_events.entry_id AND e3.at = entry_events.at AND e3.field = 'progress');

DELETE FROM entry_events
WHERE field = 'progress'
  AND id <> (SELECT MIN(e3.id) FROM entry_events e3
             WHERE e3.entry_id = entry_events.entry_id AND e3.at = entry_events.at AND e3.field = 'progress');

-- +goose Down
SELECT 1;

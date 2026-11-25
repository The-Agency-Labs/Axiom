package migrate

import (
	"database/sql"
	"fmt"
	"strconv"
)

// history turns django-simple-history rows into entry_events, so the activity
// feed and the year views (which read progress changes) keep what Django
// recorded. Only the live row's own history is used: a "+" row is the
// creation (a "created" event with the status, then the progress and score it
// started with, shown as "Read up to chapter N" / "Rated N/10" like Django's
// journal), a "~" row yields one event per changed status, progress or score,
// and "-" rows (deleted entries) are dropped with their entry.
//
// Only rows attributed to a user are copied: Django's activity feed showed
// exactly those (history_user_id set), and the rows without one come from
// bulk imports that stamp the whole list with the import time, which would
// flood the feed (Options.AllHistory copies them too).
//
// Season and episode history is not copied: plays carry their own dates, and
// the season rows keep no history in the Go schema.
func (m *migrator) history() error {
	tables := []struct{ live, hist string }{{"app_tv", "app_historicaltv"}, {"app_movie", "app_historicalmovie"}}
	for _, t := range countTables {
		tables = append(tables, struct{ live, hist string }{t.table, t.hist})
	}
	for _, t := range tables {
		if !hasTable(m.ctx, m.opts.Source, t.hist) {
			continue
		}
		rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM `+t.hist+` ORDER BY id, history_date, history_id`)
		if err != nil {
			return err
		}
		type state struct {
			status   string
			progress int64
			score    sql.NullFloat64
			seen     bool
		}
		prev := map[int64]state{}
		for _, r := range rows {
			entry, ok := m.entryBy[t.live+":"+strconv.FormatInt(r.i64("id"), 10)]
			if !ok {
				continue
			}
			at := r.stamp("history_date")
			if !at.Valid {
				continue
			}
			if r.null("history_user_id") && !m.opts.AllHistory {
				m.rep.count("history rows skipped (no user)", 1)
				continue
			}
			cur := state{status: r.str("status"), progress: r.i64("progress"), score: r.nullFloat("score"), seen: true}
			old := prev[r.i64("id")]
			prev[r.i64("id")] = cur
			var evs []event
			switch r.str("history_type") {
			case "+":
				evs = append(evs, event{"created", sql.NullString{}, nstr(cur.status)})
				if cur.progress > 0 {
					evs = append(evs, event{"progress", sql.NullString{}, nstr(strconv.FormatInt(cur.progress, 10))})
				}
				if cur.score.Valid {
					evs = append(evs, event{"score", sql.NullString{}, fmtScore(cur.score)})
				}
			case "~":
				if !old.seen {
					old = state{status: cur.status}
				}
				if cur.status != old.status {
					evs = append(evs, event{"status", nstr(old.status), nstr(cur.status)})
				}
				if cur.progress != old.progress {
					evs = append(evs, event{"progress", nstr(strconv.FormatInt(old.progress, 10)), nstr(strconv.FormatInt(cur.progress, 10))})
				}
				if cur.score != old.score {
					evs = append(evs, event{"score", fmtScore(old.score), fmtScore(cur.score)})
				}
			default:
				continue
			}
			for _, e := range evs {
				if _, err := m.exec(`INSERT INTO entry_events (entry_id, at, field, old, new) VALUES (?, ?, ?, ?, ?)`,
					entry, at.String, e.field, e.old, e.new); err != nil {
					return fmt.Errorf("%s: %w", t.hist, err)
				}
				m.rep.count("entry events", 1)
			}
		}
	}
	return nil
}

type event struct {
	field    string
	old, new sql.NullString
}

func fmtScore(s sql.NullFloat64) sql.NullString {
	if !s.Valid {
		return sql.NullString{}
	}
	return sql.NullString{String: strconv.FormatFloat(s.Float64, 'f', -1, 64), Valid: true}
}

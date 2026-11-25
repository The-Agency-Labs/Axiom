package migrate

import (
	"database/sql"
	"fmt"
)

// reading copies documents, page events and sessions. Their ids are kept
// (the SPA and KOReader plugin may hold them); the item link follows the item
// map and is dropped when the item was not migrated.
func (m *migrator) reading() error {
	if !hasTable(m.ctx, m.opts.Source, "reading_readingdocument") {
		return nil
	}
	docs, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM reading_readingdocument ORDER BY id`)
	if err != nil {
		return err
	}
	docMap := map[int64]int64{}
	now := stampOf(m.opts.Now())
	itemRef := func(v sql.NullInt64) sql.NullInt64 {
		if v.Valid {
			if id, ok := m.itemMap[v.Int64]; ok {
				return sql.NullInt64{Int64: id, Valid: true}
			}
			if id, ok := m.showOf[v.Int64]; ok {
				return sql.NullInt64{Int64: id, Valid: true}
			}
		}
		return sql.NullInt64{}
	}
	for _, r := range docs {
		uid, ok := m.userMap[r.i64("user_id")]
		if !ok {
			continue
		}
		created, updated := r.stamp("created_at"), r.stamp("updated_at")
		if !created.Valid {
			created = nstr(now)
		}
		if !updated.Valid {
			updated = created
		}
		id, err := m.insertID(`INSERT INTO reading_documents (id, user_id, document_hash, title, authors, total_pages, item_id, progress, percentage,
			device, device_id, progress_updated_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.i64("id"), uid, r.str("document_hash"), r.str("title"), r.str("authors"), r.nullInt("total_pages"), itemRef(r.nullInt("item_id")),
			r.str("progress"), r.nullFloat("percentage"), r.str("device"), r.str("device_id"), r.stamp("progress_updated_at"),
			created.String, updated.String)
		if err != nil {
			return fmt.Errorf("reading document %d: %w", r.i64("id"), err)
		}
		docMap[r.i64("id")] = id
		m.rep.count("reading documents", 1)
	}

	if hasTable(m.ctx, m.opts.Source, "reading_pageevent") {
		events, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM reading_pageevent ORDER BY id`)
		if err != nil {
			return err
		}
		for _, r := range events {
			doc, ok := docMap[r.i64("document_id")]
			start := r.stamp("start_time")
			if !ok || !start.Valid {
				continue
			}
			res, err := m.exec(`INSERT OR IGNORE INTO page_events (id, document_id, page, start_time, duration_seconds, total_pages) VALUES (?, ?, ?, ?, ?, ?)`,
				r.i64("id"), doc, r.i64("page"), start.String, r.i64("duration_seconds"), r.nullInt("total_pages"))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				m.rep.count("page events", 1)
			}
		}
	}

	if hasTable(m.ctx, m.opts.Source, "reading_readingsession") {
		sessions, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM reading_readingsession ORDER BY id`)
		if err != nil {
			return err
		}
		for _, r := range sessions {
			uid, ok := m.userMap[r.i64("user_id")]
			start, end := r.stamp("started_at"), r.stamp("ended_at")
			if !ok || !start.Valid || !end.Valid {
				continue
			}
			var doc sql.NullInt64
			if d, ok := docMap[r.i64("document_id")]; ok && !r.null("document_id") {
				doc = sql.NullInt64{Int64: d, Valid: true}
			}
			if _, err := m.exec(`INSERT INTO reading_sessions (id, user_id, source, document_id, item_id, title, started_at, ended_at, duration_seconds,
				pages_read, start_page, end_page, chapters_read, device) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.i64("id"), uid, r.str("source"), doc, itemRef(r.nullInt("item_id")), r.str("title"), start.String, end.String, r.i64("duration_seconds"),
				r.i64("pages_read"), r.nullInt("start_page"), r.nullInt("end_page"), r.i64("chapters_read"), r.str("device")); err != nil {
				return err
			}
			m.rep.count("reading sessions", 1)
		}
	}
	return nil
}

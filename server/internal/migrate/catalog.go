package migrate

import (
	"database/sql"
	"fmt"
)

// catalog copies people, credits and credit fetch markers: it is the cheaper
// correct option, because Django already holds them and refetching would
// cost one TMDB/AniList call per title. Item ids are kept, so credits follow
// their items and person ids are kept; credits of items that were not migrated are dropped.
func (m *migrator) catalog() error {
	if !hasTable(m.ctx, m.opts.Source, "catalog_person") {
		return nil
	}
	people, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM catalog_person ORDER BY id`)
	if err != nil {
		return err
	}
	personMap := map[int64]int64{}
	for _, r := range people {
		id := r.i64("id")
		_, err := m.exec(`INSERT INTO people (id, source, external_id, name, native_name, image) VALUES (?, ?, ?, ?, ?, ?)`,
			id, r.str("source"), r.str("external_id"), r.str("name"), r.str("native_name"), r.str("image"))
		if err != nil {
			return fmt.Errorf("person %d: %w", r.i64("id"), err)
		}
		personMap[r.i64("id")] = id
		m.rep.count("people", 1)
	}
	if hasTable(m.ctx, m.opts.Source, "catalog_credit") {
		credits, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM catalog_credit ORDER BY id`)
		if err != nil {
			return err
		}
		for _, r := range credits {
			item, okI := m.itemMap[r.i64("item_id")]
			person, okP := personMap[r.i64("person_id")]
			if !okI || !okP {
				continue
			}
			if _, err := m.exec(`INSERT INTO credits (item_id, person_id, role, character, character_image, character_role, language, job, ord, episode_count)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				item, person, r.str("role"), r.str("character"), r.str("character_image"), r.str("character_role"),
				r.str("language"), r.str("job"), r.i64("order"), r.nullInt("episode_count")); err != nil {
				return fmt.Errorf("credit %d: %w", r.i64("id"), err)
			}
			m.rep.count("credits", 1)
		}
	}
	if hasTable(m.ctx, m.opts.Source, "catalog_creditfetch") {
		fetches, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM catalog_creditfetch ORDER BY id`)
		if err != nil {
			return err
		}
		for _, r := range fetches {
			item, ok := m.itemMap[r.i64("item_id")]
			at := r.stamp("fetched_at")
			if !ok || !at.Valid {
				continue
			}
			if _, err := m.exec(`INSERT OR REPLACE INTO credit_fetches (item_id, fetched_at, count, error) VALUES (?, ?, ?, ?)`,
				item, at.String, r.i64("count"), r.str("error")); err != nil {
				return err
			}
			m.rep.count("credit fetches", 1)
		}
	}
	return nil
}

// lists copies custom lists with their collaborators and items. A season or
// episode item on a list becomes its show.
func (m *migrator) lists() error {
	if !hasTable(m.ctx, m.opts.Source, "lists_customlist") {
		return nil
	}
	lists, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM lists_customlist ORDER BY id`)
	if err != nil {
		return err
	}
	now := stampOf(m.opts.Now())
	listMap := map[int64]int64{}
	for _, r := range lists {
		owner, ok := m.userMap[r.i64("owner_id")]
		if !ok {
			continue
		}
		id, err := m.insertID(`INSERT INTO lists (owner_id, name, description, visibility, created_at) VALUES (?, ?, ?, 'private', ?)`,
			owner, r.str("name"), r.str("description"), now)
		if err != nil {
			return err
		}
		listMap[r.i64("id")] = id
		m.rep.count("lists", 1)
	}
	if hasTable(m.ctx, m.opts.Source, "lists_customlist_collaborators") {
		rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM lists_customlist_collaborators ORDER BY id`)
		if err != nil {
			return err
		}
		for _, r := range rows {
			list, okL := listMap[r.i64("customlist_id")]
			user, okU := m.userMap[r.i64("user_id")]
			if !okL || !okU {
				continue
			}
			if _, err := m.exec(`INSERT OR IGNORE INTO list_members (list_id, user_id) VALUES (?, ?)`, list, user); err != nil {
				return err
			}
			m.rep.count("list members", 1)
		}
	}
	if hasTable(m.ctx, m.opts.Source, "lists_customlistitem") {
		rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM lists_customlistitem ORDER BY date_added, id`)
		if err != nil {
			return err
		}
		pos := map[int64]int64{}
		for _, r := range rows {
			list, ok := listMap[r.i64("custom_list_id")]
			if !ok {
				continue
			}
			item, ok := m.itemMap[r.i64("item_id")]
			if !ok {
				item, ok = m.showOf[r.i64("item_id")]
			}
			if !ok {
				m.rep.warn("list item %d points at an unmigrated item; skipped", r.i64("id"))
				continue
			}
			added := r.stamp("date_added")
			if !added.Valid {
				added = sql.NullString{String: now, Valid: true}
			}
			res, err := m.exec(`INSERT OR IGNORE INTO list_items (list_id, item_id, added_at, position) VALUES (?, ?, ?, ?)`, list, item, added.String, pos[list])
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				pos[list]++
				m.rep.count("list items", 1)
			}
		}
	}
	return nil
}

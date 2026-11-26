package migrate

import (
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

package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// kindOf maps a Django (media_type, source, media_id) to the Go item kind.
// TMDB shows and films the Fribb mapping lists become anime / anime_movie;
// everything else keeps its type. Django's season and episode items fold into
// their show and have no kind.
func (m *migrator) kindOf(mediaType, source, mediaID string) (string, bool) {
	switch mediaType {
	case "tv":
		if source == "tmdb" {
			if _, ok := m.animeShows[mediaID]; ok {
				return "anime", true
			}
		}
		return "tv", true
	case "movie":
		if source == "tmdb" {
			if _, ok := m.animeFilms[mediaID]; ok {
				return "anime_movie", true
			}
		}
		return "movie", true
	case "anime", "manga", "comic", "book", "game", "boardgame":
		return mediaType, true
	}
	return "", false
}

func (m *migrator) items() error {
	rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM app_item ORDER BY id`)
	if err != nil {
		return err
	}
	type key struct{ mediaID, source string }
	shows := map[key]int64{}
	var folded []row
	for _, r := range rows {
		kind, ok := m.kindOf(r.str("media_type"), r.str("source"), r.str("media_id"))
		if !ok {
			folded = append(folded, r)
			continue
		}
		id := r.i64("id")
		_, err := m.exec(`INSERT INTO items (id, kind, source, external_id, title, image, kind_override, meta)
			VALUES (?, ?, ?, ?, ?, ?, 0, '{}')`,
			id, kind, r.str("source"), r.str("media_id"), r.str("title"), r.str("image"))
		if err != nil {
			return fmt.Errorf("item %d: %w", id, err)
		}
		m.itemMap[id] = id
		m.itemKind[id] = kind
		if r.str("media_type") == "tv" {
			shows[key{r.str("media_id"), r.str("source")}] = id
		}
		if err := m.itemIDs(id, kind, r.str("source"), r.str("media_id")); err != nil {
			return err
		}
		m.rep.count("items "+kind, 1)
	}
	for _, r := range folded {
		if id, ok := shows[key{r.str("media_id"), r.str("source")}]; ok {
			m.showOf[r.i64("id")] = id
		}
	}
	return nil
}

// itemIDs records the ids of an item: its own, plus the MAL/AniList/TVDB ids
// the anime mapping knows for a TMDB anime or a MAL/AniList anime (what
// catalog.storeIDs does for a freshly added item).
func (m *migrator) itemIDs(id int64, kind, source, externalID string) error {
	add := func(src string, v sql.NullInt64) error {
		if !v.Valid || v.Int64 == 0 {
			return nil
		}
		_, err := m.exec(`INSERT OR IGNORE INTO item_ids (item_id, source, external_id) VALUES (?, ?, ?)`, id, src, strconv.FormatInt(v.Int64, 10))
		return err
	}
	if _, err := m.exec(`INSERT OR IGNORE INTO item_ids (item_id, source, external_id) VALUES (?, ?, ?)`, id, source, externalID); err != nil {
		return err
	}
	n, _ := strconv.ParseInt(externalID, 10, 64)
	var where string
	switch {
	case source == "tmdb" && (kind == "anime" || kind == "tv") && n != 0:
		where = "tmdb_tv"
	case source == "tmdb" && (kind == "anime_movie" || kind == "movie") && n != 0:
		where = "tmdb_movie"
	case (source == "mal" || source == "anilist") && kind == "anime" && n != 0:
		where = map[string]string{"mal": "mal_id", "anilist": "anilist_id"}[source]
	default:
		return nil
	}
	rs, err := m.tx.QueryContext(m.ctx, `SELECT mal_id, anilist_id, tvdb_id FROM anime_mappings WHERE `+where+` = ? ORDER BY id`, n)
	if err != nil {
		return err
	}
	type ids struct{ mal, ani, tvdb sql.NullInt64 }
	var found []ids
	for rs.Next() {
		var x ids
		if err := rs.Scan(&x.mal, &x.ani, &x.tvdb); err != nil {
			rs.Close()
			return err
		}
		found = append(found, x)
	}
	rs.Close()
	if err := rs.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, x := range found {
		for _, p := range []struct {
			src string
			v   sql.NullInt64
		}{{"mal", x.mal}, {"anilist", x.ani}, {"tvdb", x.tvdb}} {
			if err := add(p.src, p.v); err != nil {
				return err
			}
		}
	}
	return nil
}

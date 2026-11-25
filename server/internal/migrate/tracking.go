package migrate

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
)

// SourceImport tags every migrated play: Django did not record where a play
// came from (webhook, import or the UI), and "import" is the source the
// additive-only rules treat as automated.
const SourceImport = "import"

// countTables are the Django per-type tables tracked by a progress count (or,
// for movies, a single play): each row is one run of one item.
var countTables = []struct{ table, hist string }{
	{"app_anime", "app_historicalanime"}, {"app_manga", "app_historicalmanga"},
	{"app_book", "app_historicalbook"}, {"app_comic", "app_historicalcomic"},
	{"app_game", "app_historicalgame"}, {"app_boardgame", "app_historicalboardgame"},
}

type entryIn struct {
	userID, itemID, run                 int64
	status                              string
	score                               sql.NullFloat64
	progress                            int64
	progressedAt, startedAt, finishedAt sql.NullString
	notes, created, updated             string
}

func (m *migrator) insertEntry(e entryIn) (int64, error) {
	if e.updated == "" {
		e.updated = e.created
	}
	return m.insertID(`INSERT INTO entries (user_id, item_id, run, status, score, progress, progressed_at, started_at, finished_at, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.userID, e.itemID, e.run, e.status, e.score, e.progress, e.progressedAt, e.startedAt, e.finishedAt, e.notes, e.created, e.updated)
}

func (m *migrator) insertWatch(entry int64, season, episode sql.NullInt64, at, source, notes string) error {
	_, err := m.exec(`INSERT INTO watches (entry_id, season, episode, watched_at, source, notes) VALUES (?, ?, ?, ?, ?, ?)`,
		entry, season, episode, at, source, notes)
	return err
}

func (m *migrator) createdStamp(r row) string {
	if s := r.stamp("created_at"); s.Valid {
		return s.String
	}
	return stampOf(m.opts.Now())
}

func (m *migrator) tracking() error {
	if err := m.shows(); err != nil {
		return err
	}
	if err := m.movies(); err != nil {
		return err
	}
	for _, t := range countTables {
		if err := m.countKind(t.table); err != nil {
			return err
		}
	}
	return nil
}

type watchIn struct {
	season, episode int64
	at, notes       string
}

// shows folds Django's TV / Season / Episode rows into one entry per show:
// the season rows become entry_seasons, every Episode row (more than one per
// episode when it was rewatched) a play, and the show's progress and dates
// are derived from the plays the way the Go library derives them.
func (m *migrator) shows() error {
	tvs, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM app_tv ORDER BY id`)
	if err != nil {
		return err
	}
	seasons, err := queryRows(m.ctx, m.opts.Source, `SELECT s.id, s.related_tv_id, s.status, s.score, s.notes, i.season_number
		FROM app_season s JOIN app_item i ON i.id = s.item_id ORDER BY s.id`)
	if err != nil {
		return err
	}
	eps, err := queryRows(m.ctx, m.opts.Source, `SELECT e.id, e.related_season_id, e.end_date, e.created_at, e.notes, i.season_number, i.episode_number
		FROM app_episode e JOIN app_item i ON i.id = e.item_id ORDER BY e.id`)
	if err != nil {
		return err
	}
	seasonsOf := map[int64][]row{}
	tvOfSeason := map[int64]int64{}
	for _, s := range seasons {
		seasonsOf[s.i64("related_tv_id")] = append(seasonsOf[s.i64("related_tv_id")], s)
		tvOfSeason[s.i64("id")] = s.i64("related_tv_id")
	}
	watchesOf := map[int64][]watchIn{}
	for _, e := range eps {
		tv, ok := tvOfSeason[e.i64("related_season_id")]
		if !ok {
			continue
		}
		at := e.stamp("end_date")
		if !at.Valid {
			at = e.stamp("created_at")
			m.rep.count("episode plays without a date (used the row's creation time)", 1)
		}
		if !at.Valid {
			continue
		}
		watchesOf[tv] = append(watchesOf[tv], watchIn{season: e.i64("season_number"), episode: e.i64("episode_number"), at: at.String, notes: e.str("notes")})
	}

	for _, r := range tvs {
		uid, okU := m.userMap[r.i64("user_id")]
		item, okI := m.itemMap[r.i64("item_id")]
		if !okU || !okI {
			m.rep.warn("tv row %d points at an unmigrated user or item; skipped", r.i64("id"))
			continue
		}
		ws := watchesOf[r.i64("id")]
		sort.SliceStable(ws, func(i, j int) bool {
			if ws[i].season != ws[j].season {
				return ws[i].season < ws[j].season
			}
			if ws[i].episode != ws[j].episode {
				return ws[i].episode < ws[j].episode
			}
			return ws[i].at < ws[j].at
		})
		distinct := map[[2]int64]bool{}
		var first, last string
		for _, w := range ws {
			if w.season == 0 {
				continue
			}
			distinct[[2]int64{w.season, w.episode}] = true
			if first == "" || w.at < first {
				first = w.at
			}
			if w.at > last {
				last = w.at
			}
		}
		e := entryIn{
			userID: uid, itemID: item, run: 1, status: r.str("status"), score: r.nullFloat("score"),
			progress: int64(len(distinct)), notes: r.str("notes"), created: m.createdStamp(r),
			startedAt: nstr(first), progressedAt: nstr(last),
		}
		if e.status == "Completed" {
			e.finishedAt = nstr(last)
		}
		e.updated = e.created
		if last > e.updated {
			e.updated = last
		}
		id, err := m.insertEntry(e)
		if err != nil {
			return fmt.Errorf("tv row %d: %w", r.i64("id"), err)
		}
		m.entryBy["app_tv:"+strconv.FormatInt(r.i64("id"), 10)] = id
		m.rep.count("entries "+m.itemKind[item], 1)
		for _, w := range ws {
			if err := m.insertWatch(id, sql.NullInt64{Int64: w.season, Valid: true}, sql.NullInt64{Int64: w.episode, Valid: true}, w.at, SourceImport, w.notes); err != nil {
				return err
			}
			m.rep.count("watches (episodes)", 1)
		}
		for _, s := range seasonsOf[r.i64("id")] {
			if _, err := m.exec(`INSERT OR REPLACE INTO entry_seasons (entry_id, season, status, score, notes) VALUES (?, ?, ?, ?, ?)`,
				id, s.i64("season_number"), s.str("status"), s.nullFloat("score"), s.str("notes")); err != nil {
				return err
			}
			m.rep.count("entry seasons", 1)
		}
	}
	return nil
}

// movies: every Django Movie row is one run; a Completed run has one play.
func (m *migrator) movies() error {
	rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM app_movie ORDER BY user_id, item_id, created_at, id`)
	if err != nil {
		return err
	}
	runs := map[[2]int64]int64{}
	for _, r := range rows {
		uid, okU := m.userMap[r.i64("user_id")]
		item, okI := m.itemMap[r.i64("item_id")]
		if !okU || !okI {
			m.rep.warn("movie row %d points at an unmigrated user or item; skipped", r.i64("id"))
			continue
		}
		runs[[2]int64{uid, item}]++
		e := m.countEntry(r, uid, item, runs[[2]int64{uid, item}])
		completed := e.status == "Completed"
		if completed && e.progress < 1 {
			e.progress = 1
		}
		at := r.stamp("end_date")
		if !at.Valid {
			at = e.progressedAt
		}
		if !at.Valid {
			at = nstr(e.created)
		}
		id, err := m.insertEntry(e)
		if err != nil {
			return fmt.Errorf("movie row %d: %w", r.i64("id"), err)
		}
		m.entryBy["app_movie:"+strconv.FormatInt(r.i64("id"), 10)] = id
		m.rep.count("entries "+m.itemKind[item], 1)
		if completed {
			if err := m.insertWatch(id, sql.NullInt64{}, sql.NullInt64{}, at.String, SourceImport, ""); err != nil {
				return err
			}
			m.rep.count("watches (movies)", 1)
		}
	}
	return nil
}

func (m *migrator) countEntry(r row, uid, item, run int64) entryIn {
	e := entryIn{
		userID: uid, itemID: item, run: run, status: r.str("status"), score: r.nullFloat("score"),
		progress: r.i64("progress"), progressedAt: r.stamp("progressed_at"),
		startedAt: r.stamp("start_date"), finishedAt: r.stamp("end_date"),
		notes: r.str("notes"), created: m.createdStamp(r),
	}
	e.updated = e.created
	if e.progressedAt.Valid && e.progressedAt.String > e.updated {
		e.updated = e.progressedAt.String
	}
	return e
}

// countKind migrates one progress-count table; repeated rows for the same
// item become runs, oldest first.
func (m *migrator) countKind(table string) error {
	if !hasTable(m.ctx, m.opts.Source, table) {
		return nil
	}
	rows, err := queryRows(m.ctx, m.opts.Source, `SELECT * FROM `+table+` ORDER BY user_id, item_id, created_at, id`)
	if err != nil {
		return err
	}
	runs := map[[2]int64]int64{}
	for _, r := range rows {
		uid, okU := m.userMap[r.i64("user_id")]
		item, okI := m.itemMap[r.i64("item_id")]
		if !okU || !okI {
			m.rep.warn("%s row %d points at an unmigrated user or item; skipped", table, r.i64("id"))
			continue
		}
		runs[[2]int64{uid, item}]++
		id, err := m.insertEntry(m.countEntry(r, uid, item, runs[[2]int64{uid, item}]))
		if err != nil {
			return fmt.Errorf("%s row %d: %w", table, r.i64("id"), err)
		}
		m.entryBy[table+":"+strconv.FormatInt(r.i64("id"), 10)] = id
		m.rep.count("entries "+m.itemKind[item], 1)
	}
	return nil
}

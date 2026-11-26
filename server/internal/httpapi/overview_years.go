package httpapi

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// overviewKindOrder lists kinds the way the year views walk them.
var overviewKindOrder = []string{
	catalog.KindTV, catalog.KindAnime, catalog.KindMovie, catalog.KindAnimeMovie,
	catalog.KindManga, catalog.KindBook, catalog.KindComic, catalog.KindGame, catalog.KindBoardgame,
}

// overviewYears lists years with tracked activity (api/years.py: years).
func (s *Server) overviewYears(r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	loc := overviewLoc(&u)
	enabled, err := s.overviewEnabled(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Library.UserEntries(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	today := s.Library.Now().In(loc)
	seen := map[int]bool{}
	for _, row := range rows {
		if !enabled[row.Item.Kind] {
			continue
		}
		for _, y := range library.EntryYears(row.Entry, today) {
			seen[y] = true
		}
	}
	sessions, err := s.Queries.ListUserReadingSessions(r.Context(), db.ListUserReadingSessionsParams{
		UserID: u.ID, FromAt: "0000-01-01T00:00:00Z", ToAt: "9999-12-31T23:59:59Z",
	})
	if err != nil {
		return nil, err
	}
	for _, ss := range sessions {
		if t, ok := overviewTime(ss.StartedAt, loc); ok {
			seen[t.Year()] = true
		}
	}
	years := make([]int, 0, len(seen))
	for y := range seen {
		years = append(years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	return years, nil
}

// overviewSection accumulates one kind's numbers for the year.
type overviewSection struct {
	units, minutes int
	byMonth        [12]int
	counts         map[int64]int // entry id -> units, in first-seen order below
	order          []int64
	items          map[int64]db.Item
	finished       int
	started        int
}

func newOverviewSection() *overviewSection {
	return &overviewSection{counts: map[int64]int{}, items: map[int64]db.Item{}}
}

func (a *overviewSection) add(entryID int64, item db.Item, month, n int) {
	if _, ok := a.counts[entryID]; !ok {
		a.order = append(a.order, entryID)
		a.items[entryID] = item
	}
	a.counts[entryID] += n
	a.byMonth[month-1] += n
	a.units += n
}

func (a *overviewSection) top(kind string) []map[string]any {
	ids := append([]int64(nil), a.order...)
	sort.SliceStable(ids, func(i, j int) bool { return a.counts[ids[i]] > a.counts[ids[j]] })
	out := []map[string]any{}
	for _, id := range ids[:min(5, len(ids))] {
		out = append(out, map[string]any{"id": id, "media_type": kind, "item": overviewItem(a.items[id], "", nil, nil), "count": a.counts[id]})
	}
	return out
}

func (a *overviewSection) json(kind string, withMinutes bool) map[string]any {
	by := make([]int, 12)
	copy(by, a.byMonth[:])
	out := map[string]any{"units": a.units, "titles": len(a.order), "by_month": by, "top": a.top(kind), "finished": a.finished, "started": a.started}
	if withMinutes {
		out["minutes"] = a.minutes
	}
	return out
}

// overviewGenres lists an item's genres (strings or {"name": ...} objects).
func overviewGenres(item db.Item) []string {
	var out []string
	switch list := library.ItemMeta(item)["genres"].(type) {
	case []any:
		for _, g := range list {
			switch v := g.(type) {
			case string:
				if v != "" {
					out = append(out, v)
				}
			case map[string]any:
				if name, _ := v["name"].(string); name != "" {
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// overviewSnapshot builds the year-end snapshot (api/years.py: build_snapshot).
func (s *Server) overviewSnapshot(ctx context.Context, u db.User, year int) (map[string]any, error) {
	loc := overviewLoc(&u)
	enabled, err := s.overviewEnabled(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Library.UserEntries(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now := s.Library.Now().In(loc)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc)
	inYear := func(v string) (time.Time, bool) {
		t, ok := overviewTime(v, loc)
		return t, ok && t.Year() == year
	}
	byEntry := map[int64]library.LibraryRow{}
	for _, row := range rows {
		if enabled[row.Item.Kind] {
			byEntry[row.Entry.ID] = row
		}
	}
	episodic := func(row library.LibraryRow) bool { return catalog.HasEpisodes(row.Item) }
	// Count-based entries only count for a year they were active in (the
	// Django view's active_in_year), so history written long after the dates
	// of the entry (a migrated or imported list) does not land in this year.
	active := func(e db.Entry) bool { return overviewActiveInYear(e, year, now, loc) }

	sections := map[string]*overviewSection{}
	section := func(kind string) *overviewSection {
		if sections[kind] == nil {
			sections[kind] = newOverviewSection()
		}
		return sections[kind]
	}

	// Episode plays of shows (tv and TMDB-structured anime).
	plays, err := s.Queries.ListUserEpisodePlays(ctx, db.ListUserEpisodePlaysParams{
		UserID: u.ID, FromAt: overviewStamp(start), ToAt: overviewStamp(end),
	})
	if err != nil {
		return nil, err
	}
	for _, p := range plays {
		row, ok := byEntry[p.EntryID]
		if !ok || !episodic(row) {
			continue
		}
		t, ok := overviewTime(p.WatchedAt, loc)
		if !ok || t.Year() != year {
			continue
		}
		sec := section(row.Item.Kind)
		sec.add(row.Entry.ID, row.Item, int(t.Month()), 1)
		sec.minutes += int(p.Runtime)
	}

	// Progress gained by count-based entries, from the change log.
	events, err := s.Queries.ListUserProgressEvents(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	gained := map[int64]int{}
	var gainOrder []int64
	prevEntry, prev := int64(-1), 0
	for _, ev := range events {
		if ev.EntryID != prevEntry {
			prevEntry, prev = ev.EntryID, 0
		}
		n, _ := strconv.Atoi(ev.New.String)
		delta := n - prev
		prev = n
		row, ok := byEntry[ev.EntryID]
		if !ok || episodic(row) || catalog.IsMovie(row.Item.Kind) || delta <= 0 || !active(row.Entry) {
			continue
		}
		if t, ok := inYear(ev.At); ok {
			if gained[ev.EntryID] == 0 {
				gainOrder = append(gainOrder, ev.EntryID)
			}
			gained[ev.EntryID] += delta
			section(row.Item.Kind).add(row.Entry.ID, row.Item, int(t.Month()), delta)
		}
	}

	// Finished and started entries.
	var finished []map[string]any
	type finishedRow struct {
		end   string
		entry map[string]any
		score *float64
	}
	var fin []finishedRow
	genreItems := []db.Item{}
	var showGenre, movieGenre, countGenre []db.Item
	doneCount := map[int64]bool{}
	for _, row := range rows {
		kind := row.Item.Kind
		if !enabled[kind] {
			continue
		}
		e := row.Entry
		if !episodic(row) && !catalog.IsMovie(kind) && !active(e) {
			continue
		}
		if _, ok := inYear(e.StartedAt.String); ok && e.StartedAt.Valid {
			section(kind).started++
		}
		done := false
		if e.Status == library.Completed && e.FinishedAt.Valid {
			if _, ok := inYear(e.FinishedAt.String); ok {
				done = true
				doneCount[e.ID] = true
				section(kind).finished++
				fr := finishedRow{end: e.FinishedAt.String, entry: map[string]any{
					"id": e.ID, "media_type": kind, "item": overviewItem(row.Item, "", nil, nil),
					"score": overviewNullFloat(e.Score), "end_date": e.FinishedAt.String,
				}}
				if e.Score.Valid {
					v := e.Score.Float64
					fr.score = &v
				}
				fin = append(fin, fr)
			}
		}
		switch {
		case episodic(row):
			active := false
			for _, y := range library.EntryYears(e, now) {
				active = active || y == year
			}
			if active {
				showGenre = append(showGenre, row.Item)
			}
		case catalog.IsMovie(kind):
			if done {
				movieGenre = append(movieGenre, row.Item)
			}
		default:
			if gained[e.ID] > 0 || done {
				countGenre = append(countGenre, row.Item)
			}
		}
	}
	genreItems = append(append(append(genreItems, showGenre...), movieGenre...), countGenre...)

	// Movies count as one unit each, in the month they were finished.
	for _, f := range fin {
		kind := f.entry["media_type"].(string)
		if catalog.IsMovie(kind) {
			t, _ := overviewTime(f.end, loc)
			sec := section(kind)
			sec.add(f.entry["id"].(int64), byEntry[f.entry["id"].(int64)].Item, int(t.Month()), 1)
		}
	}

	sort.SliceStable(fin, func(i, j int) bool { return fin[i].end < fin[j].end })
	for _, f := range fin {
		finished = append(finished, f.entry)
	}
	if finished == nil {
		finished = []map[string]any{}
	}
	rated := []finishedRow{}
	for _, f := range fin {
		if f.score != nil {
			rated = append(rated, f)
		}
	}
	sort.SliceStable(rated, func(i, j int) bool { return *rated[i].score > *rated[j].score })
	topRated := []map[string]any{}
	for _, f := range rated[:min(8, len(rated))] {
		topRated = append(topRated, f.entry)
	}

	out := map[string]map[string]any{}
	totalTitles, watchMinutes := 0, 0
	for _, kind := range overviewKindOrder {
		sec := sections[kind]
		if sec == nil || (len(sec.order) == 0 && sec.finished == 0) {
			continue
		}
		show := kind == catalog.KindTV || kind == catalog.KindAnime
		if kind == catalog.KindTV && len(sec.order) == 0 {
			continue
		}
		out[kind] = sec.json(kind, show)
		if catalog.IsMovie(kind) {
			out[kind]["top"] = []map[string]any{}
		}
		totalTitles += len(sec.order)
		if show {
			watchMinutes += sec.minutes
		}
	}

	genreCount := map[string]int{}
	var genreOrder []string
	for _, item := range genreItems {
		seen := map[string]bool{}
		for _, g := range overviewGenres(item) {
			if seen[g] {
				continue
			}
			seen[g] = true
			if _, ok := genreCount[g]; !ok {
				genreOrder = append(genreOrder, g)
			}
			genreCount[g]++
		}
	}
	sort.SliceStable(genreOrder, func(i, j int) bool { return genreCount[genreOrder[i]] > genreCount[genreOrder[j]] })
	genres := []map[string]any{}
	for _, g := range genreOrder[:min(10, len(genreOrder))] {
		genres = append(genres, map[string]any{"name": g, "count": genreCount[g]})
	}

	reading, err := s.overviewReadingSection(ctx, u.ID, year, now, loc)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"year": year, "generated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"totals":   map[string]any{"finished": len(finished), "titles": totalTitles, "watch_minutes": watchMinutes},
		"sections": out, "reading": reading, "finished": finished, "top_rated": topRated, "genres": genres,
	}, nil
}

func (s *Server) overviewReadingSection(ctx context.Context, userID int64, year int, now time.Time, loc *time.Location) (any, error) {
	first := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	last := time.Date(year, 12, 31, 0, 0, 0, 0, loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if today.Before(last) {
		last = today
	}
	if last.Before(first) {
		return nil, nil
	}
	days, err := s.overviewDailyStats(ctx, userID, first, last, loc)
	if err != nil {
		return nil, err
	}
	var pages, chapters, minutes, sessions, daysRead, longest, run int
	pagesBy, minutesBy := make([]int, 12), make([]int, 12)
	var best *overviewDay
	for i, d := range days {
		pages += d.Pages
		chapters += d.Chapters
		minutes += d.Minutes
		sessions += d.Sessions
		if d.Pages > 0 || d.Chapters > 0 {
			daysRead++
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
		t, _ := time.ParseInLocation("2006-01-02", d.Date, loc)
		pagesBy[t.Month()-1] += d.Pages
		minutesBy[t.Month()-1] += d.Minutes
		if best == nil || d.Pages > best.Pages {
			best = &days[i]
		}
	}
	var bestDay any
	if best != nil && best.Pages > 0 {
		bestDay = best
	}
	return map[string]any{
		"pages": pages, "chapters": chapters, "minutes": minutes, "sessions": sessions, "days_read": daysRead,
		"days_in_range": len(days), "longest_streak": longest, "best_day": bestDay,
		"pages_by_month": pagesBy, "minutes_by_month": minutesBy, "days": days,
	}, nil
}

func (s *Server) overviewSnapshotHandler(r *http.Request) (any, error) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "year must be an integer")
	}
	if year < 1 || year > 9999 {
		return nil, Errorf(http.StatusUnprocessableEntity, "year out of range")
	}
	return s.overviewSnapshot(r.Context(), overviewUser(r.Context()), year)
}

// overviewActiveInYear ports api/years.py active_in_year: an entry is active
// in a year when it started on or before the year's last day and ended (was
// finished, last progressed, or is still going) on or after its first day. An
// entry that ends before it starts (its dates predate the row's creation) is
// active in no year.
func overviewActiveInYear(e db.Entry, year int, now time.Time, loc *time.Location) bool {
	day := func(s sql.NullString) (time.Time, bool) {
		if !s.Valid {
			return time.Time{}, false
		}
		t, ok := overviewTime(s.String, loc)
		if !ok {
			return time.Time{}, false
		}
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc), true
	}
	start, ok := day(e.StartedAt)
	if !ok {
		if start, ok = day(sql.NullString{String: e.CreatedAt, Valid: true}); !ok {
			return false
		}
	}
	end, ok := day(e.FinishedAt)
	if !ok {
		end, ok = day(e.ProgressedAt)
	}
	if !ok {
		end = start
		if e.Status == library.InProgress {
			end = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		}
	}
	first, last := time.Date(year, 1, 1, 0, 0, 0, 0, loc), time.Date(year, 12, 31, 0, 0, 0, 0, loc)
	return !start.After(last) && !end.Before(first)
}

package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// This file has the read side the API needs: plain functions returning
// structs. JSON shaping is the API layer's job, but every field the Django
// API returned (src/api/*.py) is available here.

// ItemMeta parses items.meta (genres, details, score, external_links, ...).
func ItemMeta(item Item) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(item.Meta), &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

// LibraryFilter selects entries for ListLibrary.
type LibraryFilter struct {
	Kinds  []string // empty = every kind (anime lives in anime and anime_movie)
	Status string   // "" = any
	Search string   // title, native title or notes contain this
	Year   *int     // entries with any activity in this year
	Limit  int      // 0 = 200
	Offset int
}

// LibraryRow is one entry of the library list (media_dict + is_anime).
type LibraryRow struct {
	Entry       Entry
	Item        Item
	MaxProgress *int // episodes/chapters/pages when known (a show's regular episodes)
	IsAnime     bool
}

// LibraryPage is a page of the library list.
type LibraryPage struct {
	Count   int   // matching entries before paging
	Years   []int // every year with activity among the matching entries, newest first
	Results []LibraryRow
}

// EntryYears is every year an entry was active in: from its start (or
// creation) to its end (finish, last progress, or today when In progress).
func EntryYears(e Entry, today time.Time) []int {
	start, ok := dayOf(e.StartedAt, today.Location())
	if !ok {
		if start, ok = dayOf(sql.NullString{String: e.CreatedAt, Valid: true}, today.Location()); !ok {
			return nil
		}
	}
	end, ok := dayOf(e.FinishedAt, today.Location())
	if !ok {
		end, ok = dayOf(e.ProgressedAt, today.Location())
	}
	if !ok {
		end = start
		if e.Status == InProgress {
			end = today
		}
	}
	if end.Before(start) {
		end = start
	}
	var years []int
	for y := start.Year(); y <= end.Year(); y++ {
		years = append(years, y)
	}
	return years
}

func dayOf(s sql.NullString, loc *time.Location) (time.Time, bool) {
	if !s.Valid || s.String == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return time.Time{}, false
	}
	return t.In(loc), true
}

func kindsJSON(kinds []string) string {
	if len(kinds) == 0 {
		return ""
	}
	b, _ := json.Marshal(kinds)
	return string(b)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListLibrary lists the user's current entries (highest run per item),
// newest activity first, with the filters of GET /library.
func (s *Service) ListLibrary(ctx context.Context, userID int64, f LibraryFilter) (LibraryPage, error) {
	rows, err := s.Q.ListLibraryEntries(ctx, db.ListLibraryEntriesParams{
		UserID: userID, KindsJson: kindsJSON(f.Kinds), Status: nilIfEmpty(f.Status), Search: nilIfEmpty(f.Search),
	})
	if err != nil {
		return LibraryPage{}, err
	}
	today := s.now().In(s.Loc)
	years := map[int]bool{}
	var matched []LibraryRow
	for _, r := range rows {
		ys := EntryYears(r.Entry, today)
		for _, y := range ys {
			years[y] = true
		}
		if f.Year != nil && !containsInt(ys, *f.Year) {
			continue
		}
		row := LibraryRow{Entry: r.Entry, Item: r.Item, IsAnime: r.Item.Kind == catalog.KindAnime || r.Item.Kind == catalog.KindAnimeMovie}
		if r.Item.Total.Valid {
			n := int(r.Item.Total.Int64)
			row.MaxProgress = &n
		}
		matched = append(matched, row)
	}
	page := LibraryPage{Count: len(matched)}
	for y := range years {
		page.Years = append(page.Years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(page.Years)))
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	off := min(max(f.Offset, 0), len(matched))
	page.Results = matched[off:min(off+limit, len(matched))]
	return page, nil
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// FindEntry returns the user's current entry for an item found by provider id
// (GET /library/find). The bool is false when the user doesn't track it.
func (s *Service) FindEntry(ctx context.Context, userID int64, cat *catalog.Catalog, source, externalID string, kinds ...string) (Entry, Item, bool, error) {
	item, err := cat.FindByExternalID(ctx, source, externalID, kinds...)
	if errors.Is(err, catalog.ErrNotFound) {
		return Entry{}, Item{}, false, nil
	}
	if err != nil {
		return Entry{}, Item{}, false, err
	}
	e, err := s.CurrentEntry(ctx, userID, item.ID)
	if errors.Is(err, ErrNoEntry) {
		return Entry{}, item, false, nil
	}
	return e, item, err == nil, err
}

// HistoryRecord is one point of an entry's change log (the API's `history`).
type HistoryRecord struct {
	At       string
	Type     string // "created" or "changed"
	Status   string
	Progress int
	Score    *float64
}

// PlayedEpisode is one recorded play of an episode.
type PlayedEpisode struct {
	WatchID      int64
	Season       int
	Number       int
	Title        string // the episode's own title, "" when unknown
	WatchedAt    string
	Notes        string
	Source       string
	Watches      int // how many plays of this episode exist (repeats)
	EpisodeCount int
}

// SeasonDetail is one season of a tracked show.
type SeasonDetail struct {
	Number       int
	Status       string
	Score        *float64
	Notes        string
	Progress     int // episode number of the most recent play (the highest when not In progress)
	Watched      int // distinct episodes watched
	EpisodeCount int // episodes TMDB lists
	Episodes     []PlayedEpisode
}

// EntryDetail is one entry with everything the detail page shows.
type EntryDetail struct {
	Entry   Entry
	Item    Item
	History []HistoryRecord
	Seasons []SeasonDetail // shows only, by season number
	Watches []Watch        // movies: every play
	Runs    []Entry        // every run of the item, oldest first
}

// GetEntryDetail returns an entry of the user with its history, seasons and
// watched episodes (GET /library/{type}/{id}).
func (s *Service) GetEntryDetail(ctx context.Context, userID, entryID int64) (EntryDetail, error) {
	row, err := s.Q.GetEntryWithItem(ctx, db.GetEntryWithItemParams{ID: entryID, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return EntryDetail{}, ErrNoEntry
	}
	if err != nil {
		return EntryDetail{}, err
	}
	d := EntryDetail{Entry: row.Entry, Item: row.Item}
	events, err := s.Q.ListEntryEvents(ctx, entryID)
	if err != nil {
		return d, err
	}
	d.History = History(events)
	if d.Runs, err = s.Runs(ctx, userID, row.Item.ID); err != nil {
		return d, err
	}
	watches, err := s.Q.ListEntryWatches(ctx, entryID)
	if err != nil {
		return d, err
	}
	if catalog.IsMovie(row.Item.Kind) {
		d.Watches = watches
		return d, nil
	}
	if !catalog.IsShow(row.Item.Kind) {
		return d, nil
	}
	seasons, err := s.Q.ListEntrySeasons(ctx, entryID)
	if err != nil {
		return d, err
	}
	eps, err := s.Q.ListItemEpisodes(ctx, row.Item.ID)
	if err != nil {
		return d, err
	}
	titles := map[[2]int64]db.ItemEpisode{}
	perSeason := map[int64]int{}
	for _, e := range eps {
		titles[[2]int64{e.Season, e.Number}] = e
		perSeason[e.Season]++
	}
	bySeason := map[int64][]Watch{}
	for _, w := range watches {
		bySeason[w.Season.Int64] = append(bySeason[w.Season.Int64], w)
	}
	for _, es := range seasons {
		sd := SeasonDetail{Number: int(es.Season), Status: es.Status, Notes: es.Notes, EpisodeCount: perSeason[es.Season]}
		if es.Score.Valid {
			v := es.Score.Float64
			sd.Score = &v
		}
		ws := bySeason[es.Season]
		counts := map[int64]int{}
		for _, w := range ws {
			counts[w.Episode.Int64]++
		}
		sd.Watched = len(counts)
		sd.Progress = seasonProgress(ws, es.Status == InProgress)
		for _, w := range ws { // ListEntryWatches orders by season, episode, time
			pe := PlayedEpisode{
				WatchID: w.ID, Season: int(es.Season), Number: int(w.Episode.Int64), Title: titles[[2]int64{es.Season, w.Episode.Int64}].Title,
				WatchedAt: w.WatchedAt, Notes: w.Notes, Source: w.Source, Watches: counts[w.Episode.Int64],
			}
			sd.Episodes = append(sd.Episodes, pe)
		}
		d.Seasons = append(d.Seasons, sd)
	}
	return d, nil
}

// seasonProgress ports Season.progress: the episode number of the most recent
// play when the season is In progress (ties go to the higher number), else the
// highest episode number played.
func seasonProgress(ws []Watch, inProgress bool) int {
	best, bestAt := 0, ""
	for _, w := range ws {
		n := int(w.Episode.Int64)
		if !inProgress {
			best = max(best, n)
			continue
		}
		if w.WatchedAt > bestAt || (w.WatchedAt == bestAt && n > best) {
			best, bestAt = n, w.WatchedAt
		}
	}
	return best
}

// History turns an entry's events into the Django change log: one record per
// moment, with the status, progress and score as they were after it.
func History(events []db.EntryEvent) []HistoryRecord {
	var out []HistoryRecord
	cur := HistoryRecord{}
	for i, ev := range events {
		if i > 0 && ev.At != events[i-1].At {
			out = append(out, cur)
			cur.Type = "changed"
		}
		if i == 0 || ev.At != events[i-1].At {
			cur.At = ev.At
			cur.Type = "changed"
		}
		switch ev.Field {
		case "created":
			cur.Type = "created"
			cur.Status = ev.New.String
		case "status":
			cur.Status = ev.New.String
		case "progress":
			n, _ := strconv.Atoi(ev.New.String)
			cur.Progress = n
		case "score":
			if ev.New.Valid {
				v, _ := strconv.ParseFloat(ev.New.String, 64)
				cur.Score = &v
			} else {
				cur.Score = nil
			}
		}
	}
	if len(events) > 0 {
		out = append(out, cur)
	}
	return out
}

// ProgressDates ports api/anime._watch_dates for count-based anime: episode
// number -> when progress first reached it (most recent pass), replayed from
// the entry's progress events.
func (s *Service) ProgressDates(ctx context.Context, entryID int64) (map[int]string, error) {
	events, err := s.Q.ListEntryEvents(ctx, entryID)
	if err != nil {
		return nil, err
	}
	dates := map[int]string{}
	prev := 0
	for _, ev := range events {
		if ev.Field != "progress" {
			continue
		}
		p, _ := strconv.Atoi(ev.New.String)
		switch {
		case p > prev:
			for n := prev + 1; n <= p; n++ {
				dates[n] = ev.At
			}
		case p < prev:
			for n := p + 1; n <= prev; n++ {
				delete(dates, n)
			}
		}
		prev = p
	}
	return dates, nil
}

// SeasonEntry is a user's state of one season.
type SeasonEntry struct {
	Status string
	Score  *float64
	Notes  string
}

// ShowSeason is one season in the show overview.
type ShowSeason struct {
	Number       int
	Title        string
	Image        string
	AirDate      string
	EpisodeCount int
	Watched      int      // distinct episodes watched
	Percent      *float64 // watched / episode count, one decimal
	Entry        *SeasonEntry
}

// ShowCompletion totals the regular seasons.
type ShowCompletion struct {
	Watched, Total   int
	Percent          *float64
	SeasonsCompleted int
	Seasons          int
	SpecialsWatched  int
}

// ShowView is the overview of a show with the user's progress (GET /shows/...).
// It works whether or not the user tracks the show.
type ShowView struct {
	Item       Item
	Entry      *Entry
	Seasons    []ShowSeason // regular seasons first, specials last
	Completion ShowCompletion
}

func pct(a, b int) *float64 {
	if b == 0 {
		return nil
	}
	v := float64(int(float64(a)/float64(b)*1000+0.5)) / 10
	return &v
}

// GetShowView builds the show overview from the synced seasons.
func (s *Service) GetShowView(ctx context.Context, userID int64, item Item) (ShowView, error) {
	v := ShowView{Item: item}
	seasons, err := s.Q.ListItemSeasons(ctx, item.ID)
	if err != nil {
		return v, err
	}
	var watched map[int64]map[int64]bool
	entries := map[int64]db.EntrySeason{}
	if e, err := s.CurrentEntry(ctx, userID, item.ID); err == nil {
		v.Entry = &e
		rows, err := s.Q.ListEntrySeasons(ctx, e.ID)
		if err != nil {
			return v, err
		}
		for _, r := range rows {
			entries[r.Season] = r
		}
		if watched, err = s.watchedBySeason(ctx, e.ID); err != nil {
			return v, err
		}
	} else if !errors.Is(err, ErrNoEntry) {
		return v, err
	}
	for _, sd := range seasons {
		ss := ShowSeason{
			Number: int(sd.Number), Title: sd.Title, Image: sd.Image, AirDate: sd.AirDate.String,
			EpisodeCount: int(sd.EpisodeCount), Watched: len(watched[sd.Number]),
		}
		if ss.Title == "" {
			ss.Title = "Season " + strconv.Itoa(ss.Number)
		}
		ss.Percent = pct(ss.Watched, ss.EpisodeCount)
		if es, ok := entries[sd.Number]; ok {
			se := &SeasonEntry{Status: es.Status, Notes: es.Notes}
			if es.Score.Valid {
				sc := es.Score.Float64
				se.Score = &sc
			}
			ss.Entry = se
		}
		v.Seasons = append(v.Seasons, ss)
	}
	sort.SliceStable(v.Seasons, func(i, j int) bool {
		a, b := v.Seasons[i], v.Seasons[j]
		if (a.Number == 0) != (b.Number == 0) {
			return b.Number == 0
		}
		return a.Number < b.Number
	})
	c := &v.Completion
	for _, ss := range v.Seasons {
		if ss.Number == 0 {
			c.SpecialsWatched += ss.Watched
			continue
		}
		c.Seasons++
		c.Watched += ss.Watched
		c.Total += ss.EpisodeCount
		if ss.EpisodeCount > 0 && ss.Watched >= ss.EpisodeCount {
			c.SeasonsCompleted++
		}
	}
	c.Percent = pct(c.Watched, c.Total)
	return v, nil
}

func (s *Service) watchedBySeason(ctx context.Context, entryID int64) (map[int64]map[int64]bool, error) {
	ws, err := s.Q.ListEntryWatches(ctx, entryID)
	if err != nil {
		return nil, err
	}
	out := map[int64]map[int64]bool{}
	for _, w := range ws {
		if out[w.Season.Int64] == nil {
			out[w.Season.Int64] = map[int64]bool{}
		}
		out[w.Season.Int64][w.Episode.Int64] = true
	}
	return out, nil
}

// EpisodeView is one episode of a season with the user's watch state.
type EpisodeView struct {
	Number     int
	Position   int
	Title      string
	Overview   string
	AirDate    string // "" unknown
	Aired      bool
	Runtime    *int
	Image      string
	Kind       string // TMDB episode type, "standard" by default
	Score      *float64
	Watched    bool
	WatchCount int
	WatchedAt  string // most recent play, "" when never
	Notes      string // note of the most recent play
}

// SeasonView is a season with its episodes and the user's progress.
type SeasonView struct {
	Item           Item
	Number         int
	Title          string
	Image          string
	Entry          *SeasonEntry
	EntryID        int64 // the show entry, 0 when untracked
	Watched        int
	Total          int
	RuntimeMinutes int
	Episodes       []EpisodeView
}

// GetSeasonView builds one season of a show with air dates, runtimes and
// watch state (GET /shows/.../seasons/{n}).
func (s *Service) GetSeasonView(ctx context.Context, userID int64, item Item, season int) (SeasonView, error) {
	v := SeasonView{Item: item, Number: season, Title: "Season " + strconv.Itoa(season)}
	seasons, err := s.Q.ListItemSeasons(ctx, item.ID)
	if err != nil {
		return v, err
	}
	for _, sd := range seasons {
		if int(sd.Number) == season {
			v.Image = sd.Image
			if sd.Title != "" {
				v.Title = sd.Title
			}
		}
	}
	eps, err := s.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: item.ID, Season: int64(season)})
	if err != nil {
		return v, err
	}
	plays := map[int64][]Watch{}
	if e, err := s.CurrentEntry(ctx, userID, item.ID); err == nil {
		v.EntryID = e.ID
		if es, err := s.Q.GetEntrySeason(ctx, db.GetEntrySeasonParams{EntryID: e.ID, Season: int64(season)}); err == nil {
			se := &SeasonEntry{Status: es.Status, Notes: es.Notes}
			if es.Score.Valid {
				sc := es.Score.Float64
				se.Score = &sc
			}
			v.Entry = se
		}
		ws, err := s.Q.ListSeasonWatches(ctx, db.ListSeasonWatchesParams{EntryID: e.ID, Season: sql.NullInt64{Int64: int64(season), Valid: true}})
		if err != nil {
			return v, err
		}
		for _, w := range ws {
			plays[w.Episode.Int64] = append(plays[w.Episode.Int64], w)
		}
	} else if !errors.Is(err, ErrNoEntry) {
		return v, err
	}
	released := catalog.ReleasedEpisodes(eps, s.Today())
	for _, e := range eps {
		ev := EpisodeView{
			Number: int(e.Number), Position: int(e.Position), Title: e.Title, Overview: e.Overview, AirDate: e.AirDate.String,
			Aired: released[e.Number], Image: e.Image, Kind: e.Kind,
		}
		if ev.Title == "" {
			ev.Title = fmt.Sprintf("Episode %d", e.Number)
		}
		if e.Runtime.Valid {
			rt := int(e.Runtime.Int64)
			ev.Runtime = &rt
			v.RuntimeMinutes += rt
		}
		if e.Score.Valid {
			sc := e.Score.Float64
			ev.Score = &sc
		}
		if ws := plays[e.Number]; len(ws) > 0 {
			ev.Watched, ev.WatchCount = true, len(ws)
			last := ws[0]
			for _, w := range ws {
				if w.WatchedAt >= last.WatchedAt {
					last = w
				}
			}
			ev.WatchedAt, ev.Notes = last.WatchedAt, last.Notes
			v.Watched++
		}
		v.Episodes = append(v.Episodes, ev)
	}
	v.Total = len(eps)
	return v, nil
}

// NextUp is the next thing to watch or read.
type NextUp struct {
	Number  int
	Title   string // "" when unknown
	AirDate string // "" when unknown
}

// NowEntry is an in-progress entry for the "Media Now" page. For a show it
// stands for one In progress season (Season set); a show with no In progress
// season appears once with Season nil.
type NowEntry struct {
	Entry    Entry
	Item     Item
	Season   *int
	Progress int  // season: episode number of the most recent play; otherwise the entry's progress
	Total    *int // season: last episode number; otherwise item.total
	Watched  int  // season: distinct episodes watched
	Next     *NextUp
	Score    *float64 // season score for a season, entry score otherwise
	// ProgressedAt is when it last moved (falls back to creation).
	ProgressedAt string
}

// NowGroup is the in-progress entries of one kind.
type NowGroup struct {
	Kind    string
	Entries []NowEntry
}

// nowKinds is the order kinds are listed in (groups are then sorted by recency).
var nowKinds = []string{catalog.KindTV, catalog.KindAnime, catalog.KindManga, catalog.KindBook, catalog.KindComic,
	catalog.KindMovie, catalog.KindAnimeMovie, catalog.KindGame, catalog.KindBoardgame}

// NowGroups lists what the user is consuming: In progress entries grouped by kind,
// most recently touched first, at most perGroup per group (0 = 12), groups
// ordered by their most recent activity.
func (s *Service) NowGroups(ctx context.Context, userID int64, perGroup int) ([]NowGroup, error) {
	if perGroup <= 0 {
		perGroup = 12
	}
	rows, err := s.Q.ListNowEntries(ctx, userID)
	if err != nil {
		return nil, err
	}
	seasonRows, err := s.Q.ListNowSeasons(ctx, userID)
	if err != nil {
		return nil, err
	}
	seasonsOf := map[int64][]db.EntrySeason{}
	for _, r := range seasonRows {
		seasonsOf[r.EntryID] = append(seasonsOf[r.EntryID], r)
	}
	groups := map[string][]NowEntry{}
	for _, r := range rows {
		e, item := r.Entry, r.Item
		base := NowEntry{Entry: e, Item: item, Progress: int(e.Progress), ProgressedAt: firstNonEmpty(e.ProgressedAt.String, e.CreatedAt)}
		if e.Score.Valid {
			v := e.Score.Float64
			base.Score = &v
		}
		switch {
		case catalog.HasEpisodes(item) && len(seasonsOf[e.ID]) > 0:
			for _, sr := range seasonsOf[e.ID] {
				ne, err := s.nowSeason(ctx, base, sr)
				if err != nil {
					return nil, err
				}
				groups[item.Kind] = append(groups[item.Kind], ne)
			}
		default:
			if item.Total.Valid {
				t := int(item.Total.Int64)
				base.Total = &t
			}
			if catalog.IsMovie(item.Kind) && base.Total == nil {
				one := 1
				base.Total = &one
			}
			if item.Kind == catalog.KindAnime || item.Kind == catalog.KindManga {
				if base.Total == nil || base.Progress < *base.Total {
					base.Next = &NextUp{Number: base.Progress + 1}
				}
			}
			groups[item.Kind] = append(groups[item.Kind], base)
		}
	}
	var out []NowGroup
	for _, kind := range nowKinds {
		list := groups[kind]
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].ProgressedAt > list[j].ProgressedAt })
		out = append(out, NowGroup{Kind: kind, Entries: list[:min(perGroup, len(list))]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Entries[0].ProgressedAt > out[j].Entries[0].ProgressedAt })
	return out, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) nowSeason(ctx context.Context, base NowEntry, sr db.EntrySeason) (NowEntry, error) {
	n := int(sr.Season)
	base.Season = &n
	base.Score = nil
	if sr.Score.Valid {
		v := sr.Score.Float64
		base.Score = &v
	}
	ws, err := s.Q.ListSeasonWatches(ctx, db.ListSeasonWatchesParams{EntryID: base.Entry.ID, Season: sql.NullInt64{Int64: sr.Season, Valid: true}})
	if err != nil {
		return base, err
	}
	distinct := map[int64]bool{}
	latest := ""
	for _, w := range ws {
		distinct[w.Episode.Int64] = true
		latest = max(latest, w.WatchedAt)
	}
	base.Watched = len(distinct)
	base.Progress = seasonProgress(ws, true)
	if latest != "" {
		base.ProgressedAt = latest
	}
	eps, err := s.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: base.Item.ID, Season: sr.Season})
	if err != nil {
		return base, err
	}
	if len(eps) > 0 {
		last := int(eps[len(eps)-1].Number)
		base.Total = &last
	}
	for _, e := range eps {
		if int(e.Number) > base.Progress {
			base.Next = &NextUp{Number: int(e.Number), Title: e.Title, AirDate: e.AirDate.String}
			break
		}
	}
	return base, nil
}

// FinishedEntry is a recently completed entry.
type FinishedEntry struct {
	Entry Entry
	Item  Item
}

// RecentlyFinished lists the user's most recently completed entries (those
// with a finish date), newest first. kinds limits the kinds (empty = all).
func (s *Service) RecentlyFinished(ctx context.Context, userID int64, limit int, kinds ...string) ([]FinishedEntry, error) {
	if limit <= 0 {
		limit = 12
	}
	rows, err := s.Q.ListFinishedEntries(ctx, db.ListFinishedEntriesParams{UserID: userID, KindsJson: kindsJSON(kinds), MaxRows: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]FinishedEntry, len(rows))
	for i, r := range rows {
		out[i] = FinishedEntry{Entry: r.Entry, Item: r.Item}
	}
	return out, nil
}

// Change is one field change in an activity row.
type Change struct {
	Field    string // status, progress, score, created
	Old, New string // "" = none
}

// ActivityEntry is one row of the activity feed: either the changes made to
// an entry at one moment (Kind "change") or a play of an episode or movie
// (Kind "watch", with Season/Episode set for episodes).
type ActivityEntry struct {
	At      string
	Kind    string
	EntryID int64
	Item    Item
	Changes []Change
	Season  *int
	Episode *int
	Source  string

	// order key, for the cursor
	rank int
	id   int64
}

// ActivityCursor continues an activity listing.
type ActivityCursor struct {
	At   string
	Rank int // 1 = watch, 0 = change
	ID   int64
}

// ActivityQuery selects a page of the feed.
type ActivityQuery struct {
	From, To *time.Time // inclusive start, exclusive end
	Limit    int        // 0 = 50, max 200
	Cursor   *ActivityCursor
}

// ActivityPage is a page of the feed, newest first.
type ActivityPage struct {
	Entries []ActivityEntry
	Next    *ActivityCursor // nil on the last page
}

// Cursor returns the cursor that continues after this row.
func (a ActivityEntry) Cursor() ActivityCursor {
	return ActivityCursor{At: a.At, Rank: a.rank, ID: a.id}
}

// Activity lists the user's tracking history, newest first: status, progress
// and score changes (events of one entry at one moment are one row) and plays
// of episodes and movies.
func (s *Service) Activity(ctx context.Context, userID int64, q ActivityQuery) (ActivityPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	from, to := "0000-01-01T00:00:00Z", "9999-12-31T23:59:59Z"
	if q.From != nil {
		from = stamp(*q.From)
	}
	if q.To != nil {
		to = stamp(*q.To)
	}
	// Rows are ordered by (at, rank, id) descending with plays (rank 1) before
	// changes (rank 0) at the same moment. Each query gets the cursor id that
	// continues its own table: without a cursor everything below `to`.
	cursorAt, evID, watchID := to, int64(0), int64(0)
	if q.Cursor != nil {
		cursorAt = q.Cursor.At
		if q.Cursor.Rank == 1 {
			evID, watchID = 1<<62, q.Cursor.ID // all changes at this moment still follow
		} else {
			evID, watchID = q.Cursor.ID, 0 // plays at this moment were already listed
		}
	}
	// An entry has at most 4 events at one moment (created, status, progress, score), so
	// this window always holds `limit` whole groups.
	rawEvents := limit*4 + 4
	events, err := s.Q.ListActivityEvents(ctx, db.ListActivityEventsParams{
		UserID: userID, FromAt: from, ToAt: to, CursorAt: cursorAt, CursorID: evID, MaxRows: int64(rawEvents),
	})
	if err != nil {
		return ActivityPage{}, err
	}
	plays, err := s.Q.ListActivityWatches(ctx, db.ListActivityWatchesParams{
		UserID: userID, FromAt: from, ToAt: to, CursorAt: cursorAt, CursorID: watchID, MaxRows: int64(limit),
	})
	if err != nil {
		return ActivityPage{}, err
	}
	var rows []ActivityEntry
	for _, w := range plays {
		a := ActivityEntry{At: w.WatchedAt, Kind: "watch", EntryID: w.EntryID, Item: w.Item, Source: w.Source, rank: 1, id: w.ID}
		if w.Season.Valid {
			n := int(w.Season.Int64)
			a.Season = &n
		}
		if w.Episode.Valid {
			n := int(w.Episode.Int64)
			a.Episode = &n
		}
		rows = append(rows, a)
	}
	// Events of one entry at one moment become one row; the row's id is the
	// smallest event id it holds so the cursor never skips a sibling.
	type key struct {
		entry int64
		at    string
	}
	grouped := map[key]*ActivityEntry{}
	for _, ev := range events {
		k := key{ev.EntryID, ev.At}
		g, ok := grouped[k]
		if !ok {
			g = &ActivityEntry{At: ev.At, Kind: "change", EntryID: ev.EntryID, Item: ev.Item, rank: 0, id: ev.ID}
			grouped[k] = g
			rows = append(rows, *g)
		}
		g.id = min(g.id, ev.ID)
		g.Changes = append(g.Changes, Change{Field: ev.Field, Old: ev.Old.String, New: ev.New.String})
	}
	for i := range rows {
		if rows[i].Kind == "change" {
			g := grouped[key{rows[i].EntryID, rows[i].At}]
			rows[i].Changes, rows[i].id = g.Changes, g.id
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.At != b.At {
			return a.At > b.At
		}
		if a.rank != b.rank {
			return a.rank > b.rank
		}
		return a.id > b.id
	})
	page := ActivityPage{}
	if len(rows) > limit {
		rows = rows[:limit]
		c := rows[len(rows)-1].Cursor()
		page.Next = &c
	} else if len(events) >= rawEvents || len(plays) >= limit {
		if len(rows) > 0 {
			c := rows[len(rows)-1].Cursor()
			page.Next = &c
		}
	}
	page.Entries = rows
	return page, nil
}

// UserEntries returns every run of every item the user tracks with its item,
// for the year views.
func (s *Service) UserEntries(ctx context.Context, userID int64) ([]LibraryRow, error) {
	rows, err := s.Q.ListUserEntries(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]LibraryRow, len(rows))
	for i, r := range rows {
		out[i] = LibraryRow{Entry: r.Entry, Item: r.Item, IsAnime: r.Item.Kind == catalog.KindAnime || r.Item.Kind == catalog.KindAnimeMovie}
		if r.Item.Total.Valid {
			n := int(r.Item.Total.Int64)
			out[i].MaxProgress = &n
		}
	}
	return out, nil
}

// Years lists every year with tracked activity, newest first.
func (s *Service) Years(ctx context.Context, userID int64) ([]int, error) {
	rows, err := s.UserEntries(ctx, userID)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	today := s.now().In(s.Loc)
	for _, r := range rows {
		for _, y := range EntryYears(r.Entry, today) {
			seen[y] = true
		}
	}
	var years []int
	for y := range seen {
		years = append(years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	return years, nil
}

// UserWatches returns the user's plays between two times (from inclusive, to
// exclusive), oldest first.
func (s *Service) UserWatches(ctx context.Context, userID int64, from, to time.Time) ([]db.Watch, error) {
	return s.Q.ListUserWatchesBetween(ctx, db.ListUserWatchesBetweenParams{UserID: userID, WatchedAt: stamp(from), WatchedAt_2: stamp(to)})
}

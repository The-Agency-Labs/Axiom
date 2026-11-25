// Package anilist imports a public AniList list (anime and manga, no auth)
// into a user's library. It ports src/integrations/imports/anilist_shows.py
// and the manga/count-based half of anilist.py.
//
// AniList splits a show into one entry per cour or season. Encompass tracks
// the show itself, so each anime entry is placed in its TVDB series through
// the Fribb mapping (TVDB season plus episode offset) and its watched
// episodes are marked there, by position. Anime films become anime_movie items. Entries
// the mapping can't place stay count-based anime (source mal when the media
// has a MAL id, else anilist), and manga are count-based entries whose
// progress is chapters.
//
// The import is additive: it adds episodes and moves statuses forward and
// never removes or lowers anything. It is safe to re-run on a schedule.
package anilist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/mappings"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/integrations/sourcestatus"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// Source is the source string of everything this importer does.
const Source = "anilist"

// SourceName is the name of the importer in the Sources status.
const SourceName = "anilist"

// MaxChanges caps the changes a dry run lists.
const MaxChanges = 500

const entryFields = `
    status
    score(format: POINT_10_DECIMAL)
    progress
    repeat
    notes
    updatedAt
    startedAt { year month day }
    completedAt { year month day }
    media {
        id
        idMal
        format
        episodes
        chapters
        title { userPreferred }
        coverImage { large }
        startDate { year month day }
        endDate { year month day }
    }
`

// Query fetches both lists of a user.
var Query = `
query ($userName: String) {
    anime: MediaListCollection(userName: $userName, type: ANIME) {
        lists { isCustomList entries { ` + entryFields + ` } }
    }
    manga: MediaListCollection(userName: $userName, type: MANGA) {
        lists { isCustomList entries { ` + entryFields + ` } }
    }
}
`

type entry struct {
	Status      string    `json:"status"`
	Score       float64   `json:"score"`
	Progress    int       `json:"progress"`
	Repeat      int       `json:"repeat"`
	Notes       string    `json:"notes"`
	UpdatedAt   int64     `json:"updatedAt"`
	StartedAt   fuzzyDate `json:"startedAt"`
	CompletedAt fuzzyDate `json:"completedAt"`
	Media       media     `json:"media"`
}

type media struct {
	ID       int64  `json:"id"`
	IDMal    *int64 `json:"idMal"`
	Format   string `json:"format"`
	Episodes *int   `json:"episodes"`
	Chapters *int   `json:"chapters"`
	Title    struct {
		UserPreferred string `json:"userPreferred"`
	} `json:"title"`
	CoverImage struct {
		Large string `json:"large"`
	} `json:"coverImage"`
	StartDate fuzzyDate `json:"startDate"`
	EndDate   fuzzyDate `json:"endDate"`
}

func (e entry) title() string { return e.Media.Title.UserPreferred }

type collection struct {
	Lists []struct {
		IsCustomList bool    `json:"isCustomList"`
		Entries      []entry `json:"entries"`
	} `json:"lists"`
}

func (c collection) entries() []entry {
	var out []entry
	for _, l := range c.Lists {
		if !l.IsCustomList {
			out = append(out, l.Entries...)
		}
	}
	return out
}

// GraphQL is the slice of the AniList client the importer uses
// (catalog.AniList satisfies it).
type GraphQL interface {
	GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error)
}

// Importer imports AniList lists.
type Importer struct {
	Q        *db.Queries
	Library  *library.Service
	Catalog  *catalog.Catalog
	Mappings catalog.Mappings
	AniList  GraphQL
	// Users maps AniList names to Encompass users (ANILIST_USERS).
	Users []sourcestatus.Pair
	// Now and Loc are the clock and the zone dates are read in (default UTC).
	Now func() time.Time
	Loc *time.Location
}

// New builds an importer on a catalog and library. users is ANILIST_USERS.
func New(cat *catalog.Catalog, lib *library.Service, q *db.Queries, users []string) *Importer {
	im := &Importer{Q: q, Library: lib, Catalog: cat, Users: sourcestatus.ParsePairs(users)}
	if cat != nil && cat.Reg != nil {
		im.Mappings = cat.Reg.Mappings
		if cat.Reg.P.AniList != nil {
			im.AniList = cat.Reg.P.AniList
		}
	}
	return im
}

func (im *Importer) now() time.Time {
	if im.Now != nil {
		return im.Now().UTC()
	}
	return time.Now().UTC()
}

func (im *Importer) loc() *time.Location {
	if im.Loc != nil {
		return im.Loc
	}
	if im.Library != nil && im.Library.Loc != nil {
		return im.Library.Loc
	}
	return time.UTC
}

// Options tune a run.
type Options struct {
	// Since limits the run to entries edited on AniList after it.
	Since *time.Time
	// DryRun computes what would change and writes nothing to the library.
	DryRun bool
}

// Change is one thing a dry run would do.
type Change struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	What  string `json:"what"`
}

// Result is what a run did (or, in a dry run, would do).
type Result struct {
	DryRun   bool           `json:"dry_run"`
	Counts   map[string]int `json:"counts"`
	Warnings []string       `json:"warnings"`
	Changes  []Change       `json:"changes,omitempty"`
	// Latest is the newest updatedAt of the list (the incremental marker).
	Latest time.Time `json:"latest"`
}

// FetchLists returns the anime and manga entries of a public AniList user.
func (im *Importer) fetchLists(ctx context.Context, username string) (anime, manga []entry, err error) {
	if im.AniList == nil {
		return nil, nil, errors.New("AniList client is not configured")
	}
	raw, err := im.AniList.GraphQL(ctx, Query, map[string]any{"userName": username})
	if err != nil {
		return nil, nil, fmt.Errorf("AniList user %s could not be read: %w", username, err)
	}
	var data struct {
		Anime collection `json:"anime"`
		Manga collection `json:"manga"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, nil, fmt.Errorf("AniList user %s: unexpected response: %w", username, err)
	}
	return data.Anime.entries(), data.Manga.entries(), nil
}

// run is the state of one import.
type run struct {
	im     *Importer
	userID int64
	opts   Options
	res    *Result
	now    time.Time
	loc    *time.Location

	bulk map[dateKey]bool
	// expected: statuses of the AniList entries of each TVDB series, from the
	// placement pass (decides the status a new show starts with).
	expected map[int64]map[string]bool
	// placed: the same for entries that were actually placed.
	placed map[int64]map[string]bool
	shows  map[int64]catalog.Item
	latest time.Time
}

func (r *run) warn(format string, args ...any) {
	r.res.Warnings = append(r.res.Warnings, fmt.Sprintf(format, args...))
}

func (r *run) change(kind, title, what string) {
	if r.opts.DryRun && len(r.res.Changes) < MaxChanges {
		r.res.Changes = append(r.res.Changes, Change{Kind: kind, Title: title, What: what})
	}
}

func (r *run) count(name string, n int) {
	if n > 0 {
		r.res.Counts[name] += n
	}
}

// Run imports everything of `username` for the user and returns what it did.
// It does not touch the incremental marker or the source status; see Sync.
func (im *Importer) Run(ctx context.Context, userID int64, username string, opts Options) (*Result, error) {
	anime, manga, err := im.fetchLists(ctx, username)
	if err != nil {
		return nil, err
	}
	r := &run{
		im: im, userID: userID, opts: opts, now: im.now(), loc: im.loc(),
		res:      &Result{DryRun: opts.DryRun, Counts: map[string]int{}, Warnings: []string{}},
		bulk:     map[dateKey]bool{},
		expected: map[int64]map[string]bool{}, placed: map[int64]map[string]bool{},
		shows: map[int64]catalog.Item{},
	}

	shared := map[dateKey]int{}
	for _, e := range anime {
		if e.CompletedAt.Year != 0 {
			shared[e.CompletedAt.key()]++
		}
	}
	for k, n := range shared {
		if n > BulkDateLimit {
			r.bulk[k] = true
		}
	}

	type placement struct {
		e entry
		m *mappings.Mapping
	}
	var fresh []placement
	for _, e := range anime {
		if !r.isNew(e) {
			continue
		}
		m, err := r.mapping(ctx, e.Media)
		if err != nil {
			r.warn("%s: %v", e.title(), err)
			continue
		}
		fresh = append(fresh, placement{e, m})
		if m != nil && m.TvdbID.Valid && !(e.Media.Format == "MOVIE" && m.TmdbMovie.Valid) {
			if _, ok := startOf(seasonOf(m), int(m.TvdbOffset), e.Media.Format); ok {
				add(r.expected, m.TvdbID.Int64, e.Status)
			}
		}
	}
	for _, p := range fresh {
		placed, err := r.importAnime(ctx, p.e, p.m)
		if err == nil && !placed {
			err = r.syncCount(ctx, p.e, catalog.KindAnime)
			r.count("anime (unmapped)", 1)
		}
		if err != nil { // one bad entry mustn't stop the import
			slog.WarnContext(ctx, "anilist: import failed for entry", "title", p.e.title(), "err", err)
			r.warn("%s: %v", p.e.title(), err)
		}
	}
	for _, e := range manga {
		if !r.isNew(e) {
			continue
		}
		if err := r.syncCount(ctx, e, catalog.KindManga); err != nil {
			slog.WarnContext(ctx, "anilist: import failed for manga", "title", e.title(), "err", err)
			r.warn("%s: %v", e.title(), err)
			continue
		}
		r.count("manga", 1)
	}
	r.finishShows(ctx)
	r.res.Latest = r.latest
	return r.res, nil
}

func add(m map[int64]map[string]bool, id int64, status string) {
	if m[id] == nil {
		m[id] = map[string]bool{}
	}
	m[id][status] = true
}

// seasonOf is the TVDB season the mapping puts the cour in (nil: not given).
func seasonOf(m *mappings.Mapping) *int {
	if !m.TvdbSeason.Valid {
		return nil
	}
	s := int(m.TvdbSeason.Int64)
	return &s
}

// isNew tracks the newest edit of the list and reports whether the entry is
// newer than the incremental marker.
func (r *run) isNew(e entry) bool {
	if e.UpdatedAt == 0 {
		return true
	}
	stamp := time.Unix(e.UpdatedAt, 0).UTC()
	if stamp.After(r.latest) {
		r.latest = stamp
	}
	return r.opts.Since == nil || stamp.After(*r.opts.Since)
}

// mapping finds the Fribb row of a media by AniList id, then MAL id.
func (r *run) mapping(ctx context.Context, m media) (*mappings.Mapping, error) {
	row, err := r.im.Mappings.Lookup(ctx, "anilist", strconv.FormatInt(m.ID, 10))
	if err != nil || row != nil {
		return row, err
	}
	if m.IDMal != nil && *m.IDMal != 0 {
		return r.im.Mappings.Lookup(ctx, "mal", strconv.FormatInt(*m.IDMal, 10))
	}
	return nil, nil
}

// importAnime places an entry as a TVDB show or a TMDB film; false if it can't be placed.
func (r *run) importAnime(ctx context.Context, e entry, m *mappings.Mapping) (bool, error) {
	if m == nil {
		return false, nil
	}
	if e.Media.Format == "MOVIE" && m.TmdbMovie.Valid {
		return true, r.importMovie(ctx, e, m.TmdbMovie.Int64)
	}
	if m.TvdbID.Valid {
		ok, err := r.importShow(ctx, e, m)
		if err != nil || ok {
			return ok, err
		}
	}
	if m.TmdbMovie.Valid {
		return true, r.importMovie(ctx, e, m.TmdbMovie.Int64)
	}
	return false, nil
}

func watchedCount(e entry) int {
	n := e.Progress
	if e.Status == "COMPLETED" && e.Media.Episodes != nil && *e.Media.Episodes > n {
		n = *e.Media.Episodes
	}
	return n
}

func isWatching(status string) bool { return status == "CURRENT" || status == "REPEATING" }

func ensureItem(ctx context.Context, cat *catalog.Catalog, kind, id string) (catalog.Item, error) {
	source := catalog.SourceTVDB
	if kind == catalog.KindAnimeMovie {
		source = catalog.SourceTMDB // films are TMDB
	}
	return cat.EnsureItem(ctx, kind, source, id)
}

// importShow marks the entry's episodes in its TVDB series; false if TVDB
// lacks the season. The mapping sometimes names a season TVDB has since merged
// or renumbered; such entries are better kept as plain anime than misplaced.
func (r *run) importShow(ctx context.Context, e entry, m *mappings.Mapping) (bool, error) {
	start, ok := startOf(seasonOf(m), int(m.TvdbOffset), e.Media.Format)
	if !ok {
		return false, nil
	}
	tvID := m.TvdbID.Int64
	item, err := ensureItem(ctx, r.im.Catalog, catalog.KindAnime, strconv.FormatInt(tvID, 10))
	if err != nil {
		return false, err
	}
	if err := r.im.Catalog.SyncSeasons(ctx, item); err != nil {
		return false, err
	}
	rows, err := r.im.Q.ListItemSeasons(ctx, item.ID)
	if err != nil {
		return false, err
	}
	seasons := map[int]int{}
	for _, s := range rows {
		seasons[int(s.Number)] = int(s.EpisodeCount)
	}
	if _, ok := seasons[start.Season]; !ok {
		slog.InfoContext(ctx, "anilist: TVDB series has no such season", "tvdb", tvID, "season", start.Season, "title", e.title())
		return false, nil
	}
	claimed := map[int]bool{}
	others, err := r.im.Mappings.ByTVDB(ctx, tvID)
	if err != nil {
		return false, err
	}
	for _, o := range others {
		if o.ID != m.ID && o.TvdbSeason.Valid && int(o.TvdbSeason.Int64) != start.Season {
			claimed[int(o.TvdbSeason.Int64)] = true
		}
	}
	count := watchedCount(e)
	slots := episodeSlots(start, seasons, count, claimed)
	if count > 0 && len(slots) == 0 {
		slog.InfoContext(ctx, "anilist: no TVDB episodes for entry", "tvdb", tvID, "title", e.title())
		return false, nil
	}
	add(r.placed, tvID, e.Status)
	r.shows[tvID] = item

	entryRow, err := r.im.Library.CurrentEntry(ctx, r.userID, item.ID)
	hasEntry := err == nil
	if err != nil && !errors.Is(err, library.ErrNoEntry) {
		return false, err
	}
	if !hasEntry {
		status := library.Planning
		if st := r.expected[tvID]; len(st) > 0 && onlyPausedDropped(st) {
			status = library.Dropped
			if st["PAUSED"] {
				status = library.Paused
			}
		}
		r.change("show", item.Title, "start tracking as "+status)
		if !r.opts.DryRun {
			if entryRow, err = r.im.Library.Track(ctx, r.userID, item.ID, status, Source, r.now); err != nil {
				return false, err
			}
			hasEntry = true
		}
	}

	bySeason := map[int][]int{}
	var order []int
	for _, s := range slots {
		if _, ok := bySeason[s.Season]; !ok {
			order = append(order, s.Season)
		}
		bySeason[s.Season] = append(bySeason[s.Season], s.Position)
	}
	watched := map[[2]int64]bool{}
	if r.opts.DryRun && hasEntry {
		ws, err := r.im.Q.ListEntryWatches(ctx, entryRow.ID)
		if err != nil {
			return false, err
		}
		for _, w := range ws {
			watched[[2]int64{w.Season.Int64, w.Episode.Int64}] = true
		}
	}
	for _, season := range order {
		positions := bySeason[season]
		if r.opts.DryRun {
			eps, err := r.im.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: item.ID, Season: int64(season)})
			if err != nil {
				return false, err
			}
			n := 0
			for _, p := range positions {
				if p < 1 || p > len(eps) {
					continue
				}
				key := [2]int64{int64(season), eps[p-1].Number}
				if !watched[key] {
					watched[key] = true
					n++
				}
			}
			if n > 0 {
				r.change("show", item.Title, fmt.Sprintf("watch %d episodes of season %d", n, season))
			}
			r.count("episodes", n)
			continue
		}
		at := func(ep db.ItemEpisode) time.Time { return r.watchDate(e, parseDay(ep.AirDate.String)) }
		n, err := r.im.Library.WatchPositions(ctx, r.userID, item.ID, season, positions, at, Source)
		r.count("episodes", n)
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

func onlyPausedDropped(statuses map[string]bool) bool {
	for s := range statuses {
		if s != "PAUSED" && s != "DROPPED" {
			return false
		}
	}
	return true
}

// finishShows moves each touched show to In progress when AniList has an
// entry of it being watched; every other status follows from the episodes.
func (r *run) finishShows(ctx context.Context) {
	for tvID, statuses := range r.placed {
		item := r.shows[tvID]
		r.count("shows", 1)
		if r.opts.DryRun {
			continue
		}
		watching := false
		for s := range statuses {
			watching = watching || isWatching(s)
		}
		if !watching {
			continue
		}
		if err := r.im.Library.SetStatus(ctx, r.userID, item.ID, library.InProgress, Source, r.now); err != nil {
			r.warn("%s: %v", item.Title, err)
		}
	}
}

// importMovie records an anime film. A completed entry is a play dated by the
// date rules (the film's start date is its "air date").
func (r *run) importMovie(ctx context.Context, e entry, tmdbID int64) error {
	item, err := ensureItem(ctx, r.im.Catalog, catalog.KindAnimeMovie, strconv.FormatInt(tmdbID, 10))
	if err != nil {
		return err
	}
	done := e.Status == "COMPLETED"
	status := library.Planning
	switch {
	case done:
		status = library.Completed
	case isWatching(e.Status):
		status = library.InProgress
	}
	cur, err := r.im.Library.CurrentEntry(ctx, r.userID, item.ID)
	hasEntry := err == nil
	if err != nil && !errors.Is(err, library.ErrNoEntry) {
		return err
	}
	if r.opts.DryRun {
		switch {
		case !hasEntry:
			r.change("movie", item.Title, "add as "+status)
			r.count("movies", 1)
		case done && cur.Status != library.Completed:
			r.change("movie", item.Title, "complete")
		case !done && statusRank(status) > statusRank(cur.Status):
			r.change("movie", item.Title, "move to "+status)
		}
		return nil
	}
	if done {
		res, err := r.im.Library.WatchMovie(ctx, r.userID, item.ID, r.watchDate(e, e.Media.StartDate.day()), Source, library.WatchOptions{Import: true})
		if err != nil {
			return err
		}
		if res.EntryCreated {
			r.count("movies", 1)
		}
		return nil
	}
	changed, err := r.im.Library.SyncEntry(ctx, r.userID, item.ID, library.SyncValues{Status: status}, Source, r.now)
	if err != nil {
		return err
	}
	if changed && !hasEntry {
		r.count("movies", 1)
	}
	return nil
}

func statusRank(s string) int {
	switch s {
	case library.Planning:
		return 0
	case library.InProgress:
		return 1
	case library.Completed:
		return 2
	}
	return -1
}

// status maps an AniList status to ours.
func status(s string) string {
	switch s {
	case "CURRENT", "REPEATING":
		return library.InProgress
	case "COMPLETED":
		return library.Completed
	case "PAUSED":
		return library.Paused
	case "DROPPED":
		return library.Dropped
	}
	return library.Planning
}

// countItem finds or creates the count-based item of an entry. Like the
// Python importer it keeps MAL ids when the media has one and uses the list's
// title and cover. A dry run reports a missing item as nil.
func (r *run) countItem(ctx context.Context, e entry, kind string) (*catalog.Item, error) {
	source, id := catalog.SourceAniList, strconv.FormatInt(e.Media.ID, 10)
	if e.Media.IDMal != nil && *e.Media.IDMal != 0 {
		source, id = catalog.SourceMAL, strconv.FormatInt(*e.Media.IDMal, 10)
	}
	item, err := r.im.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: id, Kind: kind})
	if err == nil {
		return &item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if r.opts.DryRun {
		return nil, nil
	}
	total := e.Media.Chapters
	if kind == catalog.KindAnime {
		total = e.Media.Episodes
	}
	params := db.InsertItemParams{
		Kind: kind, Source: source, ExternalID: id, Title: e.title(), Image: e.Media.CoverImage.Large, Meta: "{}",
	}
	if total != nil && *total > 0 {
		params.Total = sql.NullInt64{Int64: int64(*total), Valid: true}
	}
	if e.Media.StartDate.Year != 0 {
		params.Year = sql.NullInt64{Int64: int64(e.Media.StartDate.Year), Valid: true}
	}
	item, err = r.im.Q.InsertItem(ctx, params)
	if err != nil {
		// Lost a race with another creator: use theirs.
		if existing, gerr := r.im.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: id, Kind: kind}); gerr == nil {
			return &existing, nil
		}
		return nil, err
	}
	// Both ids resolve to the item.
	ids := [][2]string{{catalog.SourceAniList, strconv.FormatInt(e.Media.ID, 10)}}
	if e.Media.IDMal != nil && *e.Media.IDMal != 0 {
		ids = append(ids, [2]string{catalog.SourceMAL, strconv.FormatInt(*e.Media.IDMal, 10)})
	}
	for _, k := range ids {
		if err := r.im.Q.UpsertItemID(ctx, db.UpsertItemIDParams{ItemID: item.ID, Source: k[0], ExternalID: k[1]}); err != nil {
			return nil, err
		}
	}
	return &item, nil
}

// syncCount applies a list entry to a count-based item (unmapped anime, manga).
func (r *run) syncCount(ctx context.Context, e entry, kind string) error {
	item, err := r.countItem(ctx, e, kind)
	if err != nil {
		return err
	}
	v := library.SyncValues{
		Status:     status(e.Status),
		Notes:      e.Notes,
		Progress:   e.Progress,
		StartedAt:  e.StartedAt.midnight(r.loc),
		FinishedAt: e.CompletedAt.midnight(r.loc),
	}
	if e.Score > 0 { // AniList scores unrated media as 0
		s := e.Score
		v.Score = &s
	}
	total := e.Media.Chapters
	if kind == catalog.KindAnime {
		total = e.Media.Episodes
	}
	if e.Status == "COMPLETED" && total != nil && *total > v.Progress {
		v.Progress = *total
	}
	at := r.now
	if e.UpdatedAt != 0 {
		at = time.Unix(e.UpdatedAt, 0).UTC()
	}
	if r.opts.DryRun {
		var cur *db.Entry
		if item != nil {
			if row, err := r.im.Library.CurrentEntry(ctx, r.userID, item.ID); err == nil {
				cur = &row
			} else if !errors.Is(err, library.ErrNoEntry) {
				return err
			}
		}
		if what := wouldSync(cur, v); what != "" {
			r.change(kind, e.title(), what)
		}
		return nil
	}
	_, err = r.im.Library.SyncEntry(ctx, r.userID, item.ID, v, Source, at)
	return err
}

// wouldSync describes what SyncEntry would do to an entry (nil = none yet).
func wouldSync(cur *db.Entry, v library.SyncValues) string {
	if cur == nil {
		return "add as " + v.Status
	}
	var what []string
	if statusRank(v.Status) > statusRank(cur.Status) && statusRank(cur.Status) >= 0 {
		what = append(what, "move to "+v.Status)
	}
	if int64(v.Progress) > cur.Progress {
		what = append(what, "progress to "+strconv.Itoa(v.Progress))
	}
	if v.Score != nil && !cur.Score.Valid {
		what = append(what, "fill score")
	}
	if v.Notes != "" && cur.Notes == "" {
		what = append(what, "fill notes")
	}
	if v.StartedAt != nil && !cur.StartedAt.Valid {
		what = append(what, "fill start date")
	}
	return strings.Join(what, ", ")
}

// MarkerKey is the kv key of the incremental marker of a user.
func MarkerKey(userID int64) string { return fmt.Sprintf("anilist:%d:updated_at", userID) }

// Marker returns the stored incremental marker, nil when there is none.
func (im *Importer) Marker(ctx context.Context, userID int64) *time.Time {
	v, err := im.Q.GetKV(ctx, MarkerKey(userID))
	if err != nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

// SyncOptions tune Sync.
type SyncOptions struct {
	// SinceLast limits the run to entries edited since the last run.
	SinceLast bool
	// DryRun returns what would change and writes nothing (no marker, no status).
	DryRun bool
}

// Sync runs the import for a user the way the scheduler does: it reads the
// incremental marker (SinceLast), imports, advances the marker and records
// the Sources status. A dry run does none of the last two.
func (im *Importer) Sync(ctx context.Context, userID int64, username string, opts SyncOptions) (*Result, error) {
	run := Options{DryRun: opts.DryRun}
	if opts.SinceLast {
		run.Since = im.Marker(ctx, userID)
	}
	res, err := im.Run(ctx, userID, username, run)
	if opts.DryRun {
		return res, err
	}
	at := im.now()
	if err != nil {
		im.writeStatus(ctx, userID, sourcestatus.New(SourceName, at, nil, nil, err.Error(), false))
		return nil, err
	}
	marker := res.Latest
	if run.Since != nil && run.Since.After(marker) {
		marker = *run.Since // the marker only moves forward
	}
	if !marker.IsZero() {
		if werr := im.Q.SetKV(ctx, db.SetKVParams{Key: MarkerKey(userID), Value: marker.Format(time.RFC3339), UpdatedAt: at.Format(time.RFC3339)}); werr != nil {
			slog.ErrorContext(ctx, "anilist: store marker", "err", werr)
		}
	}
	msg := ""
	if len(res.Warnings) > 0 {
		msg = fmt.Sprintf("%d entries had problems", len(res.Warnings))
	}
	im.writeStatus(ctx, userID, sourcestatus.New(SourceName, at, res.Counts, res.Warnings, msg, len(res.Warnings) == 0))
	return res, nil
}

func (im *Importer) writeStatus(ctx context.Context, userID int64, s sourcestatus.Status) {
	// Fresh context: the status should land even when the run was cancelled.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := sourcestatus.Write(wctx, im.Q, userID, s); err != nil {
		slog.ErrorContext(ctx, "anilist: write source status", "err", err)
	}
}

// ErrNotConfigured means the user is not in ANILIST_USERS.
var ErrNotConfigured = errors.New("anilist: user is not mapped to an AniList account")

// AniListName returns the AniList name mapped to an Encompass user.
func (im *Importer) AniListName(username string) (string, bool) {
	for _, p := range im.Users {
		if strings.EqualFold(p.Local, username) {
			return p.External, true
		}
	}
	return "", false
}

// syncUser syncs one Encompass user (by id) from their mapped AniList account.
func (im *Importer) syncUser(ctx context.Context, userID int64, opts SyncOptions) (*Result, error) {
	u, err := im.Q.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	name, ok := im.AniListName(u.Username)
	if !ok {
		return nil, ErrNotConfigured
	}
	return im.Sync(ctx, u.ID, name, opts)
}

// RegisterJobs schedules anilist.sync every 30 minutes for each ANILIST_USERS
// mapping (incremental), and handles one-off "anilist.sync" jobs
// ({"full": true} re-reads everything), which the Sources page enqueues.
func (im *Importer) RegisterJobs(m *jobs.Manager) {
	if len(im.Users) > 0 {
		m.Every("anilist.sync", 30*time.Minute, func(ctx context.Context) error {
			var errs []error
			for _, p := range im.Users {
				u, err := im.Q.GetUserByUsername(ctx, p.Local)
				if err != nil {
					errs = append(errs, fmt.Errorf("user %q: %w", p.Local, err))
					continue
				}
				if _, err := im.Sync(ctx, u.ID, p.External, SyncOptions{SinceLast: true}); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		})
	}
	m.Handle("anilist.sync", func(ctx context.Context, job jobs.Job) (string, error) {
		var p struct {
			Full bool `json:"full"`
		}
		_ = json.Unmarshal(job.Payload, &p)
		res, err := im.syncUser(ctx, job.UserID, SyncOptions{SinceLast: !p.Full})
		if errors.Is(err, ErrNotConfigured) {
			return "", nil // nothing to retry
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", res.Counts), nil
	})
}

// SyncUser is Sync for an Encompass user id, resolving the AniList name from
// ANILIST_USERS (used by the API's dry run).
func (im *Importer) SyncUser(ctx context.Context, userID int64, opts SyncOptions) (*Result, error) {
	return im.syncUser(ctx, userID, opts)
}

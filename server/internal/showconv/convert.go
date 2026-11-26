package showconv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
	"github.com/the-agency-labs/encompass/server/internal/providers/tvdb"
)

// Statuses of a season, as the library names them.
const (
	statusPlanning   = "Planning"
	statusInProgress = "In progress"
	statusPaused     = "Paused"
	statusDropped    = "Dropped"
	statusCompleted  = "Completed"
)

// EpisodeLinker finds the TVDB episode id TMDB records for an episode.
type EpisodeLinker interface {
	EpisodeTVDBID(ctx context.Context, showID string, season, episode int) (string, error)
}

// Converter converts TMDB shows to TVDB. Create it with New.
type Converter struct {
	Conn    *sql.DB
	Q       *db.Queries
	Catalog *catalog.Catalog
	// Linker (optional) asks TMDB for episode links when dates cannot settle one.
	Linker EpisodeLinker
	// Today is "today" in the library's zone (YYYY-MM-DD), for released episodes.
	Today func() string
	// Progress, when set, receives one line per show as it is processed.
	Progress func(format string, args ...any)
}

// New returns a Converter on a catalog (which must have the TVDB client).
func New(conn *sql.DB, q *db.Queries, cat *catalog.Catalog) *Converter {
	return &Converter{Conn: conn, Q: q, Catalog: cat, Today: cat.Today}
}

// Options tune Run.
type Options struct {
	// DryRun computes and reports, and writes nothing to the library (the
	// provider cache still fills).
	DryRun bool
}

// SeasonCount is a show's season counts around the conversion.
type SeasonCount struct {
	// Before and After are the regular seasons the catalog lists (TMDB, TVDB).
	Before, After int
	// EntryBefore and EntryAfter are the season rows the entries had and have.
	EntryBefore, EntryAfter int
}

// ShowResult is what happened to one show.
type ShowResult struct {
	Title    string
	TMDBID   string
	TVDBID   string
	Via      string // how the TVDB series was found
	Kind     string
	Item     int64 // the TVDB item
	Merged   bool  // the TVDB item already existed
	Seasons  SeasonCount
	Entries  int
	Watches  int
	Methods  map[string]int // how watches were placed
	Unmapped []Unmapped
	Notes    []string
	// Changed lists entries whose progress moved: user, run, before, after.
	Changed []ProgressChange
}

// ProgressChange is an entry whose episode progress differs after the conversion.
type ProgressChange struct {
	Entry         int64
	Run           int64
	Before, After int64
}

// Unmapped is a watch that had no twin and was kept on the closest episode.
type Unmapped struct {
	Show        string
	Watch       int64
	Entry       int64
	FromSeason  int
	FromEpisode int
	ToSeason    int
	ToEpisode   int
	Reason      string
	Source      string
}

// Kept is a show that stays on TMDB.
type Kept struct {
	Title, TMDBID, Reason string
}

// Report is the outcome of a run.
type Report struct {
	DryRun        bool
	Converted     []ShowResult
	Kept          []Kept
	WatchesBefore int64
	WatchesAfter  int64
	// People: how TVDB people were resolved, and how many people no credit
	// refers to any more were removed.
	People        catalog.PeopleStats
	PeopleRemoved int64
	CreditErrors  []string
	// StatusChanges are entries whose status moved (there should be none).
	StatusChanges []string
}

func (cv *Converter) progress(format string, args ...any) {
	if cv.Progress != nil {
		cv.Progress(format, args...)
	}
}

// Run converts every TMDB show. It is idempotent: shows already on TVDB are
// not touched, and a second run finds nothing left to convert but the shows
// that have no TVDB match.
func (cv *Converter) Run(ctx context.Context, opts Options) (*Report, error) {
	rep := &Report{DryRun: opts.DryRun}
	if cv.Catalog.Reg.P.TVDB == nil {
		return nil, errors.New("TVDB is not configured (set TVDB_API)")
	}
	before, err := cv.Q.CountEpisodeWatches(ctx)
	if err != nil {
		return nil, err
	}
	rep.WatchesBefore = before
	catalog.TakePeopleStats()

	items, err := cv.Q.ListShowItemsBySource(ctx, catalog.SourceTMDB)
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		cv.progress("[%d/%d] %s", i+1, len(items), item.Title)
		res, kept, err := cv.convertOne(ctx, item, opts, rep)
		switch {
		case err != nil:
			slog.WarnContext(ctx, "shows-to-tvdb: show failed", "title", item.Title, "err", err)
			rep.Kept = append(rep.Kept, Kept{item.Title, item.ExternalID, "conversion failed, rolled back: " + err.Error()})
		case kept != "":
			rep.Kept = append(rep.Kept, Kept{item.Title, item.ExternalID, kept})
		default:
			for _, n := range res.Notes {
				if strings.Contains(n, "status changed") {
					rep.StatusChanges = append(rep.StatusChanges, item.Title+": "+n)
				}
			}
			rep.Converted = append(rep.Converted, *res)
		}
	}
	after, err := cv.Q.CountEpisodeWatches(ctx)
	if err != nil {
		return rep, err
	}
	rep.WatchesAfter = after
	if opts.DryRun {
		rep.WatchesAfter = rep.WatchesBefore
	}
	if !opts.DryRun {
		rep.People = catalog.TakePeopleStats()
		n, err := cv.Q.DeleteOrphanPeople(ctx)
		if err != nil {
			return rep, err
		}
		rep.PeopleRemoved = n
	}
	return rep, nil
}

// resolveTVDB finds the TVDB series of a TMDB show: the TVDB id the item
// already knows (its stored ids), else the one TMDB lists for it, else the
// Fribb mapping's, else TVDB's own lookup of the show's IMDb id. Every
// candidate must exist on TVDB with episodes.
func (cv *Converter) resolveTVDB(ctx context.Context, item db.Item) (id, via string, err error) {
	tvdbAPI := cv.Catalog.Reg.P.TVDB
	var cands [][2]string
	add := func(v, how string) {
		v = strings.TrimSpace(v)
		if v != "" && v != "0" {
			cands = append(cands, [2]string{v, how})
		}
	}
	ids, _ := cv.Q.ListItemIDs(ctx, item.ID)
	var stored []string
	var imdb string
	for _, r := range ids {
		switch r.Source {
		case "tvdb":
			stored = append(stored, r.ExternalID)
		case "imdb":
			imdb = r.ExternalID
		}
	}
	var meta struct {
		TVDB any `json:"tvdb_id"`
	}
	_ = json.Unmarshal([]byte(item.Meta), &meta)
	tmdbListed := numToString(meta.TVDB)
	if tmdbListed == "" && cv.Catalog.Reg.P.TMDB != nil {
		if m, err := cv.Catalog.Reg.P.TMDB.TV(ctx, item.ExternalID); err == nil {
			tmdbListed = numToString(m["tvdb_id"])
		}
	}
	switch len(stored) {
	case 1:
		add(stored[0], "stored ids")
	default:
		for _, s := range stored {
			if s == tmdbListed {
				add(s, "stored ids (as TMDB lists)")
			}
		}
	}
	add(tmdbListed, "TMDB external ids")
	if cv.Catalog.Reg.Mappings != nil {
		if n, perr := strconv.ParseInt(item.ExternalID, 10, 64); perr == nil {
			if rows, merr := cv.Catalog.Reg.Mappings.ByTMDBTV(ctx, n); merr == nil {
				distinct := map[int64]bool{}
				for _, m := range rows {
					if m.TvdbID.Valid {
						distinct[m.TvdbID.Int64] = true
					}
				}
				if len(distinct) == 1 {
					for v := range distinct {
						add(strconv.FormatInt(v, 10), "anime mapping")
					}
				}
			}
		}
	}
	if imdb != "" {
		if hit, herr := tvdbAPI.FindByRemoteID(ctx, imdb); herr == nil && hit != nil {
			add(strconv.FormatInt(hit.SeriesID, 10), "TVDB lookup of the IMDb id")
		}
	}
	tried := map[string]bool{}
	for _, c := range cands {
		if tried[c[0]] {
			continue
		}
		tried[c[0]] = true
		seasons, serr := tvdbAPI.Seasons(ctx, c[0])
		if serr != nil {
			if providersNotFound(serr) {
				continue
			}
			return "", "", serr
		}
		if len(seasons) > 0 {
			return c[0], c[1], nil
		}
	}
	return "", "", nil
}

func providersNotFound(err error) bool { return providers.NotFound(err) }

func numToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x != 0 {
			return strconv.FormatInt(int64(x), 10)
		}
	}
	return ""
}

func epsOf(rows []db.ItemEpisode) []Ep {
	out := make([]Ep, 0, len(rows))
	for _, r := range rows {
		out = append(out, Ep{Season: int(r.Season), Number: int(r.Number), Title: r.Title, Air: r.AirDate.String, ExtID: r.ExtID})
	}
	return out
}

func tvdbEps(seasons []tvdb.Season) []Ep {
	var out []Ep
	for _, s := range seasons {
		seen := map[int]bool{}
		for _, e := range s.Episodes {
			if seen[e.Number] {
				continue
			}
			seen[e.Number] = true
			out = append(out, Ep{Season: s.Number, Number: e.Number, Title: e.Name, Air: e.Aired, ExtID: strconv.FormatInt(e.ID, 10)})
		}
	}
	return out
}

// convertOne converts a show. kept is a reason when the show stays on TMDB.
func (cv *Converter) convertOne(ctx context.Context, item db.Item, opts Options, rep *Report) (*ShowResult, string, error) {
	tvdbID, via, err := cv.resolveTVDB(ctx, item)
	if err != nil {
		return nil, "", err
	}
	if tvdbID == "" {
		return nil, "no TVDB series found (no TVDB id stored, none listed by TMDB, none in the anime mapping, IMDb not found)", nil
	}
	seasons, err := cv.Catalog.Reg.P.TVDB.Seasons(ctx, tvdbID)
	if err != nil {
		return nil, "", err
	}
	res := &ShowResult{Title: item.Title, TMDBID: item.ExternalID, TVDBID: tvdbID, Via: via, Methods: map[string]int{}}

	// The source episodes: what the catalog stored from TMDB.
	srcRows, err := cv.Q.ListItemEpisodes(ctx, item.ID)
	if err != nil {
		return nil, "", err
	}
	if len(srcRows) == 0 && !opts.DryRun {
		if err := cv.Catalog.SyncSeasons(ctx, item, catalog.Force); err == nil {
			srcRows, _ = cv.Q.ListItemEpisodes(ctx, item.ID)
		}
	}
	srcSeasons, _ := cv.Q.ListItemSeasons(ctx, item.ID)
	for _, s := range srcSeasons {
		if s.Number > 0 && s.EpisodeCount > 0 {
			res.Seasons.Before++
		}
	}
	for _, s := range seasons {
		if s.Number > 0 {
			res.Seasons.After++
		}
	}
	src, dst := epsOf(srcRows), tvdbEps(seasons)

	entries, err := cv.Q.ListItemEntries(ctx, item.ID)
	if err != nil {
		return nil, "", err
	}
	res.Entries = len(entries)

	watched := map[Key]bool{}
	type entryWatches struct {
		entry   db.Entry
		watches []db.Watch
		seasons []db.EntrySeason
	}
	all := make([]entryWatches, 0, len(entries))
	for _, e := range entries {
		ws, err := cv.Q.ListEntryWatches(ctx, e.ID)
		if err != nil {
			return nil, "", err
		}
		ss, err := cv.Q.ListEntrySeasons(ctx, e.ID)
		if err != nil {
			return nil, "", err
		}
		for _, w := range ws {
			if w.Season.Valid && w.Episode.Valid {
				watched[Key{int(w.Season.Int64), int(w.Episode.Int64)}] = true
			}
		}
		res.Seasons.EntryBefore += len(ss)
		all = append(all, entryWatches{e, ws, ss})
	}

	var link Linker
	if cv.Linker != nil {
		link = func(season, number int) string {
			id, err := cv.Linker.EpisodeTVDBID(ctx, item.ExternalID, season, number)
			if err != nil {
				return ""
			}
			return id
		}
	}
	align := Align(src, dst, func(k Key) bool { return watched[k] }, link)
	srcByKey := map[Key]Ep{}
	for _, e := range src {
		srcByKey[Key{e.Season, e.Number}] = e
	}

	// Plan every entry: where each watch goes, the seasons and the progress.
	type plannedWatch struct {
		w        db.Watch
		toS, toE int64
		notes    string
	}
	type plannedEntry struct {
		entry    db.Entry
		watches  []plannedWatch
		seasons  []db.EntrySeason
		progress int64
	}
	dstSeason := map[int][]db.ItemEpisode{}
	for _, e := range dst {
		dstSeason[e.Season] = append(dstSeason[e.Season], db.ItemEpisode{
			Season: int64(e.Season), Number: int64(e.Number), Position: int64(len(dstSeason[e.Season]) + 1),
			AirDate: nullStr(e.Air),
		})
	}
	today := cv.Today()
	var plan []plannedEntry
	for _, ew := range all {
		pe := plannedEntry{entry: ew.entry}
		moved := map[Key]bool{}
		for _, w := range ew.watches {
			pw := plannedWatch{w: w, toS: w.Season.Int64, toE: w.Episode.Int64, notes: w.Notes}
			if w.Season.Valid && w.Episode.Valid {
				k := Key{int(w.Season.Int64), int(w.Episode.Int64)}
				if l, ok := align[k]; ok {
					pw.toS, pw.toE = int64(l.To[0]), int64(l.To[1])
					res.Methods[l.Method]++
				} else if to, how, ok := closest(k[0], k[1], srcByKey[k].Air, dst); ok {
					pw.toS, pw.toE = int64(to[0]), int64(to[1])
					reason := "TMDB lists no such episode"
					if _, listed := srcByKey[k]; listed {
						reason = "no TVDB twin found"
					}
					reason += "; placed on the " + how
					pw.notes = appendNote(w.Notes, fmt.Sprintf("Converted from TMDB S%02dE%02d (%s)", k[0], k[1], reason))
					res.Methods["fallback"]++
					res.Unmapped = append(res.Unmapped, Unmapped{
						Show: item.Title, Watch: w.ID, Entry: ew.entry.ID, FromSeason: k[0], FromEpisode: k[1],
						ToSeason: to[0], ToEpisode: to[1], Reason: reason, Source: w.Source,
					})
				} else {
					res.Methods["kept as is"]++
					res.Unmapped = append(res.Unmapped, Unmapped{
						Show: item.Title, Watch: w.ID, Entry: ew.entry.ID, FromSeason: k[0], FromEpisode: k[1],
						ToSeason: k[0], ToEpisode: k[1], Reason: "TVDB lists no episodes; kept as is", Source: w.Source,
					})
				}
				moved[Key{int(pw.toS), int(pw.toE)}] = true
			}
			pe.watches = append(pe.watches, pw)
		}
		pe.seasons = planSeasons(ew.seasons, ew.watches, moved, dstSeason, align, srcByKey, today)
		for k := range moved {
			if k[0] > 0 {
				pe.progress++
			}
		}
		plan = append(plan, pe)
		res.Watches += len(ew.watches)
		res.Seasons.EntryAfter += len(pe.seasons)
		if pe.progress != ew.entry.Progress {
			res.Changed = append(res.Changed, ProgressChange{Entry: ew.entry.ID, Run: ew.entry.Run, Before: ew.entry.Progress, After: pe.progress})
		}
	}
	if opts.DryRun {
		return res, "", nil
	}
	applyPlan := make([]applyEntry, 0, len(plan))
	for _, pe := range plan {
		ae := applyEntry{entry: pe.entry, seasons: pe.seasons, progress: pe.progress}
		for _, w := range pe.watches {
			ae.watches = append(ae.watches, applyWatch{id: w.w.ID, season: w.w.Season, episode: w.w.Episode, toS: w.toS, toE: w.toE, notes: w.notes})
		}
		applyPlan = append(applyPlan, ae)
	}
	return cv.apply(ctx, item, res, tvdbID, applyPlan)
}

func appendNote(existing, note string) string {
	if existing == "" {
		return note
	}
	return existing + "\n" + note
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

type applyWatch struct {
	id       int64
	season   sql.NullInt64
	episode  sql.NullInt64
	toS, toE int64
	notes    string
}

type applyEntry struct {
	entry    db.Entry
	watches  []applyWatch
	seasons  []db.EntrySeason
	progress int64
}

// apply writes one show's conversion in a transaction: the TVDB item (made
// first, outside it), the entries moved onto it, the watches re-placed, the
// season rows rebuilt, lists and reading links moved, and the TMDB item gone.
func (cv *Converter) apply(ctx context.Context, old db.Item, res *ShowResult, tvdbID string, plan []applyEntry) (*ShowResult, string, error) {
	_, existed := cv.itemExists(ctx, tvdbID)
	target, err := cv.Catalog.EnsureItem(ctx, old.Kind, catalog.SourceTVDB, tvdbID)
	if err != nil {
		return nil, "", fmt.Errorf("create TVDB item: %w", err)
	}
	res.Merged, res.Item, res.Kind = existed, target.ID, target.Kind
	if err := cv.Catalog.SyncSeasons(ctx, target, catalog.Force); err != nil {
		return nil, "", fmt.Errorf("sync TVDB seasons: %w", err)
	}

	tx, err := cv.Conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback() }()
	q := cv.Q.WithTx(tx)

	if old.KindOverride == 1 && target.Kind != old.Kind {
		if err := q.UpdateItemKind(ctx, db.UpdateItemKindParams{Kind: old.Kind, ID: target.ID}); err != nil {
			return nil, "", err
		}
		res.Kind = old.Kind
	}
	// Ids of the old item stay on the new one, so old links and matching still work.
	ids, err := q.ListItemIDs(ctx, old.ID)
	if err != nil {
		return nil, "", err
	}
	for _, r := range ids {
		if r.Source == "tvdb" && r.ExternalID != tvdbID {
			continue // another series' id (a multi-part anime): not this item's
		}
		if err := q.UpsertItemID(ctx, db.UpsertItemIDParams{ItemID: target.ID, Source: r.Source, ExternalID: r.ExternalID}); err != nil {
			return nil, "", err
		}
	}
	if err := q.UpsertItemID(ctx, db.UpsertItemIDParams{ItemID: target.ID, Source: catalog.SourceTMDB, ExternalID: old.ExternalID}); err != nil {
		return nil, "", err
	}

	statusBefore := map[int64]string{}
	for _, pe := range plan {
		e := pe.entry
		statusBefore[e.ID] = e.Status
		run := e.Run
		maxRun, err := q.MaxEntryRun(ctx, db.MaxEntryRunParams{UserID: e.UserID, ItemID: target.ID})
		if err != nil {
			return nil, "", err
		}
		if m := anyInt(maxRun); m > 0 {
			// The user already tracks the TVDB series (or another TMDB item
			// that maps to it): keep both, the moved entry as the next run.
			run = m + 1
			res.Notes = append(res.Notes, fmt.Sprintf("entry %d joined an existing TVDB entry as run %d", e.ID, run))
		}
		if err := q.MoveEntry(ctx, db.MoveEntryParams{ItemID: target.ID, Run: run, Progress: pe.progress, ID: e.ID}); err != nil {
			return nil, "", err
		}
		for _, w := range pe.watches {
			if !w.season.Valid || !w.episode.Valid {
				continue
			}
			if err := q.UpdateWatchEpisode(ctx, db.UpdateWatchEpisodeParams{
				Season: sql.NullInt64{Int64: w.toS, Valid: true}, Episode: sql.NullInt64{Int64: w.toE, Valid: true},
				Notes: w.notes, ID: w.id,
			}); err != nil {
				return nil, "", err
			}
		}
		if err := q.DeleteEntrySeasons(ctx, e.ID); err != nil {
			return nil, "", err
		}
		for _, s := range pe.seasons {
			if err := q.UpsertEntrySeason(ctx, db.UpsertEntrySeasonParams{
				EntryID: e.ID, Season: s.Season, Status: s.Status, Score: s.Score, Notes: s.Notes,
			}); err != nil {
				return nil, "", err
			}
		}
	}
	if err := q.MoveReadingDocuments(ctx, db.MoveReadingDocumentsParams{ItemID: sql.NullInt64{Int64: target.ID, Valid: true}, ItemID_2: sql.NullInt64{Int64: old.ID, Valid: true}}); err != nil {
		return nil, "", err
	}
	if err := q.MoveReadingSessions(ctx, db.MoveReadingSessionsParams{ItemID: sql.NullInt64{Int64: target.ID, Valid: true}, ItemID_2: sql.NullInt64{Int64: old.ID, Valid: true}}); err != nil {
		return nil, "", err
	}
	// The TMDB credits go with the item; the TVDB ones are fetched below.
	if err := q.DeleteItem(ctx, old.ID); err != nil {
		return nil, "", fmt.Errorf("remove TMDB item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}

	// Entry statuses must be what they were.
	for id, before := range statusBefore {
		if e, err := cv.Q.GetEntry(ctx, id); err == nil && e.Status != before {
			res.Notes = append(res.Notes, fmt.Sprintf("entry %d status changed %s -> %s", id, before, e.Status))
		}
	}
	// Credits and releases of the TVDB item (outside the transaction: they
	// call TVDB). A failure leaves them for the periodic backfill.
	if err := cv.Catalog.FetchCredits(ctx, target, true); err != nil {
		res.Notes = append(res.Notes, "credits not fetched yet: "+err.Error())
	}
	if err := cv.Catalog.RefreshReleases(ctx, target); err != nil {
		res.Notes = append(res.Notes, "releases not built yet: "+err.Error())
	}
	return res, "", nil
}

func anyInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func (cv *Converter) itemExists(ctx context.Context, tvdbID string) (db.Item, bool) {
	for _, k := range []string{catalog.KindTV, catalog.KindAnime} {
		if it, err := cv.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: catalog.SourceTVDB, ExternalID: tvdbID, Kind: k}); err == nil {
			return it, true
		}
	}
	return db.Item{}, false
}

// planSeasons rebuilds an entry's season rows on the TVDB seasons from its
// remapped watches: a season with watches is Completed when every released
// episode is watched and nothing is still to air, else In progress (unless
// the user had paused or dropped the TMDB season it came from); a season
// row the entry had without watches (Planning, say) moves to the TVDB season
// of the episode it started at. Scores and notes follow the first TVDB season
// of the TMDB season they were set on.
func planSeasons(old []db.EntrySeason, watches []db.Watch, moved map[Key]bool, dst map[int][]db.ItemEpisode, align map[Key]Link, src map[Key]Ep, today string) []db.EntrySeason {
	bySeason := map[int]map[int]bool{}
	for k := range moved {
		if bySeason[k[0]] == nil {
			bySeason[k[0]] = map[int]bool{}
		}
		bySeason[k[0]][k[1]] = true
	}
	// Which old season each new one came from (the TMDB season of most of its watches).
	origin := map[int]map[int]int{}
	for _, w := range watches {
		if !w.Season.Valid || !w.Episode.Valid {
			continue
		}
		k := Key{int(w.Season.Int64), int(w.Episode.Int64)}
		to := k
		if l, ok := align[k]; ok {
			to = l.To
		} else if _, listed := src[k]; !listed {
			continue
		}
		if origin[to[0]] == nil {
			origin[to[0]] = map[int]int{}
		}
		origin[to[0]][k[0]]++
	}
	oldBy := map[int]db.EntrySeason{}
	for _, s := range old {
		oldBy[int(s.Season)] = s
	}
	out := map[int]db.EntrySeason{}
	var numbers []int
	for n := range bySeason {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		eps := dst[n]
		status := statusInProgress
		if len(eps) > 0 {
			rel := catalog.ReleasedEpisodes(eps, today)
			allWatched, unreleased := true, false
			for _, e := range eps {
				if rel[e.Number] {
					if !bySeason[n][int(e.Number)] {
						allWatched = false
					}
				} else {
					unreleased = true
				}
			}
			if allWatched && !unreleased {
				status = statusCompleted
			}
		}
		if status != statusCompleted {
			// Keep a pause or drop the user chose on the season it came from.
			best, bestN := 0, -1
			for from, c := range origin[n] {
				if c > bestN {
					best, bestN = from, c
				}
			}
			if o, ok := oldBy[best]; ok && (o.Status == statusPaused || o.Status == statusDropped) {
				status = o.Status
			}
		}
		out[n] = db.EntrySeason{Season: int64(n), Status: status}
	}
	// What the old season rows hold beyond what watches derive (a score, notes,
	// a Planning row with no watches) moves to the TVDB season of the episode
	// the old season started at.
	for _, o := range old {
		first, ok := firstTarget(int(o.Season), align, src)
		if !ok {
			first = int(o.Season)
		}
		cur, has := out[first]
		if !has {
			cur = db.EntrySeason{Season: int64(first), Status: o.Status}
		}
		if o.Score.Valid && !cur.Score.Valid {
			cur.Score = o.Score
		}
		if o.Notes != "" && cur.Notes == "" {
			cur.Notes = o.Notes
		}
		out[first] = cur
	}
	var rows []db.EntrySeason
	for _, s := range out {
		rows = append(rows, s)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Season < rows[j].Season })
	return rows
}

// firstTarget is the TVDB season of the first episode (lowest number) of an
// old season that the alignment placed.
func firstTarget(season int, align map[Key]Link, src map[Key]Ep) (int, bool) {
	found, best := false, 0
	var to int
	for k := range src {
		if k[0] != season {
			continue
		}
		l, ok := align[k]
		if !ok {
			continue
		}
		if !found || k[1] < best {
			found, best, to = true, k[1], l.To[0]
		}
	}
	return to, found
}

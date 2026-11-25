package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Series storage and sourcing.
//
// Hardcover is the primary source: a book lists the series it is in (first =
// primary) and each series lists all of its books. Open Library only has
// free-text strings on an edition ("Red Rising Saga #2"), used as a fallback
// for Open Library books. Names are normalised so every book of a series
// shows the same name.

// Series refresh and backfill tuning.
const (
	SeriesRefreshAfter   = 30 * 24 * time.Hour // a stored Hardcover series is re-read after this
	SeriesFetchFresh     = 24 * time.Hour      // on-demand fetches reuse a series refreshed this recently
	SeriesBackfillLimit  = 20                  // books asked per backfill run
	SeriesRefreshLimit   = 10                  // stale series re-read per backfill run
	seriesCheckRetryGap  = 29 * 24 * time.Hour // a failed check is back-dated so it is retried tomorrow
	seriesBackfillHour   = 4
	seriesBackfillMinute = 30
)

// JobSeriesBackfill is the name of the daily series backfill job.
const JobSeriesBackfill = "catalog.series_backfill"

// SourceSeriesOpenLibrary is the source of series built from Open Library
// edition strings; their external id is the name key.
const SourceSeriesOpenLibrary = SourceOpenLibrary

// HardcoverSeries is the series slice of the Hardcover client. The catalog
// uses it when the configured Hardcover client implements it (the real one
// does).
type HardcoverSeries interface {
	BookSeries(ctx context.Context, bookID string) ([]providers.BookSeries, error)
	Series(ctx context.Context, seriesID string) (providers.SeriesInfo, error)
	SearchSeries(ctx context.Context, query string) ([]providers.SeriesHit, error)
}

// OpenLibrarySeries is the series slice of the Open Library client.
type OpenLibrarySeries interface {
	EditionSeries(ctx context.Context, editionID string) ([]string, error)
}

// ItemSeries is the primary series of a book as stored in items.meta["series"].
type ItemSeries struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Position float64 `json:"position"`
}

// ItemSeriesFromMeta reads the primary series out of an item's meta JSON, nil
// when the item has none.
func ItemSeriesFromMeta(meta string) *ItemSeries {
	var m struct {
		Series *ItemSeries `json:"series"`
	}
	if json.Unmarshal([]byte(meta), &m) != nil || m.Series == nil || m.Series.ID == 0 {
		return nil
	}
	return m.Series
}

var (
	// "The Red Rising Trilogy" -> "Red Rising"; "The Wheel of Time" is kept.
	seriesKindRe = regexp.MustCompile(`(?i)^(?:the\s+)?(.*?\S)\s+(?:trilogy|series|saga|duology|quartet|quintet|cycle|sequence|chronicles|books|novels)$`)
	// "Red Rising Saga #2", "Dune, #3"
	seriesHashRe = regexp.MustCompile(`(?i)^(.*?\S)\s*(?:[,;:(–-]\s*)?#\s*(\d{1,3}(?:\.\d)?)\)?$`)
	// "The Red Rising Trilogy, Book 2", "Mistborn ; v. 1", "Dune (Vol. 3)"
	seriesWordRe = regexp.MustCompile(`(?i)^(.*?\S)(?:\s*[,;:(–-]\s*|\s+)(?:book|bk\.?|vol(?:ume)?\.?|v\.|no\.?|part)\s*(\d{1,3}(?:\.\d)?)\)?$`)
	// "Dune, 3", "Dune (3)": a bare number needs a separator, "Fahrenheit 451" is a title
	seriesBareRe = regexp.MustCompile(`^(.*?\S)\s*[,;(]\s*(\d{1,3}(?:\.\d)?)\)?$`)
	nonAlnumRe   = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// A name with a subtitle: "The Mistborn Saga: The Original Trilogy",
// "Discworld - Ankh-Morpork City Watch".
var seriesSubtitleRe = regexp.MustCompile(`^(.+?)(\s*:\s+|\s+[-–—]\s+)(.+)$`)

// seriesModifiers are generic one-word qualifiers: "The Original Trilogy" is
// a name, "Original" alone is not, so the packaging word stays with them.
var seriesModifiers = map[string]bool{
	"original": true, "complete": true, "first": true, "second": true, "third": true, "new": true,
	"early": true, "final": true, "classic": true, "modern": true, "young": true, "next": true,
	"old": true, "whole": true, "entire": true, "full": true, "great": true, "later": true,
}

// stripSeriesKind drops the packaging word of a name ("The Red Rising
// Trilogy" -> "Red Rising"). It does not when what is left is not a name:
// a lone generic modifier ("The Original Trilogy"), or, in a subtitle (the
// part after "Foo: "), anything under two words ("Era One Series" is fine,
// "Trilogy" after "Mistborn: The Original" is the distinguishing part).
func stripSeriesKind(s string, subtitle bool) string {
	m := seriesKindRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	words := strings.Fields(m[1])
	if len(words) == 1 && seriesModifiers[strings.ToLower(words[0])] || subtitle && len(words) < 2 {
		if subtitle {
			return strings.TrimSpace(seriesArticleRe.ReplaceAllString(s, ""))
		}
		return s
	}
	return m[1]
}

var seriesArticleRe = regexp.MustCompile(`(?i)^the\s+`)

// NormalizeSeriesName drops packaging words, so every book of a series shows
// the same name: "The Red Rising Trilogy" and "Red Rising" are both "Red Rising".
// Both halves of "Mistborn Saga: The Original Trilogy" are treated alone,
// giving "Mistborn: Original Trilogy", never a dangling "Mistborn: The Original".
func NormalizeSeriesName(s string) string {
	s = strings.Trim(strings.TrimSpace(s), ",;:-–")
	s = strings.TrimSpace(s)
	if m := seriesSubtitleRe.FindStringSubmatch(s); m != nil {
		sep := m[2]
		if strings.Contains(sep, ":") {
			sep = ": "
		}
		return stripSeriesKind(m[1], false) + sep + stripSeriesKind(m[3], true)
	}
	return stripSeriesKind(s, false)
}

// SeriesKey is the comparison key of a series name: normalised, lower-cased,
// without a leading "the" or punctuation.
func SeriesKey(name string) string {
	k := strings.ToLower(NormalizeSeriesName(name))
	k = strings.TrimSpace(nonAlnumRe.ReplaceAllString(k, " "))
	return strings.TrimSpace(strings.TrimPrefix(k+" ", "the "))
}

// ParseSeriesString splits an Open Library series string such as "Red Rising
// Saga #2" or "The Red Rising Trilogy, Book 2" into the normalised series
// name and the position (0 when the string has none).
func ParseSeriesString(s string) (name string, position float64) {
	s = strings.TrimSpace(s)
	for _, re := range []*regexp.Regexp{seriesHashRe, seriesWordRe, seriesBareRe} {
		if m := re.FindStringSubmatch(s); m != nil {
			position, _ = strconv.ParseFloat(m[2], 64)
			return NormalizeSeriesName(m[1]), position
		}
	}
	return NormalizeSeriesName(s), 0
}

func (c *Catalog) hardcoverSeries() HardcoverSeries {
	h, _ := c.Reg.P.Hardcover.(HardcoverSeries)
	return h
}

func (c *Catalog) openLibrarySeries() OpenLibrarySeries {
	o, _ := c.Reg.P.OpenLibrary.(OpenLibrarySeries)
	return o
}

func nullInt(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: n > 0} }

// bookRef identifies one book row of a series.
type bookRef struct{ source, id string }

// bookItemID finds the book item that has the id (as its primary id or any
// of its item_ids), 0 when there is none.
func bookItemID(ctx context.Context, q *db.Queries, source, id string) int64 {
	items, err := q.FindItemsByExternalID(ctx, db.FindItemsByExternalIDParams{Source: source, ExternalID: id})
	if err != nil {
		return 0
	}
	for _, it := range items {
		if it.Kind == KindBook {
			return it.ID
		}
	}
	return 0
}

// storeSeries saves a Hardcover series and all of its books, linking each to
// the book item that has its id. Books the provider no longer lists are
// removed.
func (c *Catalog) storeSeries(ctx context.Context, info providers.SeriesInfo) (db.Series, error) {
	var out db.Series
	err := c.tx(ctx, func(q *db.Queries) error {
		name := NormalizeSeriesName(info.Name)
		s, err := q.UpsertSeries(ctx, db.UpsertSeriesParams{
			Source: SourceHardcover, ExternalID: info.ExternalID, Name: name, NameKey: SeriesKey(name),
			Slug: info.Slug, BooksCount: int64(info.BooksCount), RefreshedAt: Stamp(c.now()),
		})
		if err != nil {
			return err
		}
		out = s
		keep := make([]string, 0, len(info.Books))
		for _, b := range info.Books {
			keep = append(keep, b.ExternalID)
		}
		keepJSON, _ := json.Marshal(keep)
		if err := q.DeleteSeriesBooksNotIn(ctx, db.DeleteSeriesBooksNotInParams{
			SeriesID: s.ID, Source: SourceHardcover, Keep: string(keepJSON),
		}); err != nil {
			return err
		}
		for _, b := range info.Books {
			var item sql.NullInt64
			if id := bookItemID(ctx, q, SourceHardcover, b.ExternalID); id != 0 {
				item = sql.NullInt64{Int64: id, Valid: true}
			}
			if err := q.UpsertSeriesBook(ctx, db.UpsertSeriesBookParams{
				SeriesID: s.ID, Position: b.Position, ItemID: item, Source: SourceHardcover, ExternalID: b.ExternalID,
				Title: b.Title, Image: cleanImage(b.Image), Year: nullInt(b.Year),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return db.Series{}, err
	}
	c.applySeriesMeta(ctx, out.ID)
	return out, nil
}

// SyncItemSeries reads the series of a book from its provider (Hardcover
// first; Open Library edition strings for Open Library books) and stores it,
// marking the first as the book's primary series. Existing series rows that
// list the book are linked to it. A provider that is not configured does
// nothing; a provider error is returned (and the check is retried tomorrow).
func (c *Catalog) SyncItemSeries(ctx context.Context, item Item) error {
	if item.Kind != KindBook {
		return nil
	}
	if err := c.linkItemSeriesBooks(ctx, item); err != nil {
		return err
	}
	var err error
	switch {
	case item.Source == SourceHardcover && c.hardcoverSeries() != nil:
		err = c.syncHardcoverItemSeries(ctx, item)
	case item.Source == SourceOpenLibrary && c.openLibrarySeries() != nil:
		err = c.syncOpenLibraryItemSeries(ctx, item)
	default:
		return nil
	}
	check := c.now()
	if err != nil && !providers.NotFound(err) {
		check = check.Add(-seriesCheckRetryGap)
	}
	if cerr := c.Q.SetItemSeriesCheck(ctx, db.SetItemSeriesCheckParams{ItemID: item.ID, CheckedAt: Stamp(check)}); cerr != nil && err == nil {
		err = cerr
	}
	c.applyItemSeriesMeta(ctx, item.ID)
	if providers.NotFound(err) {
		return nil
	}
	return err
}

// linkItemSeriesBooks points the series rows that list this book at its item.
func (c *Catalog) linkItemSeriesBooks(ctx context.Context, item Item) error {
	ids, err := c.ItemIDs(ctx, item.ID)
	if err != nil {
		return err
	}
	for source, list := range ids {
		for _, id := range list {
			if err := c.Q.LinkSeriesBook(ctx, db.LinkSeriesBookParams{
				ItemID: sql.NullInt64{Int64: item.ID, Valid: true}, Source: source, ExternalID: id,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Catalog) syncHardcoverItemSeries(ctx context.Context, item Item) error {
	list, err := c.hardcoverSeries().BookSeries(ctx, item.ExternalID)
	if err != nil {
		return err
	}
	ids := make([]int64, len(list)) // stored series id per list entry; 0 = skipped
	seen := map[string]bool{}
	for i, bs := range list {
		// Hardcover often has a saga and a sub-series with the same name
		// once normalised ("Red Rising Saga", "Red Rising"): keep the first
		// (featured) one so the series shows once.
		key := SeriesKey(bs.Series.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		s, err := c.storeSeries(ctx, bs.Series)
		if err != nil {
			return err
		}
		// The book itself may be missing from the list (e.g. no position there).
		if err := c.Q.UpsertSeriesBook(ctx, db.UpsertSeriesBookParams{
			SeriesID: s.ID, Position: bs.Position, ItemID: sql.NullInt64{Int64: item.ID, Valid: true},
			Source: SourceHardcover, ExternalID: item.ExternalID, Title: item.Title, Image: item.Image, Year: item.Year,
		}); err != nil {
			return err
		}
		ids[i] = s.ID
	}
	var primary int64
	if i := choosePrimarySeries(list, ids); i >= 0 {
		primary = ids[i]
	}
	if primary != 0 {
		return c.Q.SetItemPrimarySeries(ctx, db.SetItemPrimarySeriesParams{
			SeriesID: primary, ItemID: sql.NullInt64{Int64: item.ID, Valid: true},
		})
	}
	return nil
}

func (c *Catalog) syncOpenLibraryItemSeries(ctx context.Context, item Item) error {
	strs, err := c.openLibrarySeries().EditionSeries(ctx, item.ExternalID)
	if err != nil {
		return err
	}
	var primary int64
	seen := map[string]bool{}
	for _, str := range strs {
		name, pos := ParseSeriesString(str)
		key := SeriesKey(name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		s, err := c.openLibrarySeriesRow(ctx, name, key)
		if err != nil {
			return err
		}
		itemID := sql.NullInt64{Int64: item.ID, Valid: true}
		claimed := int64(0)
		if pos > 0 {
			claimed, err = c.Q.LinkSeriesBookAtPosition(ctx, db.LinkSeriesBookAtPositionParams{ItemID: itemID, SeriesID: s.ID, Position: pos})
			if err != nil {
				return err
			}
		}
		if claimed == 0 {
			if err := c.Q.UpsertSeriesBook(ctx, db.UpsertSeriesBookParams{
				SeriesID: s.ID, Position: pos, ItemID: itemID, Source: item.Source, ExternalID: item.ExternalID,
				Title: item.Title, Image: item.Image, Year: item.Year,
			}); err != nil {
				return err
			}
		}
		if primary == 0 {
			primary = s.ID
		}
	}
	if primary != 0 {
		return c.Q.SetItemPrimarySeries(ctx, db.SetItemPrimarySeriesParams{
			SeriesID: primary, ItemID: sql.NullInt64{Int64: item.ID, Valid: true},
		})
	}
	return nil
}

// openLibrarySeriesRow finds the series with that name (whatever its source)
// or creates one from the Open Library strings.
func (c *Catalog) openLibrarySeriesRow(ctx context.Context, name, key string) (db.Series, error) {
	if s, err := c.Q.FindSeriesByNameKey(ctx, key); err == nil {
		return s, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return db.Series{}, err
	}
	return c.Q.UpsertSeries(ctx, db.UpsertSeriesParams{
		Source: SourceSeriesOpenLibrary, ExternalID: strings.ReplaceAll(key, " ", "-"), Name: name, NameKey: key,
		RefreshedAt: Stamp(c.now()),
	})
}

// applySeriesMeta refreshes the series copy in items.meta of every book of a series.
func (c *Catalog) applySeriesMeta(ctx context.Context, seriesID int64) {
	ids, err := c.Q.ListSeriesItemIDs(ctx, seriesID)
	if err != nil {
		slog.WarnContext(ctx, "catalog: series meta", "series", seriesID, "err", err)
		return
	}
	for _, id := range ids {
		c.applyItemSeriesMeta(ctx, id.Int64)
	}
}

// applyItemSeriesMeta copies the primary series of a book into items.meta
// ("series": {id, name, position}) so item JSON can show it without a join.
// Provider refreshes rewrite meta, so this runs after every refresh too.
func (c *Catalog) applyItemSeriesMeta(ctx context.Context, itemID int64) {
	item, err := c.Q.GetItem(ctx, itemID)
	if err != nil {
		return
	}
	meta := map[string]any{}
	if json.Unmarshal([]byte(item.Meta), &meta) != nil {
		meta = map[string]any{}
	}
	_, had := meta["series"]
	p, err := c.Q.GetItemPrimarySeries(ctx, sql.NullInt64{Int64: itemID, Valid: true})
	switch {
	case err == nil:
		meta["series"] = ItemSeries{ID: p.ID, Name: p.Name, Position: p.Position}
	case errors.Is(err, sql.ErrNoRows):
		if !had {
			return
		}
		delete(meta, "series")
	default:
		slog.WarnContext(ctx, "catalog: item series", "item", itemID, "err", err)
		return
	}
	b, err := json.Marshal(meta)
	if err == nil && string(b) != item.Meta {
		if err := c.Q.UpdateItemMeta(ctx, db.UpdateItemMetaParams{Meta: string(b), ID: itemID}); err != nil {
			slog.WarnContext(ctx, "catalog: item series meta", "item", itemID, "err", err)
		}
	}
}

// syncBookSeries is the hook of EnsureItem and RefreshItem: best effort, a
// provider failure never fails the caller. It returns the item as stored
// afterwards (its meta carries the series).
func (c *Catalog) syncBookSeries(ctx context.Context, item Item) Item {
	if item.Kind != KindBook {
		return item
	}
	if err := c.SyncItemSeries(ctx, item); err != nil {
		slog.WarnContext(ctx, "catalog: book series", "item", item.ID, "err", err)
	}
	if fresh, err := c.Q.GetItem(ctx, item.ID); err == nil {
		return fresh
	}
	return item
}

// FetchSeries reads a series from its provider by (source, external id),
// stores it and returns the stored row. A series refreshed within
// SeriesFetchFresh is returned as is, and a provider failure falls back to
// the stored copy when there is one. Only Hardcover series can be fetched.
func (c *Catalog) FetchSeries(ctx context.Context, source, externalID string) (db.Series, error) {
	local, lerr := c.Q.GetSeriesByKey(ctx, db.GetSeriesByKeyParams{Source: source, ExternalID: externalID})
	if lerr != nil && !errors.Is(lerr, sql.ErrNoRows) {
		return db.Series{}, lerr
	}
	have := lerr == nil
	if have {
		if t, err := time.Parse(time.RFC3339, local.RefreshedAt); err == nil && c.now().Sub(t) < SeriesFetchFresh {
			return local, nil
		}
	}
	h := c.hardcoverSeries()
	if source != SourceHardcover || h == nil {
		if have {
			return local, nil
		}
		return db.Series{}, fmt.Errorf("%w: series from %s", ErrUnsupported, source)
	}
	info, err := h.Series(ctx, externalID)
	if err != nil {
		if have {
			slog.WarnContext(ctx, "catalog: series refresh failed, using stored copy", "series", local.ID, "err", err)
			return local, nil
		}
		if providers.NotFound(err) {
			return db.Series{}, ErrNotFound
		}
		return db.Series{}, err
	}
	info.ExternalID = externalID
	return c.storeSeries(ctx, info)
}

// SearchRemoteSeries searches Hardcover series by name; empty when Hardcover
// is not configured.
func (c *Catalog) SearchRemoteSeries(ctx context.Context, query string) ([]providers.SeriesHit, error) {
	h := c.hardcoverSeries()
	if h == nil {
		return nil, nil
	}
	return h.SearchSeries(ctx, query)
}

// BackfillSeries asks up to limit books that have no series data yet, links
// series rows to books that appeared since, and re-reads Hardcover series not
// refreshed for SeriesRefreshAfter. It returns the books asked.
func (c *Catalog) BackfillSeries(ctx context.Context, limit int) (int, error) {
	if err := c.Q.LinkAllSeriesBooks(ctx); err != nil {
		return 0, err
	}
	items, err := c.Q.ListBooksNeedingSeries(ctx, db.ListBooksNeedingSeriesParams{
		Cutoff: Stamp(c.now().Add(-SeriesRefreshAfter)), Max: int64(limit),
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if err := c.SyncItemSeries(ctx, it); err != nil {
			slog.WarnContext(ctx, "series backfill", "item", it.ID, "err", err)
			continue
		}
		n++
	}
	stale, err := c.Q.ListStaleSeries(ctx, db.ListStaleSeriesParams{
		RefreshedAt: Stamp(c.now().Add(-SeriesRefreshAfter)), Limit: SeriesRefreshLimit,
	})
	if err != nil {
		return n, err
	}
	h := c.hardcoverSeries()
	for _, s := range stale {
		if h == nil {
			break
		}
		info, err := h.Series(ctx, s.ExternalID)
		if err != nil {
			slog.WarnContext(ctx, "series refresh", "series", s.ID, "err", err)
			continue
		}
		info.ExternalID = s.ExternalID
		if _, err := c.storeSeries(ctx, info); err != nil {
			slog.WarnContext(ctx, "series refresh", "series", s.ID, "err", err)
		}
	}
	return n, c.Q.LinkAllSeriesBooks(ctx)
}

// registerSeriesJobs schedules the daily series backfill.
func (c *Catalog) registerSeriesJobs(m *jobs.Manager) {
	m.Daily(JobSeriesBackfill, seriesBackfillHour, seriesBackfillMinute, func(ctx context.Context) error {
		n, err := c.BackfillSeries(ctx, SeriesBackfillLimit)
		slog.InfoContext(ctx, "series backfill", "books", n)
		return err
	})
}

// choosePrimarySeries picks the book's main series among the stored entries
// (ids[i] != 0), in Hardcover's order:
//   - a reading-order or umbrella list ("… Universe", "(Publication Order)")
//     is never primary while a regular series exists;
//   - a sub-arc named after its own first book whose books all belong to
//     another regular series of the book defers to that larger series
//     ("Iron Gold" inside "Red Rising Saga" → Red Rising 4, not Iron Gold 1),
//     while a series like "The Hunger Games" (named after its first book but
//     contained in no other regular series) stays primary.
func choosePrimarySeries(list []providers.BookSeries, ids []int64) int {
	var regular []int
	for i, bs := range list {
		if ids[i] != 0 && !providers.IsMetaSeries(bs.Series.Name) {
			regular = append(regular, i)
		}
	}
	if len(regular) == 0 {
		for i := range list {
			if ids[i] != 0 {
				return i
			}
		}
		return -1
	}
	first := regular[0]
	if isSubArc(list[first].Series) {
		for _, j := range regular[1:] {
			if containsAllBooks(list[j].Series, list[first].Series) {
				return j
			}
		}
	}
	return first
}

// isSubArc reports whether a series is named after its own first book.
func isSubArc(s providers.SeriesInfo) bool {
	var firstTitle string
	lowest := math.Inf(1)
	for _, b := range s.Books {
		if b.Position >= 1 && b.Position < lowest {
			lowest, firstTitle = b.Position, b.Title
		}
	}
	return firstTitle != "" && SeriesKey(firstTitle) == SeriesKey(s.Name)
}

// containsAllBooks reports whether every book of sub is also in parent, and
// parent is larger.
func containsAllBooks(parent, sub providers.SeriesInfo) bool {
	if len(parent.Books) <= len(sub.Books) || len(sub.Books) == 0 {
		return false
	}
	in := map[string]bool{}
	for _, b := range parent.Books {
		in[b.ExternalID] = true
	}
	for _, b := range sub.Books {
		if !in[b.ExternalID] {
			return false
		}
	}
	return true
}

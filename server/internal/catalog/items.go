package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// noImagePrefix starts the placeholder URL every provider uses for "no image".
const noImagePrefix = "https://www.themoviedb.org/assets/2/v4/glyphicons"

func validKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// lookupKinds are the kinds an item of (kind, source) may be stored under:
// a TMDB title is a tv/anime show or a movie/anime_movie film, whichever the
// classification (or the user) decided.
func lookupKinds(kind, source string) []string {
	if source == SourceTVDB {
		return []string{KindTV, KindAnime}
	}
	if source == SourceTMDB {
		switch kind {
		case KindTV, KindAnime:
			return []string{KindTV, KindAnime}
		case KindMovie, KindAnimeMovie:
			return []string{KindMovie, KindAnimeMovie}
		}
	}
	return []string{kind}
}

// classify picks tv vs anime (or movie vs anime_movie) for a TMDB title: the
// mapping decides, unless the caller explicitly asked for anime on a title
// the mapping doesn't know, which is kept as anime and marked overridden.
func (c *Catalog) classify(ctx context.Context, kind, source, id string) (resolved string, override bool) {
	if source == SourceTVDB && IsShow(kind) {
		switch anime := c.Reg.IsAnimeTVDB(ctx, id); {
		case anime:
			return KindAnime, false
		case kind == KindAnime:
			return kind, true
		}
		return KindTV, false
	}
	if source != SourceTMDB || !(IsShow(kind) || IsMovie(kind)) {
		return kind, false
	}
	film := IsMovie(kind)
	anime := c.Reg.IsAnime(ctx, film, id)
	switch {
	case anime && film:
		return KindAnimeMovie, false
	case anime:
		return KindAnime, false
	case kind == KindAnime || kind == KindAnimeMovie:
		return kind, true
	case film:
		return KindMovie, false
	}
	return KindTV, false
}

// EnsureItem returns the item for a provider title, creating it from the
// provider's metadata when it doesn't exist yet. kind is a hint for TMDB
// titles: the anime mapping decides tv vs anime (and movie vs anime_movie)
// unless the item's kind was set by the user (kind_override). Asking for
// anime on a TMDB show the mapping doesn't know creates it as anime and
// marks the kind as overridden.
func (c *Catalog) EnsureItem(ctx context.Context, kind, source, externalID string) (Item, error) {
	if !validKind(kind) {
		return Item{}, fmt.Errorf("%w: kind %q", ErrUnsupported, kind)
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return Item{}, errors.New("catalog: empty external id")
	}
	if IsShow(kind) {
		source, externalID = c.ResolveShowRef(ctx, source, externalID)
	}
	for _, k := range lookupKinds(kind, source) {
		item, err := c.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: externalID, Kind: k})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Item{}, err
		}
		if item.KindOverride == 0 && (source == SourceTMDB || source == SourceTVDB) {
			if want, _ := c.classify(ctx, item.Kind, source, externalID); want != item.Kind {
				if err := c.Q.UpdateItemKindAuto(ctx, db.UpdateItemKindAutoParams{Kind: want, ID: item.ID}); err != nil {
					slog.WarnContext(ctx, "catalog: reclassify failed", "item", item.ID, "to", want, "err", err)
				} else {
					item.Kind = want
				}
			}
		}
		return item, nil
	}

	resolved, override := c.classify(ctx, kind, source, externalID)
	meta, err := c.Reg.Metadata(ctx, resolved, source, externalID)
	if err != nil {
		return Item{}, err
	}
	f := describe(resolved, meta)
	var item Item
	err = c.tx(ctx, func(q *db.Queries) error {
		var err error
		item, err = q.InsertItem(ctx, db.InsertItemParams{
			Kind: resolved, Source: source, ExternalID: externalID,
			Title: f.title, NativeTitle: f.native, Image: f.image, Year: f.year,
			Synopsis: f.synopsis, Total: f.total, KindOverride: boolInt(override), Meta: f.meta,
			RefreshedAt: sql.NullString{String: Stamp(c.now()), Valid: true},
		})
		if err != nil {
			return err
		}
		return c.storeIDs(ctx, q, item, meta)
	})
	if err != nil {
		// Lost a race with another creator: use theirs.
		if existing, gerr := c.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: externalID, Kind: resolved}); gerr == nil {
			return existing, nil
		}
		return Item{}, err
	}
	return c.syncBookSeries(ctx, item), nil
}

// RefreshItem re-reads the provider metadata of an item and updates its row
// and ids.
func (c *Catalog) RefreshItem(ctx context.Context, item Item) (Item, error) {
	meta, err := c.Reg.Metadata(ctx, item.Kind, item.Source, item.ExternalID)
	if err != nil {
		return item, err
	}
	f := describe(item.Kind, meta)
	err = c.tx(ctx, func(q *db.Queries) error {
		if err := q.UpdateItemMetadata(ctx, db.UpdateItemMetadataParams{
			Title: f.title, NativeTitle: f.native, Image: f.image, Year: f.year, Synopsis: f.synopsis,
			Total: f.total, Meta: f.meta, RefreshedAt: sql.NullString{String: Stamp(c.now()), Valid: true}, ID: item.ID,
		}); err != nil {
			return err
		}
		return c.storeIDs(ctx, q, item, meta)
	})
	if err != nil {
		return item, err
	}
	item = c.syncBookSeries(ctx, item)
	return c.GetItem(ctx, item.ID)
}

// FindByExternalID finds an item by any of its known ids (tmdb, tvdb, imdb,
// mal, anilist, ...). With kinds, only items of those kinds match. A match
// whose primary source is the one asked about wins. ErrNotFound when none.
func (c *Catalog) FindByExternalID(ctx context.Context, source, id string, kinds ...string) (Item, error) {
	items, err := c.Q.FindItemsByExternalID(ctx, db.FindItemsByExternalIDParams{Source: source, ExternalID: id})
	if err != nil {
		return Item{}, err
	}
	for _, it := range items {
		if len(kinds) == 0 {
			return it, nil
		}
		for _, k := range kinds {
			if it.Kind == k {
				return it, nil
			}
		}
	}
	return Item{}, ErrNotFound
}

// ItemIDs returns every known id of an item as source -> ids.
func (c *Catalog) ItemIDs(ctx context.Context, itemID int64) (map[string][]string, error) {
	rows, err := c.Q.ListItemIDs(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, r := range rows {
		out[r.Source] = append(out[r.Source], r.ExternalID)
	}
	return out, nil
}

// storeIDs records every id we know for an item: its own, the ids TMDB
// reports (tvdb, imdb), and the MAL/AniList ids of the anime mapping.
func (c *Catalog) storeIDs(ctx context.Context, q *db.Queries, item Item, meta Metadata) error {
	ids := map[[2]string]bool{{item.Source, item.ExternalID}: true}
	add := func(source string, id any) {
		if s := idString(id); s != "" && s != "0" {
			ids[[2]string{source, s}] = true
		}
	}
	if item.Source == SourceTVDB {
		// Cross ids are for matching (Jellyfin, AniList, old links) only.
		remote := asMap(meta["remote_ids"])
		add("tmdb", remote["tmdb"])
		add("imdb", remote["imdb"])
		add("tvdb", item.ExternalID)
	}
	if item.Source == SourceTMDB {
		add("tvdb", meta["tvdb_id"])
		links := asMap(meta["external_links"])
		if imdb, ok := links["IMDb"].(string); ok {
			add("imdb", lastPathSegment(imdb))
		}
		if tvdb, ok := links["TVDB"].(string); ok && meta["tvdb_id"] == nil {
			add("tvdb", lastPathSegment(tvdb))
		}
	}
	if c.Reg.Mappings != nil && (IsShow(item.Kind) || IsMovie(item.Kind)) {
		n, _ := strconv.ParseInt(item.ExternalID, 10, 64)
		switch {
		case item.Source == SourceTVDB && n != 0:
			rows, err := c.Reg.Mappings.ByTVDB(ctx, n)
			if err != nil {
				return err
			}
			for _, m := range rows {
				add("mal", nullID(m.MalID))
				add("anilist", nullID(m.AnilistID))
			}
		case item.Source == SourceTMDB && n != 0:
			var err error
			var rows []mappingRow
			if IsMovie(item.Kind) {
				rows, err = c.mappingRows(ctx, true, n)
			} else {
				rows, err = c.mappingRows(ctx, false, n)
			}
			if err != nil {
				return err
			}
			for _, m := range rows {
				add("mal", m.mal)
				add("anilist", m.anilist)
				add("tvdb", m.tvdb)
			}
		case item.Source == SourceMAL || item.Source == SourceAniList:
			m, err := c.Reg.Mappings.Lookup(ctx, item.Source, item.ExternalID)
			if err != nil {
				return err
			}
			if m != nil {
				add("mal", nullID(m.MalID))
				add("anilist", nullID(m.AnilistID))
			}
		}
	}
	for k := range ids {
		if err := q.UpsertItemID(ctx, db.UpsertItemIDParams{ItemID: item.ID, Source: k[0], ExternalID: k[1]}); err != nil {
			return err
		}
	}
	return nil
}

type mappingRow struct{ mal, anilist, tvdb any }

func (c *Catalog) mappingRows(ctx context.Context, film bool, tmdbID int64) ([]mappingRow, error) {
	var out []mappingRow
	if film {
		rows, err := c.Reg.Mappings.ByTMDBMovie(ctx, tmdbID)
		if err != nil {
			return nil, err
		}
		for _, m := range rows {
			out = append(out, mappingRow{nullID(m.MalID), nullID(m.AnilistID), nullID(m.TvdbID)})
		}
		return out, nil
	}
	rows, err := c.Reg.Mappings.ByTMDBTV(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	for _, m := range rows {
		out = append(out, mappingRow{nullID(m.MalID), nullID(m.AnilistID), nullID(m.TvdbID)})
	}
	return out, nil
}

func nullID(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func lastPathSegment(url string) string {
	url = strings.TrimRight(url, "/")
	return url[strings.LastIndex(url, "/")+1:]
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// fields are the item columns derived from provider metadata.
type fields struct {
	title, native, image, synopsis, meta string
	year, total                          sql.NullInt64
}

// describe extracts the item columns from provider metadata.
func describe(kind string, meta Metadata) fields {
	f := fields{
		title:  providers.Str(meta, "title"),
		native: providers.Str(meta, "native_title"),
		image:  cleanImage(providers.Str(meta, "image")),
	}
	if s := providers.Str(meta, "synopsis"); s != "No synopsis available." {
		f.synopsis = s
	}
	if y := yearOf(meta); y != 0 {
		f.year = sql.NullInt64{Int64: int64(y), Valid: true}
	}
	if t := totalOf(kind, meta); t > 0 {
		f.total = sql.NullInt64{Int64: int64(t), Valid: true}
	}
	f.meta = metaJSON(meta)
	return f
}

func cleanImage(s string) string {
	if strings.HasPrefix(s, noImagePrefix) {
		return ""
	}
	return s
}

// yearOf reads the release year out of a metadata map's details.
func yearOf(meta Metadata) int {
	details := asMap(meta["details"])
	for _, key := range []string{"first_air_date", "release_date", "start_date", "publish_date", "year"} {
		if y := leadingYear(details[key]); y != 0 {
			return y
		}
	}
	return leadingYear(meta["release_year"])
}

func leadingYear(v any) int {
	switch x := v.(type) {
	case string:
		if len(x) >= 4 {
			if y, err := strconv.Atoi(x[:4]); err == nil && y > 0 {
				return y
			}
		}
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// totalOf is the number of episodes/chapters/pages/units: for shows the regular
// (non-special) episodes across seasons, for films 1.
func totalOf(kind string, meta Metadata) int {
	if IsMovie(kind) {
		return 1
	}
	if kind == KindTV || kind == KindAnime {
		sum := 0
		for _, s := range asList(asMap(meta["related"])["seasons"]) {
			m := asMap(s)
			if providers.Int(m, "season_number") != 0 {
				sum += providers.Int(m, "max_progress")
			}
		}
		if sum > 0 {
			return sum
		}
	}
	return providers.Int(meta, "max_progress")
}

// metaKeep are the metadata keys kept in items.meta for local reads; the
// heavy ones (episodes, related, cast, providers) stay with the provider
// cache.
var metaKeep = []string{"genres", "score", "score_count", "source_url", "external_links", "details", "tvdb_id", "anilist_id", "mal_id"}

func metaJSON(meta Metadata) string {
	kept := map[string]any{}
	for _, k := range metaKeep {
		if v, ok := meta[k]; ok && v != nil {
			kept[k] = v
		}
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ResolveShowRef maps a reference to a show to where shows live now: a TMDB id
// (an old link, an old client) becomes the TVDB series of the item that
// carries that TMDB id, or of the TMDB title's own TVDB cross id; anything
// else, and shows with no TVDB match, come back unchanged. The cross id only
// finds the series: the show itself is read from TVDB.
func (c *Catalog) ResolveShowRef(ctx context.Context, source, id string) (string, string) {
	if source != SourceTMDB || c.Reg == nil || c.Reg.P.TVDB == nil {
		return source, id
	}
	items, err := c.Q.FindItemsByExternalID(ctx, db.FindItemsByExternalIDParams{Source: SourceTMDB, ExternalID: id})
	if err == nil {
		legacy := false
		for _, it := range items {
			if !IsShow(it.Kind) {
				continue
			}
			if it.Source == SourceTVDB {
				return SourceTVDB, it.ExternalID
			}
			legacy = legacy || it.Source == SourceTMDB
		}
		if legacy {
			return source, id // a show with no TVDB match stays as it is
		}
	}
	if c.Reg.P.TMDB != nil {
		if meta, err := c.Reg.P.TMDB.TV(ctx, id); err == nil {
			if tvdbID := idString(meta["tvdb_id"]); tvdbID != "" && tvdbID != "0" {
				return SourceTVDB, tvdbID
			}
		}
	}
	return source, id
}

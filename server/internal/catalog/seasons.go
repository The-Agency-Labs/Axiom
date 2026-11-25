package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// SeasonsMaxAge is how long synced seasons and episodes are trusted.
const SeasonsMaxAge = 24 * time.Hour

const tmdbStill = "https://image.tmdb.org/t/p/w500"

// SyncOption changes how SyncSeasons behaves.
type SyncOption int

// Force refreshes even when the last sync is younger than SeasonsMaxAge.
const Force SyncOption = 1

// SyncSeasons refreshes item_seasons and item_episodes of a TMDB show from
// TMDB: every season including specials (season 0), every episode with its
// number, its 1-based position within the season (TMDB numbers some long
// runners' seasons continuously, so positions, not numbers, order a season),
// air date, runtime, still, overview, TMDB episode type and rating.
//
// It does nothing for items without TMDB episodes (movies, count-based
// anime, other kinds), and for items synced less than 24 hours ago unless
// Force is passed. Watches refer to episode numbers, so nothing is lost when
// TMDB renumbers: rows are replaced wholesale.
func (c *Catalog) SyncSeasons(ctx context.Context, item Item, opts ...SyncOption) error {
	if !HasEpisodes(item) {
		return nil
	}
	force := false
	for _, o := range opts {
		force = force || o == Force
	}
	if !force && item.SeasonsSyncedAt.Valid {
		if at, err := time.Parse(time.RFC3339, item.SeasonsSyncedAt.String); err == nil && c.now().Sub(at) < SeasonsMaxAge {
			return nil
		}
	}
	tv, err := c.Reg.P.TMDB.TV(ctx, item.ExternalID)
	if err != nil {
		return err
	}
	var numbers []int
	for _, s := range asList(asMap(tv["related"])["seasons"]) {
		numbers = append(numbers, providers.Int(asMap(s), "season_number"))
	}
	sort.Ints(numbers)
	fetched := Metadata{}
	if len(numbers) > 0 {
		if fetched, err = c.Reg.P.TMDB.TVWithSeasons(ctx, item.ExternalID, numbers); err != nil {
			return err
		}
	}

	type seasonRow struct {
		db.InsertItemSeasonParams
		episodes []db.InsertItemEpisodeParams
	}
	var rows []seasonRow
	for _, s := range asList(asMap(tv["related"])["seasons"]) {
		rel := asMap(s)
		n := providers.Int(rel, "season_number")
		row := seasonRow{InsertItemSeasonParams: db.InsertItemSeasonParams{
			ItemID: item.ID, Number: int64(n),
			Title:        providers.Str(rel, "season_title"),
			Image:        cleanImage(providers.Str(rel, "image")),
			AirDate:      nullStr(rel["first_air_date"]),
			EpisodeCount: int64(providers.Int(rel, "max_progress")),
		}}
		if season := asMap(fetched[fmt.Sprintf("season/%d", n)]); season != nil {
			raw := episodesOf(season)
			row.EpisodeCount = int64(len(raw))
			if row.Title == "" {
				row.Title = providers.Str(season, "season_title")
			}
			for i, ep := range raw {
				row.episodes = append(row.episodes, episodeRow(item.ID, n, i+1, ep))
			}
		}
		rows = append(rows, row)
	}

	regular := int64(0)
	for _, r := range rows {
		if r.Number != 0 {
			regular += r.EpisodeCount
		}
	}
	return c.tx(ctx, func(q *db.Queries) error {
		if regular > 0 {
			// The show's total is its regular (non-special) episode count.
			if err := q.UpdateItemTotal(ctx, db.UpdateItemTotalParams{Total: sql.NullInt64{Int64: regular, Valid: true}, ID: item.ID}); err != nil {
				return err
			}
		}
		if err := q.DeleteItemEpisodes(ctx, item.ID); err != nil {
			return err
		}
		if err := q.DeleteItemSeasons(ctx, item.ID); err != nil {
			return err
		}
		for _, r := range rows {
			if err := q.InsertItemSeason(ctx, r.InsertItemSeasonParams); err != nil {
				return err
			}
			for _, ep := range r.episodes {
				if err := q.InsertItemEpisode(ctx, ep); err != nil {
					return err
				}
			}
		}
		return q.MarkSeasonsSynced(ctx, db.MarkSeasonsSyncedParams{
			SeasonsSyncedAt: sql.NullString{String: Stamp(c.now()), Valid: true}, ID: item.ID,
		})
	})
}

// episodesOf returns a season's raw TMDB episodes ordered by episode number.
func episodesOf(season Metadata) []map[string]any {
	var out []map[string]any
	for _, e := range asList(season["episodes"]) {
		if m := asMap(e); m != nil {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return providers.Int(out[i], "episode_number") < providers.Int(out[j], "episode_number")
	})
	return out
}

func episodeRow(itemID int64, season, position int, ep map[string]any) db.InsertItemEpisodeParams {
	row := db.InsertItemEpisodeParams{
		ItemID: itemID, Season: int64(season), Number: int64(providers.Int(ep, "episode_number")),
		Position: int64(position),
		Title:    providers.Str(ep, "name"),
		AirDate:  nullStr(ep["air_date"]),
		Overview: providers.Str(ep, "overview"),
		Kind:     "standard",
	}
	if row.Title == "" {
		row.Title = providers.Str(ep, "title")
	}
	if rt := providers.Int(ep, "runtime"); rt > 0 {
		row.Runtime = sql.NullInt64{Int64: int64(rt), Valid: true}
	}
	if still := providers.Str(ep, "still_path"); still != "" {
		row.Image = tmdbStill + still
	} else {
		row.Image = cleanImage(providers.Str(ep, "image"))
	}
	if k := providers.Str(ep, "episode_type"); k != "" {
		row.Kind = k
	}
	if v, ok := ep["vote_average"].(float64); ok && v > 0 {
		row.Score = sql.NullFloat64{Float64: v, Valid: true}
	}
	return row
}

func nullStr(v any) sql.NullString {
	if s, ok := v.(string); ok && s != "" {
		return sql.NullString{String: s, Valid: true}
	}
	return sql.NullString{}
}

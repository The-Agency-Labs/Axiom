package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/the-agency-labs/encompass/server/internal/db"
)

// syncTVDB replaces the seasons and episodes of a TVDB show with TVDB's aired
// order: season 0 is the specials, numbers are TVDB's, and each episode keeps
// its TVDB id (ext_id), which is how Jellyfin plays find it. English titles
// and overviews are used when the series' default language is not English. A
// TVDB answer with no episodes never wipes an item that has some.
func (c *Catalog) syncTVDB(ctx context.Context, item Item) error {
	if c.Reg.P.TVDB == nil {
		return fmt.Errorf("%w: TVDB is not configured", ErrUnsupported)
	}
	seasons, err := c.Reg.P.TVDB.Seasons(ctx, item.ExternalID)
	if err != nil {
		return err
	}
	type seasonRow struct {
		db.InsertItemSeasonParams
		episodes []db.InsertItemEpisodeParams
	}
	var rows []seasonRow
	regular := int64(0)
	for _, s := range seasons {
		title := fmt.Sprintf("Season %d", s.Number)
		if s.Number == 0 {
			title = "Specials"
		}
		row := seasonRow{InsertItemSeasonParams: db.InsertItemSeasonParams{
			ItemID: item.ID, Number: int64(s.Number), Title: title, Image: cleanImage(s.Image),
			AirDate: nullStr(s.AirDate),
		}}
		seen := map[int]bool{}
		for _, e := range s.Episodes {
			if seen[e.Number] {
				continue
			}
			seen[e.Number] = true
			ep := db.InsertItemEpisodeParams{
				ItemID: item.ID, Season: int64(s.Number), Number: int64(e.Number), Position: int64(len(row.episodes) + 1),
				Title: e.Name, AirDate: nullStr(e.Aired), Image: e.Image, Overview: e.Overview, Kind: "standard",
				ExtID: strconv.FormatInt(e.ID, 10),
			}
			if ep.Title == "" {
				ep.Title = fmt.Sprintf("Episode %d", e.Number)
			}
			if e.Runtime > 0 {
				ep.Runtime = sql.NullInt64{Int64: int64(e.Runtime), Valid: true}
			}
			if e.FinaleType == "season" || e.FinaleType == "series" {
				ep.Kind = "finale"
			}
			row.episodes = append(row.episodes, ep)
		}
		row.EpisodeCount = int64(len(row.episodes))
		if s.Number != 0 {
			regular += row.EpisodeCount
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 && item.SeasonsSyncedAt.Valid {
		return nil
	}
	return c.tx(ctx, func(q *db.Queries) error {
		if regular > 0 {
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

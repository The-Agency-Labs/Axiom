package library

import (
	"database/sql"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// SourceAssumed marks watches Encompass inferred rather than saw: when a
// show is first seen by Jellyfin (webhook or history import) at season
// N > 1, the owner watched the earlier seasons before Encompass existed
// (media gets deleted from Jellyfin to free space). They count for progress
// and completion but not for activity, year views or the entry's dates.
const SourceAssumed = "assumed"

// SourceJellyfin is the source of Jellyfin plays (webhook and history import).
// Only Jellyfin triggers assumed seasons: AniList lists every season the
// owner watched as its own entry, so nothing needs assuming there.
const SourceJellyfin = "jellyfin"

// assumeEarlierSeasons marks every released episode of regular seasons
// 1..first-1 watched (source "assumed") and those seasons Completed. Each
// episode is dated by its air date, never later than the first real play.
func (o *op) assumeEarlierSeasons(first int64) error {
	latest := o.at.Add(-time.Second)
	for n := int64(1); n < first; n++ {
		eps, err := o.episodes(n)
		if err != nil {
			return err
		}
		released := catalog.ReleasedEpisodes(eps, o.today)
		added := 0
		for _, e := range eps {
			if !released[e.Number] {
				continue
			}
			at := latest
			if e.AirDate.Valid {
				if day, err := time.Parse(time.DateOnly, e.AirDate.String); err == nil {
					if noon := day.Add(12 * time.Hour); noon.Before(at) {
						at = noon
					}
				}
			}
			if _, err := o.q.InsertWatch(o.ctx, db.InsertWatchParams{
				EntryID: o.entry.ID, Season: sql.NullInt64{Int64: n, Valid: true},
				Episode:   sql.NullInt64{Int64: e.Number, Valid: true},
				WatchedAt: stamp(at), Source: SourceAssumed,
			}); err != nil {
				return err
			}
			added++
		}
		if added > 0 {
			if err := o.putSeason(n, Completed); err != nil {
				return err
			}
		}
	}
	return nil
}

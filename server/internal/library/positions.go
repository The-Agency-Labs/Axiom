package library

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// WatchPositions marks episodes of a season watched by their position (1 =
// first) rather than their number, for imports of AniList counts: TMDB
// numbers some long runners' seasons continuously (season 2 of Naruto
// Shippuden starts at episode 33), so "the 3rd episode of this season" is a
// position. Each play is dated by at(episode) (the import's date rules; nil =
// now) and is an import play: episodes already watched are skipped. Positions
// past the end of the season are ignored. It returns how many plays were added.
func (s *Service) WatchPositions(ctx context.Context, userID, itemID int64, season int, positions []int, at func(db.ItemEpisode) time.Time, source string) (int, error) {
	item, err := s.Catalog.GetItem(ctx, itemID)
	if err != nil {
		return 0, err
	}
	if !catalog.HasEpisodes(item) {
		return 0, ErrNotShow
	}
	if err := s.Catalog.SyncSeasons(ctx, item); err != nil {
		slog.WarnContext(ctx, "library: season sync failed", "item", itemID, "err", err)
	}
	eps, err := s.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: itemID, Season: int64(season)})
	if err != nil {
		return 0, err
	}
	sorted := append([]int(nil), positions...)
	sort.Ints(sorted)
	added := 0
	for _, p := range sorted {
		if p < 1 || p > len(eps) {
			continue
		}
		ep := eps[p-1]
		when := s.now()
		if at != nil {
			when = at(ep)
		}
		res, err := s.WatchEpisode(ctx, userID, itemID, season, int(ep.Number), when, source, WatchOptions{Import: true})
		if err != nil {
			return added, err
		}
		if res.Watch != nil {
			added++
		}
	}
	return added, nil
}

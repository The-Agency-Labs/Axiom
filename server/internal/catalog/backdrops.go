package catalog

import (
	"context"
	"encoding/json"
	"log/slog"
)

// BackfillBackdrops re-reads, once, the tracked films, shows and games stored
// before banner art was kept with the item. It returns how many it refreshed;
// one item failing does not stop the run.
func (c *Catalog) BackfillBackdrops(ctx context.Context) (int, error) {
	items, err := c.Q.ListTrackedItems(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, item := range items {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		if item.Source != SourceTMDB && item.Source != SourceTVDB && item.Source != SourceIGDB {
			continue
		}
		var meta map[string]any
		if json.Unmarshal([]byte(item.Meta), &meta) != nil {
			continue
		}
		if _, has := meta["backdrop"]; has {
			continue
		}
		if _, err := c.RefreshItem(ctx, item); err != nil {
			slog.WarnContext(ctx, "catalog: banner art", "item", item.ID, "title", item.Title, "err", err)
			continue
		}
		done++
	}
	return done, nil
}

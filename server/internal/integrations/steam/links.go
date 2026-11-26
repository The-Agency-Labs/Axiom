package steam

import (
	"context"
	"log/slog"

	"github.com/the-agency-labs/axiom/server/internal/library"
)

// RefreshLinks syncs, once, any user whose Steam games aren't yet recorded as
// tracked by Steam; the daily sync keeps the record from then on.
func (im *Importer) RefreshLinks(ctx context.Context) {
	if im.Key == "" {
		return
	}
	users, err := im.UsersWithSavedID(ctx)
	if err != nil {
		slog.WarnContext(ctx, "steam: links", "err", err)
		return
	}
	for _, id := range users {
		rows, err := im.Q.ListItemLinks(ctx, id)
		if err != nil {
			slog.WarnContext(ctx, "steam: links", "user", id, "err", err)
			continue
		}
		linked := false
		for _, r := range rows {
			linked = linked || r.Source == library.LinkSteam
		}
		if !linked {
			if _, err := im.Sync(ctx, id); err != nil {
				slog.WarnContext(ctx, "steam: links", "user", id, "err", err)
			}
		}
	}
}

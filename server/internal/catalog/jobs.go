package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/jobs"
)

// Job kinds of the durable queue; the payload is {"item_id": N}.
const (
	JobCredits  = "catalog.credits"  // fetch credits for one item
	JobReleases = "catalog.releases" // refresh seasons and releases of one item
	JobSeasons  = "catalog.seasons"  // force a season/episode sync of one item
)

// BackfillEvery is how often the credits backfill and the release refresh run.
const BackfillEvery = 6 * time.Hour

// RegisterJobs schedules the periodic credits backfill and release refresh
// and registers the one-off handlers (JobCredits, JobReleases, JobSeasons).
func (c *Catalog) RegisterJobs(m *jobs.Manager) {
	m.Every("catalog-credits-backfill", BackfillEvery, func(ctx context.Context) error {
		n, err := c.BackfillCredits(ctx, CreditsBackfillLimit)
		slog.InfoContext(ctx, "credits backfill", "fetched", n)
		return err
	})
	m.Every("catalog-releases-refresh", BackfillEvery, func(ctx context.Context) error {
		n, err := c.RefreshAllReleases(ctx)
		slog.InfoContext(ctx, "releases refresh", "items", n)
		return err
	})
	c.registerSeriesJobs(m)
	for kind, run := range map[string]func(context.Context, Item) error{
		JobCredits:  func(ctx context.Context, it Item) error { return c.FetchCredits(ctx, it, false) },
		JobReleases: c.RefreshReleases,
		JobSeasons:  func(ctx context.Context, it Item) error { return c.SyncSeasons(ctx, it, Force) },
	} {
		m.Handle(kind, func(ctx context.Context, j jobs.Job) (string, error) {
			var p struct {
				ItemID int64 `json:"item_id"`
			}
			if err := json.Unmarshal(j.Payload, &p); err != nil {
				return "", fmt.Errorf("bad payload: %w", err)
			}
			item, err := c.GetItem(ctx, p.ItemID)
			if err != nil {
				return "", err
			}
			return "", run(ctx, item)
		})
	}
}

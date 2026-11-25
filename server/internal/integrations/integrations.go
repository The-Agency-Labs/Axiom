// Package integrations wires the importers (AniList, Goodreads CSV, Steam) on
// the catalog and library, for the scheduler and the HTTP API. The Jellyfin
// integration is wired separately.
package integrations

import (
	"github.com/the-agency-labs/encompass/server/internal/cache"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/integrations/anilist"
	"github.com/the-agency-labs/encompass/server/internal/integrations/goodreads"
	"github.com/the-agency-labs/encompass/server/internal/integrations/steam"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Deps are what the importers are built on.
type Deps struct {
	Config  config.Config
	Queries *db.Queries
	Catalog *catalog.Catalog
	Library *library.Service
	Cache   *cache.Cache
}

// Suite is the set of importers. It holds no state of its own (sync markers
// and statuses live in kv), so building one per request is fine.
type Suite struct {
	AniList   *anilist.Importer
	Goodreads *goodreads.Importer
	Steam     *steam.Importer
}

// New builds the importers.
func New(d Deps) *Suite {
	return &Suite{
		AniList:   anilist.New(d.Catalog, d.Library, d.Queries, d.Config.AniListUsers),
		Goodreads: goodreads.New(d.Catalog, d.Library, d.Queries),
		Steam:     steam.New(d.Catalog, d.Library, d.Queries, providers.NewClient(d.Cache), d.Config.SteamKey),
	}
}

// RegisterJobs registers the periodic and one-off jobs of every importer:
// anilist.sync (every 30 minutes per ANILIST_USERS mapping), steam.sync
// (daily, for users that saved a Steam id) and the goodreads.import,
// anilist.sync and steam.sync one-off jobs the API enqueues.
func (s *Suite) RegisterJobs(m *jobs.Manager) {
	s.AniList.RegisterJobs(m)
	s.Goodreads.RegisterJobs(m)
	s.Steam.RegisterJobs(m)
}

package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/integrations"
	"github.com/the-agency-labs/encompass/server/internal/integrations/anilist"
	"github.com/the-agency-labs/encompass/server/internal/integrations/goodreads"
	"github.com/the-agency-labs/encompass/server/internal/integrations/sourcestatus"
	"github.com/the-agency-labs/encompass/server/internal/integrations/steam"
)

// integrations builds the importers on the server's catalog and library.
func (s *Server) integrations() *integrations.Suite {
	return integrations.New(integrations.Deps{
		Config: s.Config, Queries: s.Queries, Catalog: s.Catalog, Library: s.Library, Cache: s.Cache,
	})
}

// sourceInfo is one entry of GET /sources.
type sourceInfo struct {
	Name       string          `json:"name"`
	Kinds      []string        `json:"kinds"`
	Configured bool            `json:"configured"`
	Status     json.RawMessage `json:"status"` // the last run's status, null before the first
}

func (s *Server) sourcesRoutes(mux *http.ServeMux) {
	// GET /sources: every source with whether it is set up for the user and how its last run went.
	mux.Handle("GET /api/v1/sources", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		user := auth.User(r.Context())
		cfg := s.Config
		showKinds := []string{"tv", "anime", "movie", "anime_movie"}
		sources := []sourceInfo{
			{Name: "jellyfin", Kinds: showKinds,
				Configured: cfg.JellyfinURL != "" && cfg.JellyfinKey != "" &&
					sourcestatus.HasLocal(sourcestatus.ParsePairs(cfg.JellyfinUsers), user.Username)},
			{Name: "jellyfin_webhook", Kinds: showKinds, Configured: true}, // every user has a webhook token
			{Name: anilist.SourceName, Kinds: []string{"anime", "anime_movie", "manga"},
				Configured: sourcestatus.HasLocal(sourcestatus.ParsePairs(cfg.AniListUsers), user.Username)},
			{Name: goodreads.SourceName, Kinds: []string{"book"}, Configured: true},
			{Name: steam.SourceName, Kinds: []string{"game"}, Configured: cfg.SteamKey != ""},
		}
		for i := range sources {
			status, err := sourcestatus.Read(r.Context(), s.Queries, user.ID, sources[i].Name)
			if err != nil {
				return nil, err
			}
			sources[i].Status = status
		}
		return sources, nil
	})))

	// POST /sources/anilist/sync: re-reads the whole AniList list in the background and
	// returns {"job_id"}. With dry_run=1 it runs now and returns what would change, writing nothing.
	mux.Handle("POST /api/v1/sources/anilist/sync", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		user := auth.User(r.Context())
		im := s.integrations().AniList
		if _, ok := im.AniListName(user.Username); !ok {
			return nil, Errorf(http.StatusBadRequest, "AniList is not set up for this user")
		}
		if v := r.URL.Query().Get("dry_run"); v == "1" || v == "true" {
			res, err := im.SyncUser(r.Context(), user.ID, anilist.SyncOptions{DryRun: true})
			if err != nil {
				return nil, Errorf(http.StatusBadGateway, err.Error())
			}
			return res, nil
		}
		id, err := s.Jobs.Enqueue(r.Context(), "anilist.sync", user.ID, map[string]any{"full": true})
		if err != nil {
			return nil, err
		}
		return map[string]int64{"job_id": id}, nil
	})))

	// POST /sources/steam/sync: syncs the saved Steam library in the background.
	mux.Handle("POST /api/v1/sources/steam/sync", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		user := auth.User(r.Context())
		if s.Config.SteamKey == "" {
			return nil, Errorf(http.StatusBadRequest, "Steam API key not configured")
		}
		if _, ok := s.integrations().Steam.SavedID(r.Context(), user.ID); !ok {
			return nil, Errorf(http.StatusBadRequest, steam.ErrNoSteamID.Error())
		}
		id, err := s.Jobs.Enqueue(r.Context(), steam.JobKind, user.ID, nil)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"job_id": id}, nil
	})))
}

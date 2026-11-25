package httpapi

import (
	"database/sql"
	"io/fs"
	"net/http"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/cache"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// Server holds what handlers need.
type Server struct {
	Config  config.Config
	DB      *sql.DB
	Queries *db.Queries
	Web     fs.FS
	Auth    *auth.Service
	Jobs    *jobs.Manager
	Cache   *cache.Cache
	Catalog *catalog.Catalog
	Library *library.Service
}

// Routes builds the top-level handler. Areas register their routes on mux
// in their own files (one per area, mirroring src/api/*.py).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if err := s.DB.PingContext(r.Context()); err != nil {
			return nil, Errorf(http.StatusServiceUnavailable, "database unavailable")
		}
		return map[string]string{"status": "ok"}, nil
	}))
	s.authRoutes(mux)
	// One file per API area; each phase 3/4 agent owns its file.
	s.libraryRoutes(mux)  // routes_library.go: library, shows, seasons, episodes, anime, metadata, search
	s.readingRoutes(mux)  // routes_reading.go: reading, KOSync, KOReader, book linking, Triple P
	s.peopleRoutes(mux)   // routes_people.go: people, credits
	s.overviewRoutes(mux) // routes_overview.go: me, summary, now, activity, years
	s.jellyfinRoutes(mux) // routes_jellyfin.go: /webhook/jellyfin/{token}, Jellyfin import/sync
	s.importRoutes(mux)   // routes_imports.go: AniList, Goodreads CSV, Steam imports, /sources status
	s.calendarRoutes(mux) // routes_calendar.go: /calendar/{token}.ics
	s.listsRoutes(mux)    // routes_lists.go: custom lists
	mux.Handle("/api/", JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return nil, Errorf(http.StatusNotFound, "Not Found")
	}))
	mux.Handle("/", SPA(s.Web))
	return SecurityHeaders(s.Auth.Middleware(mux))
}

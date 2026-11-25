package httpapi

import (
	"database/sql"
	"io/fs"
	"net/http"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// Server holds what handlers need.
type Server struct {
	Config  config.Config
	DB      *sql.DB
	Queries *db.Queries
	Web     fs.FS
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
	mux.Handle("/api/", JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return nil, Errorf(http.StatusNotFound, "Not Found")
	}))
	mux.Handle("/", SPA(s.Web))
	return SecurityHeaders(mux)
}

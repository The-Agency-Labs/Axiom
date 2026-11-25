package httpapi

import (
	"net/http"

	"github.com/the-agency-labs/encompass/server/internal/auth"
)

// calendarRoutes registers the iCal calendar feed endpoints.
func (s *Server) calendarRoutes(mux *http.ServeMux) {
	// The token in the path is the auth, so no auth.Require here.
	mux.HandleFunc("GET /calendar/{file}", s.calendarFeed)
	mux.Handle("GET /api/v1/calendar", auth.Require(JSON(s.calendarJSON)))
	mux.Handle("POST /api/v1/settings/calendar-token", auth.RequireSession(JSON(s.calendarRegenerateToken)))
}

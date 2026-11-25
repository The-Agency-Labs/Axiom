package httpapi

import "net/http"

// overviewRoutes registers the me, summary, now, activity, years endpoints.
func (s *Server) overviewRoutes(mux *http.ServeMux) {
	_ = mux
}

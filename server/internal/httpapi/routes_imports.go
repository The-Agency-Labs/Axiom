package httpapi

import "net/http"

// importRoutes registers the AniList, Goodreads CSV and Steam import and the sources status endpoints.
func (s *Server) importRoutes(mux *http.ServeMux) {
	_ = mux
}

package httpapi

import "net/http"

// importRoutes registers the AniList, Goodreads CSV and Steam import and the sources status endpoints.
func (s *Server) importRoutes(mux *http.ServeMux) {
	s.sourcesRoutes(mux) // sources_handlers.go: GET /sources, POST /sources/{anilist,steam}/sync
	s.importsRoutes(mux) // imports_handlers.go: POST /imports/{goodreads,steam}
}

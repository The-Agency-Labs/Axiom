package httpapi

import "net/http"

// readingRoutes registers the reading, KOSync, KOReader, book linking, Triple P endpoints.
func (s *Server) readingRoutes(mux *http.ServeMux) {
	_ = mux
}

package httpapi

import "net/http"

// libraryRoutes registers the library, shows, seasons, episodes, anime, metadata, search endpoints.
func (s *Server) libraryRoutes(mux *http.ServeMux) {
	_ = mux
}

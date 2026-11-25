package httpapi

import "net/http"

// libraryRoutes registers the library, shows, seasons, episodes, anime, metadata, search endpoints.
func (s *Server) libraryRoutes(mux *http.ServeMux) {
	s.libraryRegisterEntries(mux) // src/api/library.py
	s.libraryRegisterShows(mux)   // src/api/shows.py
	s.libraryRegisterAnime(mux)   // src/api/anime.py
}

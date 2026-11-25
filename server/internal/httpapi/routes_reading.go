package httpapi

import (
	"net/http"

	"github.com/the-agency-labs/encompass/server/internal/auth"
)

// readingRoutes registers the reading, KOSync, KOReader, book linking and
// Triple P endpoints (src/api/reading.py, src/reading/urls.py, and the book
// notes that are new in the rewrite).
func (s *Server) readingRoutes(mux *http.ServeMux) {
	w := func(h HandlerFunc) http.Handler { return auth.RequireWrite(JSON(h)) }
	r := func(h HandlerFunc) http.Handler { return auth.Require(JSON(h)) }

	mux.Handle("POST /api/v1/reading/koreader", w(s.readingUploadKOReader))
	mux.Handle("GET /api/v1/reading/stats", r(s.readingStats))
	mux.Handle("GET /api/v1/reading/sessions", r(s.readingListSessions))
	mux.Handle("POST /api/v1/reading/sessions", w(s.readingCreateSession))
	mux.Handle("GET /api/v1/reading/documents", r(s.readingListDocuments))
	mux.Handle("POST /api/v1/reading/documents/auto-link", w(s.readingAutoLinkAll))
	mux.Handle("GET /api/v1/reading/documents/{id}/suggestions", r(s.readingSuggestions))
	mux.Handle("POST /api/v1/reading/documents/{id}/link", w(s.readingLink))
	mux.Handle("POST /api/v1/reading/documents/{id}/merge", w(s.readingMerge))
	mux.Handle("POST /api/v1/reading/documents/{id}/ignore", w(s.readingIgnore))

	mux.Handle("GET /api/v1/books/{entry_id}/notes", r(s.readingGetNotes))
	mux.Handle("PUT /api/v1/books/{entry_id}/notes", w(s.readingPutNotes))

	s.kosyncRoutes(mux)
}

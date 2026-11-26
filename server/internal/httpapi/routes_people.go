package httpapi

import (
	"net/http"

	"github.com/the-agency-labs/axiom/server/internal/auth"
)

// peopleRoutes registers the people and credits endpoints (src/api/people.py).
func (s *Server) peopleRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/credits/{media_type}/{source}/{media_id}", auth.Require(JSON(s.peopleTitleCredits)))
	mux.Handle("GET /api/v1/people", auth.Require(JSON(s.peopleLibrary)))
	mux.Handle("GET /api/v1/people/search/by-name", auth.Require(JSON(s.peopleByName)))
	mux.Handle("GET /api/v1/people/{person_id}", auth.Require(JSON(s.peoplePerson)))
}

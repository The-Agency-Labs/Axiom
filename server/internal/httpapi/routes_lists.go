package httpapi

import (
	"net/http"

	"github.com/the-agency-labs/encompass/server/internal/auth"
)

// listsRoutes registers the custom lists endpoints. Reads need any
// authenticated caller, writes a write-scoped one.
func (s *Server) listsRoutes(mux *http.ServeMux) {
	read := func(h func(r *http.Request) (any, error)) http.Handler {
		return auth.Require(JSON(func(_ http.ResponseWriter, r *http.Request) (any, error) { return h(r) }))
	}
	write := func(h func(r *http.Request) (any, error)) http.Handler {
		return auth.RequireWrite(JSON(func(_ http.ResponseWriter, r *http.Request) (any, error) { return h(r) }))
	}
	mux.Handle("GET /api/v1/lists", read(s.listsIndex))
	mux.Handle("POST /api/v1/lists", write(s.listsCreate))
	mux.Handle("GET /api/v1/lists/{id}", read(s.listsDetail))
	mux.Handle("PATCH /api/v1/lists/{id}", write(s.listsPatch))
	mux.Handle("DELETE /api/v1/lists/{id}", write(s.listsDelete))
	mux.Handle("POST /api/v1/lists/{id}/items", write(s.listsAddItem))
	mux.Handle("DELETE /api/v1/lists/{id}/items", write(s.listsRemoveItem))
	mux.Handle("DELETE /api/v1/lists/{id}/items/{item_id}", write(s.listsRemoveItem))
	mux.Handle("PUT /api/v1/lists/{id}/order", write(s.listsReorder))
	mux.Handle("POST /api/v1/lists/{id}/members", write(s.listsAddMember))
	mux.Handle("DELETE /api/v1/lists/{id}/members", write(s.listsRemoveMember))
}

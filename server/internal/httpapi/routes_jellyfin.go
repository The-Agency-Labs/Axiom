package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/integrations/jellyfin"
)

// jellyfinRoutes registers the Jellyfin webhook and the on-demand sync.
func (s *Server) jellyfinRoutes(mux *http.ServeMux) {
	var once sync.Once
	var svc *jellyfin.Service
	service := func() *jellyfin.Service {
		once.Do(func() {
			svc = jellyfin.NewService(s.DB, s.Queries, s.Config, s.Catalog, s.Library, s.Cache, s.Jobs)
		})
		return svc
	}
	// The token in the path is the only auth (no session, no CSRF): the
	// Jellyfin webhook plugin cannot log in.
	mux.HandleFunc("POST /webhook/jellyfin/{token}", func(w http.ResponseWriter, r *http.Request) {
		s.jellyfinWebhook(w, r, service())
	})
	mux.Handle("POST /api/v1/sources/jellyfin/sync", auth.RequireWrite(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.jellyfinSync(w, r, service())
	})))
}

func (s *Server) jellyfinWebhook(w http.ResponseWriter, r *http.Request, svc *jellyfin.Service) {
	ctx := r.Context()
	user, err := s.Queries.GetUserByWebhookToken(ctx, r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		slog.WarnContext(ctx, "jellyfin webhook: invalid token")
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "jellyfin webhook: user lookup", "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		slog.WarnContext(ctx, "jellyfin webhook: missing payload")
		http.Error(w, "Missing payload", http.StatusBadRequest)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	ev := jellyfin.ParsePayload(payload, svc.Percent(user))
	res, err := svc.Process(ctx, user, ev, jellyfin.Options{})
	if err != nil {
		// Always answer 200: Jellyfin would retry a failing webhook forever.
		slog.ErrorContext(ctx, "jellyfin webhook: processing failed", "item", res.Label, "err", err)
	}
	if ev.Kind == jellyfin.EventPlay || ev.Kind == jellyfin.EventStop {
		svc.RecordWebhook(ctx, user.ID, res.Label, err == nil && res.Outcome != jellyfin.Unmatched)
	}
	w.WriteHeader(http.StatusOK)
}

// jellyfinSync queues a history sync for the caller. ?full=true ignores the
// incremental marker and replays everything (still additive).
func (s *Server) jellyfinSync(w http.ResponseWriter, r *http.Request, svc *jellyfin.Service) {
	if !svc.Configured() {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"detail": "Jellyfin is not configured"})
		return
	}
	user := auth.User(r.Context())
	full := r.URL.Query().Get("full") == "true"
	id, err := s.Jobs.Enqueue(r.Context(), jellyfin.JobSync, user.ID, map[string]bool{"full": full})
	if err != nil {
		slog.ErrorContext(r.Context(), "jellyfin sync: enqueue", "err", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"detail": "Internal server error"})
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"queued": true, "job_id": id})
}

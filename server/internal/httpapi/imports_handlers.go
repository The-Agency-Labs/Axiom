package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/the-agency-labs/axiom/server/internal/auth"
	"github.com/the-agency-labs/axiom/server/internal/integrations/goodreads"
	"github.com/the-agency-labs/axiom/server/internal/integrations/mal"
	"github.com/the-agency-labs/axiom/server/internal/integrations/steam"
)

func (s *Server) importsRoutes(mux *http.ServeMux) {
	s.malImportRoute(mux)
	// POST /imports/goodreads: multipart upload of the Goodreads export (field "file", 10 MB).
	// The import runs as a background job; its result lands in the goodreads source status.
	mux.Handle("POST /api/v1/imports/goodreads", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		user := auth.User(r.Context())
		// A little headroom over the file for the multipart framing.
		r.Body = http.MaxBytesReader(w, r.Body, goodreads.MaxUpload+(1<<20))
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, uploadError(err)
		}
		defer r.MultipartForm.RemoveAll() //nolint:errcheck
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, Errorf(http.StatusUnprocessableEntity, "file is required")
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, goodreads.MaxUpload+1))
		if err != nil {
			return nil, uploadError(err)
		}
		if len(data) > goodreads.MaxUpload {
			return nil, Errorf(http.StatusRequestEntityTooLarge, "File is larger than 10 MB")
		}
		im := s.integrations().Goodreads
		key, err := im.Stash(r.Context(), user.ID, data)
		if errors.Is(err, goodreads.ErrNotUTF8) {
			return nil, Errorf(http.StatusBadRequest, "Invalid file format. Please upload a CSV file.")
		}
		if err != nil {
			return nil, err
		}
		id, err := s.Jobs.Enqueue(r.Context(), goodreads.JobKind, user.ID, map[string]string{"key": key})
		if err != nil {
			return nil, err
		}
		return map[string]int64{"job_id": id}, nil
	})))

	// POST /imports/steam {"steam_id"}: saves the SteamID64 (or vanity name) for the daily sync and
	// imports the library now, in the background.
	mux.Handle("POST /api/v1/imports/steam", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		user := auth.User(r.Context())
		var body struct {
			SteamID string `json:"steam_id"`
		}
		if err := DecodeJSON(r, &body); err != nil {
			return nil, err
		}
		body.SteamID = strings.TrimSpace(body.SteamID)
		if body.SteamID == "" {
			return nil, Errorf(http.StatusUnprocessableEntity, "steam_id is required")
		}
		if s.Config.SteamKey == "" {
			return nil, Errorf(http.StatusBadRequest, "Steam API key not configured")
		}
		if err := s.integrations().Steam.SaveID(r.Context(), user.ID, body.SteamID); err != nil {
			return nil, err
		}
		id, err := s.Jobs.Enqueue(r.Context(), steam.JobKind, user.ID, nil)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"job_id": id}, nil
	})))
}

func (s *Server) malImportRoute(mux *http.ServeMux) {
	// POST /imports/mal {"username"}: imports a public MyAnimeList anime and manga list in the background.
	mux.Handle("POST /api/v1/imports/mal", auth.RequireWrite(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		user := auth.User(r.Context())
		var body struct {
			Username string `json:"username"`
		}
		if err := DecodeJSON(r, &body); err != nil {
			return nil, err
		}
		body.Username = strings.TrimSpace(body.Username)
		if body.Username == "" {
			return nil, Errorf(http.StatusUnprocessableEntity, "username is required")
		}
		if err := s.integrations().MAL.Preflight(body.Username); err != nil {
			if errors.Is(err, mal.ErrNoKey) {
				return nil, Errorf(http.StatusBadRequest, err.Error())
			}
			return nil, Errorf(http.StatusUnprocessableEntity, err.Error())
		}
		id, err := s.Jobs.Enqueue(r.Context(), mal.JobKind, user.ID, map[string]string{"username": body.Username})
		if err != nil {
			return nil, err
		}
		return map[string]int64{"job_id": id}, nil
	})))
}

func uploadError(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return Errorf(http.StatusRequestEntityTooLarge, "File is larger than 10 MB")
	}
	return Errorf(http.StatusBadRequest, "Invalid upload: expected a multipart form with a CSV file")
}

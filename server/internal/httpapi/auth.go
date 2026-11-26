package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/the-agency-labs/axiom/server/internal/auth"
)

// authRoutes mirrors src/api/session.py and src/api/tokens.py.
func (s *Server) authRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/auth/csrf", JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		token, err := s.Auth.CSRFToken(w, r, false)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"csrf": token, "authenticated": false, "username": nil}
		if u := auth.User(r.Context()); u != nil {
			out["authenticated"] = true
			out["username"] = u.Username
		}
		return out, nil
	}))

	mux.Handle("POST /api/v1/auth/login", JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if !auth.CheckCSRF(r) {
			return nil, Errorf(http.StatusForbidden, "CSRF check failed")
		}
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := DecodeJSON(r, &in); err != nil {
			return nil, err
		}
		user, raw, err := s.Auth.Login(r.Context(), in.Username, in.Password, r.UserAgent())
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return nil, Errorf(http.StatusUnauthorized, "Invalid username or password")
		}
		if err != nil {
			return nil, err
		}
		s.Auth.SetSessionCookie(w, r, raw)
		csrf, err := s.Auth.CSRFToken(w, r, true)
		if err != nil {
			return nil, err
		}
		return map[string]string{"username": user.Username, "csrf": csrf}, nil
	}))

	mux.Handle("POST /api/v1/auth/logout", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		s.Auth.Logout(r.Context(), r)
		s.Auth.ClearSessionCookie(w, r)
		return map[string]bool{"ok": true}, nil
	})))

	mux.Handle("GET /api/v1/tokens", auth.RequireSession(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.Auth.ListTokens(r.Context(), auth.User(r.Context()).ID)
	})))

	mux.Handle("POST /api/v1/tokens", auth.RequireSession(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		in := struct {
			Name  *string `json:"name"`
			Scope string  `json:"scope"`
		}{Scope: auth.ScopeRead}
		if err := DecodeJSON(r, &in); err != nil {
			return nil, err
		}
		if in.Name == nil {
			return nil, Errorf(http.StatusUnprocessableEntity, "name is required")
		}
		if in.Scope != auth.ScopeRead && in.Scope != auth.ScopeWrite {
			return nil, Errorf(http.StatusUnprocessableEntity, "scope must be 'read' or 'write'")
		}
		return s.Auth.CreateToken(r.Context(), auth.User(r.Context()).ID, *in.Name, in.Scope)
	})))

	mux.Handle("DELETE /api/v1/tokens/{id}", auth.RequireSession(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return nil, Errorf(http.StatusUnprocessableEntity, "token id must be an integer")
		}
		token, err := s.Auth.RevokeToken(r.Context(), auth.User(r.Context()).ID, id)
		if errors.Is(err, auth.ErrNotFound) {
			return nil, Errorf(http.StatusNotFound, "Not Found")
		}
		return token, err
	})))
}

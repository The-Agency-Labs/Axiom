package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func writeDetail(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}

// bearer extracts the token from "Authorization: Bearer <token>".
func bearer(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// Middleware identifies the caller (bearer token first, then session cookie)
// and stores it in the request context. It never rejects; use Require*.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p *Principal
		if token := bearer(r); token != "" {
			p = s.authenticateToken(r.Context(), token)
		}
		if p == nil {
			if c, err := r.Cookie(SessionCookie); err == nil {
				var renewed bool
				if p, renewed = s.authenticateSession(r.Context(), c.Value); renewed {
					s.SetSessionCookie(w, r, c.Value)
				}
			}
		}
		if p != nil {
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, p))
		}
		next.ServeHTTP(w, r)
	})
}

// Require rejects unauthenticated requests with 401 and, for cookie-session
// callers making unsafe requests, enforces CSRF (403). Bearer callers skip CSRF.
func Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromContext(r.Context())
		if p == nil {
			writeDetail(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		if p.Token == nil && isUnsafe(r.Method) && !CheckCSRF(r) {
			writeDetail(w, http.StatusForbidden, "CSRF check failed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireWrite is Require plus the write scope: read-only tokens get 403.
func RequireWrite(next http.Handler) http.Handler {
	return Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !FromContext(r.Context()).CanWrite() {
			writeDetail(w, http.StatusForbidden, "This token is read-only")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireSession is Require restricted to browser sessions: API tokens get 403.
func RequireSession(next http.Handler) http.Handler {
	return Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()).Token != nil {
			writeDetail(w, http.StatusForbidden, "Manage tokens from the web app")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

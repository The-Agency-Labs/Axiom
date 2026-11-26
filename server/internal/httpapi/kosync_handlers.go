package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/auth"
)

// kosyncContentType is what KOReader's sync client expects.
const kosyncContentType = "application/vnd.koreader.v1+json"

func kosyncWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", kosyncContentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("kosync: encode response", "err", err)
	}
}

func kosyncUnauthorized(w http.ResponseWriter) {
	kosyncWrite(w, http.StatusUnauthorized, map[string]any{"code": 2001, "message": "Unauthorized"})
}

// kosyncAuth authenticates x-auth-user / x-auth-key (md5 of an API token).
// On failure it has already written the 401.
func (s *Server) kosyncAuth(w http.ResponseWriter, r *http.Request) *auth.Principal {
	p := s.Auth.AuthenticateMD5(r.Context(), r.Header.Get("x-auth-user"), r.Header.Get("x-auth-key"))
	if p == nil {
		kosyncUnauthorized(w)
	}
	return p
}

// kosyncRoutes mirrors src/reading/urls.py and kosync.py: the KOSync
// protocol, with API tokens as passwords.
func (s *Server) kosyncRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /kosync/users/create", func(w http.ResponseWriter, r *http.Request) {
		kosyncWrite(w, http.StatusPaymentRequired, map[string]any{
			"code": 2002,
			"message": "Registration is disabled. Create an API token in the web " +
				"app and log in with it as the password.",
		})
	})

	mux.HandleFunc("GET /kosync/users/auth", func(w http.ResponseWriter, r *http.Request) {
		if s.kosyncAuth(w, r) != nil {
			kosyncWrite(w, http.StatusOK, map[string]any{"authorized": "OK"})
		}
	})

	mux.HandleFunc("PUT /kosync/syncs/progress", s.kosyncUpdateProgress)

	mux.HandleFunc("GET /kosync/syncs/progress/{document}", func(w http.ResponseWriter, r *http.Request) {
		p := s.kosyncAuth(w, r)
		if p == nil {
			return
		}
		doc, found, err := s.readingService().GetProgress(r.Context(), p.User.ID, r.PathValue("document"))
		if err != nil {
			slog.ErrorContext(r.Context(), "kosync: get progress", "err", err)
			kosyncWrite(w, http.StatusInternalServerError, map[string]any{"code": 2000, "message": "Internal server error"})
			return
		}
		if !found {
			kosyncWrite(w, http.StatusOK, map[string]any{})
			return
		}
		var pct any
		if doc.Percentage.Valid {
			pct = doc.Percentage.Float64
		}
		ts, _ := time.Parse(time.RFC3339, doc.ProgressUpdatedAt.String)
		kosyncWrite(w, http.StatusOK, map[string]any{
			"document": doc.DocumentHash, "progress": doc.Progress, "percentage": pct,
			"device": doc.Device, "device_id": doc.DeviceID, "timestamp": ts.Unix(),
		})
	})

	mux.HandleFunc("GET /kosync/healthcheck", func(w http.ResponseWriter, r *http.Request) {
		kosyncWrite(w, http.StatusOK, map[string]any{"state": "OK"})
	})
}

// kosyncString mirrors Python's str() on a JSON value.
func kosyncString(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// kosyncFloat mirrors Python's float() on a JSON value.
func kosyncFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

func (s *Server) kosyncUpdateProgress(w http.ResponseWriter, r *http.Request) {
	p := s.kosyncAuth(w, r)
	if p == nil {
		return
	}
	if !p.CanWrite() {
		kosyncWrite(w, http.StatusForbidden, map[string]any{"code": 2001, "message": "Token is read-only"})
		return
	}
	invalid := func() {
		kosyncWrite(w, http.StatusForbidden, map[string]any{"code": 2003, "message": "Invalid request"})
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		invalid()
		return
	}
	payload := map[string]any{}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			invalid()
			return
		}
	}
	rawDoc, ok := payload["document"]
	if !ok {
		invalid()
		return
	}
	pct := 0.0
	if v, present := payload["percentage"]; present {
		if pct, ok = kosyncFloat(v); !ok || math.IsNaN(pct) {
			invalid()
			return
		}
	}
	progress := ""
	if v, present := payload["progress"]; present {
		progress = kosyncString(v)
	}
	device, deviceID := "", ""
	if v, present := payload["device"]; present {
		device = kosyncString(v)
	}
	if v, present := payload["device_id"]; present {
		deviceID = kosyncString(v)
	}

	doc, err := s.readingService().UpdateProgress(r.Context(), p.User.ID, kosyncString(rawDoc), progress, math.Max(0, math.Min(pct, 1)), device, deviceID)
	if err != nil {
		slog.ErrorContext(r.Context(), "kosync: update progress", "err", err)
		kosyncWrite(w, http.StatusInternalServerError, map[string]any{"code": 2000, "message": "Internal server error"})
		return
	}
	ts, _ := time.Parse(time.RFC3339, doc.ProgressUpdatedAt.String)
	kosyncWrite(w, http.StatusOK, map[string]any{"document": doc.DocumentHash, "timestamp": ts.Unix()})
}

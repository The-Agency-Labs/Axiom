// Package httpapi wires the HTTP surface: /api/v1, KOSync, webhooks, the
// calendar feed and the SPA.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Error is an HTTP error with the API's JSON shape: {"detail": "..."},
// matching django-ninja's HttpError.
type Error struct {
	Status int
	Detail string
	// Code names an error the client acts on; Sources goes with "linked".
	Code    string
	Sources []string
}

func (e *Error) Error() string { return e.Detail }

// Errorf builds an API error.
func Errorf(status int, detail string) *Error { return &Error{Status: status, Detail: detail} }

// HandlerFunc is an API handler that returns a value to encode as JSON, or an error.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) (any, error)

// JSON adapts a HandlerFunc to http.Handler.
func JSON(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := h(w, r)
		if err != nil {
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				slog.ErrorContext(r.Context(), "handler failed", "path", r.URL.Path, "err", err)
				apiErr = Errorf(http.StatusInternalServerError, "Internal server error")
			}
			body := map[string]any{"detail": apiErr.Detail}
			if apiErr.Code != "" {
				body["code"], body["sources"] = apiErr.Code, apiErr.Sources
			}
			WriteJSON(w, apiErr.Status, body)
			return
		}
		if value == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		WriteJSON(w, http.StatusOK, value)
	})
}

// WriteJSON writes value as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode response", "err", err)
	}
}

// DecodeJSON reads a JSON request body into dst (max 1 MB).
func DecodeJSON(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return Errorf(http.StatusUnprocessableEntity, "Invalid JSON body: "+err.Error())
	}
	return nil
}

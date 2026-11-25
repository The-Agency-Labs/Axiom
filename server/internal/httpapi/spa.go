package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPA serves the built SvelteKit app: real files as-is (versioned assets
// cached for a year), anything else falls back to 200.html for client-side
// routing.
func SPA(files fs.FS) http.Handler {
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if info, err := fs.Stat(files, name); err != nil || info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			data, err := fs.ReadFile(files, "200.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
		if strings.HasPrefix(name, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		server.ServeHTTP(w, r)
	})
}

// SecurityHeaders adds the headers nginx used to set.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer-when-downgrade")
		next.ServeHTTP(w, r)
	})
}

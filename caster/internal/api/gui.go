package api

import (
	"embed"
	"net/http"
	"strings"
)

// The browser UI is a static shell: it holds no data and no secrets, so it is
// served without a token. Everything it shows comes from the authenticated
// /v1 routes, called with a device token the user pairs into the tab.
//
//go:embed ui
var uiFS embed.FS

// uiPrefix is the path under which the UI's script and stylesheet are served.
const uiPrefix = "/ui/"

// isUIPath reports whether path belongs to the UI shell.
func isUIPath(path string) bool {
	return path == "/" || path == "/favicon.ico" || strings.HasPrefix(path, uiPrefix)
}

// csp pins the page to its own origin: no inline script or style, no third
// party loads, no framing. Stored notes are rendered with DOM calls, never as
// HTML, and this is the backstop if that ever slips.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func mountUI(mux *http.ServeMux) {
	serve := func(name, mime string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			b, err := uiFS.ReadFile("ui/" + name)
			if err != nil {
				http.NotFound(w, nil)
				return
			}
			h := w.Header()
			h.Set("Content-Type", mime)
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Cache-Control", "no-cache")
			_, _ = w.Write(b)
		}
	}
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /{$}", serve("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET "+uiPrefix+"app.js", serve("app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET "+uiPrefix+"app.css", serve("app.css", "text/css; charset=utf-8"))
}

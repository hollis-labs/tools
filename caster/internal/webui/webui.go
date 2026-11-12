// Package webui embeds the browser UI: a small static page (no build step) that signs in with the
// identity module's session cookie and talks to the /api routes. Note text is untrusted and is only
// ever put into the page with DOM calls, never as HTML.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed ui
var embedded embed.FS

// Assets returns the UI rooted at ui/, for httphost.Options{Assets: ...}.
func Assets() (fs.FS, error) { return fs.Sub(embedded, "ui") }

// csp pins the page to its own origin: no inline script or style, no third party loads, no framing.
// Stored notes are rendered with DOM calls, never as HTML, and this is the backstop if that slips.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Headers sets the page's security headers on everything outside /api/ and /healthz (the pages and
// their assets); the API answers JSON and carries its own.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; !strings.HasPrefix(p, "/api/") && p != "/healthz" {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The host's fallback answers 200 HTML for any unknown path; the guard answers 403 route_not_mapped
// for an /api path with no table entry. Neither may reach a caller as the page or as a refusal that
// reads like a permission problem.
func TestNotFoundPathsAreJSON(t *testing.T) {
	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ok":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/denied":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"scope_required","message":"x"}`))
		case "/api/unmapped":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"route_not_mapped","message":"no scope is mapped for this route"}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html>"))
		}
	})
	h := NotFound(page)
	for _, tc := range []struct {
		path   string
		status int
		body   string
	}{
		{"/v1/list", 404, "not_found"},
		{"/v1", 404, "not_found"},
		{"/api", 404, "not_found"},
		{"/api/unmapped", 404, "not_found"},
		{"/api/denied", 403, "scope_required"}, // a real permission refusal is left alone
		{"/api/ok", 200, `"ok":true`},
		{"/", 200, "<!doctype html>"}, // the UI shell is untouched
		{"/anything-else", 200, "<!doctype html>"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: %d %q, want %d containing %q", tc.path, rec.Code, rec.Body.String(), tc.status, tc.body)
		}
		if tc.status == 404 && !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: content type %q, want JSON", tc.path, rec.Header().Get("Content-Type"))
		}
	}
}

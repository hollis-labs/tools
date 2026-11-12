package api

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/hollis-labs/substrate/apierr"
)

// NotFound makes the paths that are not routes answer 404 JSON and never the UI page. The host
// serves its single-page fallback for any path outside /api and /healthz, which suits the UI but
// would answer 200 HTML to a caller still using the removed /v1 prefix. So:
//
//   - /v1 and everything under it is 404 {"code":"not_found"} (the prefix is gone for good);
//   - /api and an /api/ path with no route is 404 {"code":"not_found"}: the guard refuses a route
//     with no table entry as 403 route_not_mapped, which is rewritten here, so a typo reads as a
//     missing path. An anonymous caller still meets the guard's 401 first, which says nothing
//     about which paths exist.
func NotFound(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/v1" || strings.HasPrefix(p, "/v1/"), p == "/api":
			apierr.Write(w, http.StatusNotFound, "not_found", "no such path", nil)
			return
		case strings.HasPrefix(p, "/api/"):
			bw := &bufferedWriter{ResponseWriter: w}
			next.ServeHTTP(bw, r)
			bw.finish()
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bufferedWriter holds back a 403 response so its body can be checked for route_not_mapped.
type bufferedWriter struct {
	http.ResponseWriter
	status int
	hold   bool
	buf    bytes.Buffer
	sent   bool
}

func (b *bufferedWriter) WriteHeader(code int) {
	if b.status != 0 {
		return
	}
	b.status = code
	if code == http.StatusForbidden {
		b.hold = true
		return
	}
	b.sent = true
	b.ResponseWriter.WriteHeader(code)
}

func (b *bufferedWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.WriteHeader(http.StatusOK)
	}
	if b.hold {
		return b.buf.Write(p)
	}
	return b.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the real writer (flush for the event stream).
func (b *bufferedWriter) Unwrap() http.ResponseWriter { return b.ResponseWriter }

func (b *bufferedWriter) finish() {
	if !b.hold {
		return
	}
	if bytes.Contains(b.buf.Bytes(), []byte(`"route_not_mapped"`)) {
		b.ResponseWriter.Header().Del("Content-Length")
		apierr.Write(b.ResponseWriter, http.StatusNotFound, "not_found", "no such path", nil)
		return
	}
	b.ResponseWriter.WriteHeader(b.status)
	_, _ = b.ResponseWriter.Write(b.buf.Bytes())
}

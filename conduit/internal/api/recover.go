package api

import (
	"net/http"

	"github.com/hollis-labs/conduit/internal/safe"
)

// recoverMW turns a panic in any handler into a 500 that carries a ref and
// nothing else. net/http would also survive the panic, but it drops the
// connection with no response and records nothing; this gives the caller an
// answer and the operator a record. http.ErrAbortHandler keeps its meaning.
func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
				panic(v)
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			pe := safe.Capture("http "+route, v)
			writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "internal", "message": pe.Error(), "ref": pe.Rec.Ref}})
		}()
		next.ServeHTTP(w, r)
	})
}

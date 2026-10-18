package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestUIShellIsOpenButLocked(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/ui/app.js", "/ui/app.css"} {
		resp, err := http.Get(e.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("%s: weak CSP %q", p, csp)
		}
	}
	// The shell is open; the data behind it is not, and unknown paths stay closed.
	if code, _ := e.do(t, "GET", "/v1/list", "", ""); code != 401 {
		t.Fatalf("list without token: %d", code)
	}
	if code, _ := e.do(t, "GET", "/nope", "", ""); code != 404 && code != 401 {
		t.Fatalf("unknown path: %d", code)
	}
}

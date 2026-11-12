package api

import (
	"net/url"
	"strings"
	"testing"
)

// The routes mapped to authentication only leave the scope decision to the service, because it
// depends on the ref's scheme. This drives every such route from Routes(): a route added later with
// the empty scope and no check in the service fails here, because it is exercised with the callers
// that must be refused.
func TestAuthOnlyRoutesLeaveTheDecisionToTheService(t *testing.T) {
	ts := newServer(t)
	if c, b := do(t, ts, "POST", "/api/put", "caster.write", `{"name":"a/secret","content":"top secret walrus"}`); c != 200 {
		t.Fatalf("seed: %d %s", c, b)
	}
	local, remote := "caster://a/secret", "torque://task/CW-20260417-0011"
	if c, _ := do(t, ts, "GET", "/api/whoami", "caster.none", ""); c != 200 {
		t.Fatal("whoami is the one route any verified caller may use")
	}

	// request builds the call for a route and a ref.
	request := func(rt Route, ref string) (method, path, body string) {
		switch rt.Pattern {
		case "/api/scan", "/api/expand":
			return rt.Method, rt.Pattern, `{"text":"see ` + ref + ` and ` + local + `"}`
		case "/api/snap":
			return rt.Method, rt.Pattern, `{"ref":"` + ref + `","name":"a/copy"}`
		}
		return rt.Method, rt.Pattern + "?ref=" + url.QueryEscape(ref), ""
	}

	seen := 0
	for _, rt := range Routes() {
		if rt.Scope != ScopeAuthenticated || rt.Pattern == "/api/whoami" {
			continue
		}
		seen++
		whole := rt.Pattern != "/api/scan" && rt.Pattern != "/api/expand" // those two answer per ref
		for _, tc := range []struct {
			who, ref string
		}{
			{"caster.other", local}, {"caster.other", remote}, {"caster.read", remote}, {"caster.write", remote},
		} {
			m, p, body := request(rt, tc.ref)
			status, got := do(t, ts, m, p, tc.who, body)
			if tc.who != "caster.read" && strings.Contains(got, "top secret walrus") { // a reader may see local content
				t.Fatalf("%s as %q for %s leaked the content: %s", p, tc.who, tc.ref, got)
			}
			if whole && status != 403 {
				t.Fatalf("%s as %q for %s: want 403, got %d %s", p, tc.who, tc.ref, status, got)
			}
			// scan and expand answer 200 with what the caller may read: a caller without caster.read
			// resolves nothing, and the remote ref is never resolved without caster.resolve.
			if !whole && tc.who != "caster.read" && !strings.Contains(got, `"refs":[]`) {
				t.Fatalf("%s as %q resolved a ref it may not read: %s", p, tc.who, got)
			}
			if !whole && strings.Contains(got, "torque://") && strings.Contains(strings.ReplaceAll(got, `"text"`, ""), `"ref":"torque://`) {
				t.Fatalf("%s as %q resolved a remote ref without caster.resolve: %s", p, tc.who, got)
			}
		}
		// A caller that holds the scope is let through, so the refusals above are not a dead route.
		m, p, body := request(rt, local)
		if status, got := do(t, ts, m, p, "caster.read caster.write", body); status == 403 || status == 401 {
			t.Fatalf("%s with read and write was refused: %d %s", p, status, got)
		}
	}
	if seen < 6 {
		t.Fatalf("expected the six ref routes to be exercised, got %d", seen)
	}
}

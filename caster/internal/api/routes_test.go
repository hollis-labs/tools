package api

import (
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// Every route the API serves has a scope in the table the guard enforces, and lives under /api/,
// where the guard looks. Routes() is the single list the table is built from.
func TestEveryRouteHasItsScope(t *testing.T) {
	routes := Routes()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	table := libauth.NewScopeTable()
	RegisterScopes(table)
	for _, rt := range routes {
		got, known := table.ForRoute(rt.Method, rt.Pattern)
		if !known || got != rt.Scope {
			t.Errorf("%s %s: scope=%q known=%v, want %q", rt.Method, rt.Pattern, got, known, rt.Scope)
		}
		if !strings.HasPrefix(rt.Pattern, "/api/") {
			t.Errorf("%s %s is outside /api/, where the guard does not look", rt.Method, rt.Pattern)
		}
		if rt.Scope == "public" || rt.Scope == "authenticated" {
			t.Errorf("%s %s: %q is the ui.json word, not a table scope (authentication only is the empty string)", rt.Method, rt.Pattern, rt.Scope)
		}
	}
	// A route nobody declared is unmapped, which the guard refuses in enforce mode.
	for _, undeclared := range [][2]string{{"GET", "/api/health"}, {"DELETE", "/api/put"}, {"GET", "/api/admin"}, {"POST", "/api/pair"}, {"GET", "/v1/list"}} {
		if _, known := table.ForRoute(undeclared[0], undeclared[1]); known {
			t.Errorf("%s %s is mapped but not declared", undeclared[0], undeclared[1])
		}
	}
}

// The routes that need a specific scope name it; the ones that serve refs of any scheme leave the
// ref-dependent decision to the service and ask only for a verified caller.
func TestRouteScopesMatchTheDocumentedTable(t *testing.T) {
	want := map[string]string{
		"GET /api/list": "caster.read", "GET /api/log": "caster.read", "GET /api/search": "caster.read",
		"POST /api/put": "caster.write", "POST /api/act": "caster.act", "POST /api/observe": "caster.observe",
		"GET /api/report": "caster.admin", "POST /api/label": "caster.admin",
		"GET /api/get": ScopeAuthenticated, "GET /api/head": ScopeAuthenticated, "POST /api/snap": ScopeAuthenticated,
		"POST /api/scan": ScopeAuthenticated, "POST /api/expand": ScopeAuthenticated, "GET /api/actions": ScopeAuthenticated,
		"GET /api/whoami": ScopeAuthenticated,
	}
	got := map[string]string{}
	for _, rt := range Routes() {
		got[rt.Method+" "+rt.Pattern] = rt.Scope
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: scope %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the API serves %d routes, the table documents %d", len(got), len(want))
	}
}

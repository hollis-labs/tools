package api

import (
	"net/http"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/caster/internal/service"
)

// ScopeAuthenticated is the route scope for any verified caller: authentication only, no scope
// check at the guard. It is the empty string, which the guard's table reads as "mapped, needs no
// scope". (The word "authenticated" is the ui.json vocabulary for it; handed to ScopeTable.Route as
// it is, it would be a literal scope that only a `*` holder has.) The routes that read or write refs
// of any scheme use it because the scope they need depends on the ref (caster.read for a local one,
// caster.resolve for torque:// and tesseract://); the service layer decides.
const ScopeAuthenticated = ""

type route struct {
	Method  string
	Pattern string
	Scope   string
	handler http.HandlerFunc
}

// Route is one served method and path with the scope the guard requires for it.
type Route struct {
	Method  string
	Pattern string
	Scope   string
}

// Routes is every route the API serves, the single source for the scope table. A request that matches
// none is refused by the auth guard in enforce mode, so a handler added without a line here is
// unreachable rather than open.
func Routes() []Route {
	var out []Route
	// The table does not depend on the data behind the service, so a service over no storage lists it.
	for _, r := range New(service.New(nil)).routes() {
		out = append(out, Route{r.Method, r.Pattern, r.Scope})
	}
	return out
}

// RegisterScopes declares the scope of every route on the guard's table.
func RegisterScopes(t *libauth.ScopeTable) {
	for _, rt := range Routes() {
		t.Route(rt.Method, rt.Pattern, rt.Scope)
	}
}

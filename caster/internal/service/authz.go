package service

import (
	"context"
	"fmt"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/caster/internal/ref"
)

// Scopes. Authorization lives in the service layer, not in a transport, so every
// transport (CLI, MCP, HTTP) is held to the same rules by the same code. A
// transport's only job is to stamp the caller's identity into the context.
//
//	caster.read     the local store: get/list/log/search of cas:// and caster:// refs
//	caster.resolve  refs backed by another system (torque://, tesseract://), read AS caster
//	caster.write    put and snapshot into the local store
//	caster.act      actions with effects on other systems (save-handoff)
//	caster.observe  shadow-mode detection feed (records refs and verdicts, returns counts)
//	caster.admin    detection reports and labels, device listing and revocation
//
// Remote-backed refs need their own scope because caster resolves them with the
// home node's access: without it, a read-only caller could read anything that
// node can reach in Tesseract or Torque.
const (
	ScopeRead    = "caster.read"
	ScopeResolve = "caster.resolve"
	ScopeWrite   = "caster.write"
	ScopeAct     = "caster.act"
	ScopeObserve = "caster.observe"
	ScopeAdmin   = "caster.admin"
)

// AllScopes lists every scope an identity key may be given.
var AllScopes = []string{ScopeRead, ScopeResolve, ScopeWrite, ScopeAct, ScopeObserve, ScopeAdmin}

// LocalScopes are the scopes of the local principal: a stdio MCP process or CLI command run by a
// user who can open the data file, with no key (mcp.StdioOptions{LocalTrust: true}). Local trust
// is the trust of anyone who can run the binary as that user, which on the agent hosts includes
// agents, so it never covers caster.admin (detection reports and labels) or caster.act (actions
// with effects on other systems): those need a key. TestLocalScopesNeverHoldAdminOrAct holds the
// list to that.
var LocalScopes = []string{ScopeRead, ScopeResolve, ScopeWrite, ScopeObserve}

// LocalPrincipalID is the id of the local principal. It has the same "local:" prefix the framework's
// own local principal has, which no identity-issued id ("user:", "key:") can equal, so an audit line
// tells a local run from an authenticated caller.
const LocalPrincipalID = "local:operator"

// LocalPrincipal is who a CLI command or stdio MCP process is when it runs on the home node's own
// data without a key: verified by filesystem trust, holding exactly LocalScopes.
func LocalPrincipal() auth.Principal {
	return auth.Principal{
		Verified: true, ID: LocalPrincipalID, Kind: auth.KindOperator,
		Display: "local operator (filesystem trust)", Scopes: append([]string{}, LocalScopes...), Audience: "caster",
	}
}

// WithPrincipal stamps a verified identity (the HTTP transport does this from
// the identity middleware).
func WithPrincipal(ctx context.Context, p auth.Principal) context.Context {
	return auth.WithPrincipal(ctx, p)
}

func caller(ctx context.Context) (auth.Principal, bool) {
	p, ok := auth.FromContext(ctx)
	if !ok || (!p.Verified && p.ID != auth.OffModePrincipalID) {
		return auth.Principal{}, false
	}
	return p, true
}

// can reports whether the caller holds scope.
func can(ctx context.Context, scope string) bool {
	p, ok := caller(ctx)
	return ok && p.HasScope(scope)
}

// require fails closed: no verified caller is unauthenticated, a missing scope is denied.
func require(ctx context.Context, scopes ...string) error {
	p, ok := caller(ctx)
	if !ok {
		return &Error{KindUnauthenticated, fmt.Errorf("a verified caller identity is required")}
	}
	for _, s := range scopes {
		if !p.HasScope(s) {
			return &Error{KindDenied, fmt.Errorf("scope %s is required", s)}
		}
	}
	return nil
}

// remoteBacked reports whether a scheme is resolved through another system.
func remoteBacked(scheme string) bool {
	return scheme == ref.SchemeTorque || scheme == ref.SchemeTesseract
}

// readScope is the scope needed to read r.
func readScope(r ref.Ref) string {
	if remoteBacked(r.Scheme) {
		return ScopeResolve
	}
	return ScopeRead
}

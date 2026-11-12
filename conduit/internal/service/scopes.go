package service

import "context"

// Scopes. The identity module issues them on API keys and sessions; the auth guard enforces them on routes and tools.
// A holder of conduit.admin is not implied to hold the others: grant what is needed, or the wildcard conduit.*.
const (
	ScopeRead    = "conduit.read"     // see your own runs and their output
	ScopeReadAll = "conduit.read.all" // see every principal's runs and output
	ScopeRun     = "conduit.run"      // submit, wait for and cancel your own runs
	ScopeApprove = "conduit.approve"  // list and decide pending runs; also needs a signed-in person (see AsPerson)
	ScopeAdmin   = "conduit.admin"    // cancel any principal's run
)

type personKey struct{}

// AsPerson marks ctx as carrying a signed-in person, by id. Only the HTTP transport calls it, and only for a principal
// the identity module verified as a user (a session or a user token); an API key, a local-trust process and any CLI
// context are never persons. Approve refuses every context without the mark.
func AsPerson(ctx context.Context, id string) context.Context {
	return context.WithValue(As(ctx, id), personKey{}, true)
}

func isPerson(ctx context.Context) bool {
	ok, _ := ctx.Value(personKey{}).(bool)
	return ok
}

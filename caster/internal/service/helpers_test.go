package service

import (
	"context"

	"github.com/hollis-labs/libs/security/auth"
)

// localCtx is a caller holding every scope, for the tests that are not about authorization.
func localCtx() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "operator/test", Kind: auth.KindOperator, Scopes: []string{"*"}})
}

// ctxWith is a verified caller holding exactly the given scopes.
func ctxWith(scopes ...string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "device/test", Kind: auth.KindDevice, Scopes: scopes})
}

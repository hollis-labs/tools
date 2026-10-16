package service

import (
	"context"

	"github.com/hollis-labs/libs/security/auth"
)

// localCtx is the CLI/stdio caller: the local operator with every scope.
func localCtx() context.Context { return AsLocalOperator(context.Background()) }

// ctxWith is a verified caller holding exactly the given scopes.
func ctxWith(scopes ...string) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "device/test", Kind: auth.KindDevice, Scopes: scopes})
}

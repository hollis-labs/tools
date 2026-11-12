package main

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/spf13/cobra"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tools/caster/internal/hostguard"
	"github.com/hollis-labs/tools/caster/internal/settings"
)

// caster is what the commands share: the started framework app and the resolved data layout.
// ready, when set, learns the bound address (tests listen on port 0).
type caster struct {
	app    *module.App
	layout paths.Layout
	ready  func(net.Addr)
}

func (c *caster) serveCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the API and the browser UI",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return c.serve(ctx) },
	}
}

// serve runs the HTTP host until ctx ends.
func (c *caster) serve(ctx context.Context) error {
	id := identity.From(c.app)
	if id == nil {
		return errors.New("the identity module must be started")
	}
	cfg, err := settings.Parse(c.app.Config[settings.Key])
	if err != nil {
		return err
	}
	handler, err := httphost.Handler(c.app, httphost.Options{})
	if err != nil {
		return err
	}
	// Outermost first: the Host check (DNS rebinding) and forwarding-header hygiene, then the
	// session cookie becomes the bearer the guard verifies, then the host (guard, routes).
	handler = hostguard.Handler(cfg.AllowedHosts, id.Middleware(handler))
	return httphost.Serve(ctx, c.app, handler, httphost.ServeOptions{
		Ready: func(addr net.Addr) {
			slog.Info("caster listening", "addr", addr.String())
			if c.ready != nil {
				c.ready(addr)
			}
		},
	})
}

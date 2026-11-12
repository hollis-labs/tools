package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/spf13/cobra"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tools/caster/internal/api"
	"github.com/hollis-labs/tools/caster/internal/hostguard"
	"github.com/hollis-labs/tools/caster/internal/resolver"
	"github.com/hollis-labs/tools/caster/internal/service"
	"github.com/hollis-labs/tools/caster/internal/settings"
	"github.com/hollis-labs/tools/caster/internal/storage"
	"github.com/hollis-labs/tools/caster/internal/webui"
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

// newService builds the service layer over the database module's store, with the remote resolvers
// (Torque, Tesseract) the settings configure. The returned close function stops the Tesseract
// gateway child, if one was started.
func newService(app *module.App, cfg settings.Settings) (*service.Service, func(), error) {
	st := database.From(app)
	if st == nil {
		return nil, nil, errors.New("the database module must be started")
	}
	var gw resolver.ToolCaller
	var err error
	if cfg.TesseractMCP != "" {
		gw, err = resolver.CommandServer(cfg.TesseractMCP)
	} else {
		gw, err = resolver.CatalogServer("tesseract") // MCP-store refs report unavailable when there is none
	}
	if err != nil {
		gw = nil
	}
	closeGW := func() {
		if gw != nil {
			_ = gw.Close()
		}
	}
	svc := service.New(storage.New(st, storage.RootFor(databasePath(app))),
		resolver.Torque{BaseURL: cfg.TorqueURL},
		resolver.Tesseract{Bin: cfg.TesseractBin, Gateway: gw},
	)
	return svc, closeGW, nil
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
	svc, closeGW, err := newService(c.app, cfg)
	if err != nil {
		return err
	}
	defer closeGW()
	api.RegisterScopes(auth.ScopeTable(c.app))
	assets, err := webui.Assets()
	if err != nil {
		return fmt.Errorf("ui assets: %w", err)
	}
	handler, err := httphost.Handler(c.app, httphost.Options{Assets: assets, Routes: api.New(svc).Mux()})
	if err != nil {
		return err
	}
	// Outermost first: the Host check (DNS rebinding) and forwarding-header hygiene, the page's
	// security headers, then the session cookie becomes the bearer the guard verifies, then the
	// host (guard, routes, UI).
	handler = hostguard.Handler(cfg.AllowedHosts, webui.Headers(id.Middleware(handler)))
	return httphost.Serve(ctx, c.app, handler, httphost.ServeOptions{
		Ready: func(addr net.Addr) {
			slog.Info("caster listening", "addr", addr.String())
			if c.ready != nil {
				c.ready(addr)
			}
		},
	})
}

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tools/conduit/internal/api"
	"github.com/hollis-labs/tools/conduit/internal/hostguard"
	conduitmcp "github.com/hollis-labs/tools/conduit/internal/mcp"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/settings"
)

func serveCmd(ctx context.Context, app *module.App, svc *service.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the REST API and the MCP endpoint",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return serve(ctx, app, svc) },
	}
}

// serve runs the HTTP host and the loop that executes approved runs until ctx ends. The framework stops the modules
// when the app stops, after the executions in flight have been drained by the caller.
func serve(ctx context.Context, app *module.App, svc *service.Service) error {
	if err := requireLoopback(app); err != nil {
		return err
	}
	set, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		return err
	}
	id := identity.From(app)
	table := auth.ScopeTable(app)
	if id == nil || table == nil {
		return errors.New("the identity and auth modules must be started")
	}
	roots, err := allowRoots(set)
	if err != nil {
		return err
	}
	srv, err := api.New(svc, api.Config{AllowDirs: roots, RatePerMinute: set.RatePerMinute})
	if err != nil {
		return err
	}
	srv.RegisterScopes(table)
	conduitmcp.Register(app, svc, table, conduitmcp.Options{AllowDirs: roots})

	handler, err := httphost.Handler(app, httphost.Options{Routes: srv.Handler()})
	if err != nil {
		return err
	}
	// Outermost first: the Host check, then the session cookie becomes the bearer the guard verifies, then the host
	// (guard, routes).
	handler = hostguard.Handler(set.AllowedHosts, id.Middleware(handler))

	background, stopBackground := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { srv.Run(background) })
	serveErr := httphost.Serve(ctx, app, handler, httphost.ServeOptions{
		Ready: func(addr net.Addr) {
			slog.Info("conduit listening", "addr", addr.String(), "commands confined to", roots)
		},
	})
	stopBackground()
	wg.Wait()
	return serveErr
}

// requireLoopback keeps the backend on the loopback interface: the Host check assumes it, and a remote client reaches it
// through an authenticated tunnel or a local TLS proxy.
func requireLoopback(app *module.App) error {
	cfg, _ := app.Config["httphost"].(map[string]any)
	addr, _ := cfg["addr"].(string)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("conduit requires a loopback httphost.addr (for example 127.0.0.1:7421), got %q", addr)
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"

	"github.com/hollis-labs/tools/registry/internal/authmode"
	"github.com/hollis-labs/tools/registry/internal/catalog"
)

// modeEnv overrides the compiled default auth mode; tokenEnv carries the bearer token `enforce` requires.
// Neither is committed: they come from the unit's environment.
const (
	modeEnv  = "REGISTRY_AUTH"
	tokenEnv = "REGISTRY_TOKEN"
)

func serveCmd(ctx context.Context, app *module.App) *cobra.Command {
	var dev, managed bool
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the HTTP API",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if addr != "" {
				cfg, _ := app.Config["httphost"].(map[string]any)
				if cfg == nil {
					cfg = map[string]any{}
					app.Config["httphost"] = cfg
				}
				cfg["addr"] = addr
			}
			return serveWith(ctx, app, nil, dev, managed)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "Listener address (overrides httphost.addr)")
	cmd.Flags().BoolVar(&dev, "dev", false, "Allow loopback_open for a local development run")
	cmd.Flags().BoolVar(&managed, "managed", false, "Require enforced auth for a supervised run")
	return cmd
}

func serveWith(ctx context.Context, app *module.App, ready func(net.Addr), dev, managed bool) error {
	override := os.Getenv(modeEnv)
	if managed && override == "" {
		override = string(authmode.Enforce)
	}
	mode, err := authmode.Resolve(override)
	if err != nil {
		return err
	}
	if checkErr := authmode.CheckRun(mode, dev, managed); checkErr != nil {
		return checkErr
	}
	if mode != authmode.Enforce {
		return fmt.Errorf("MCP-enabled services require enforce auth, including development runs")
	}
	addr := bindAddr(app)
	// loopback_open on a non-loopback bind is refused before anything listens.
	if checkErr := authmode.CheckBind(mode, addr); checkErr != nil {
		return checkErr
	}
	if auth.Guard(app) == nil {
		return fmt.Errorf("MCP service requires the auth module")
	}
	// The active mode is part of /healthz.
	health.From(app).Add("auth_"+string(mode), func(context.Context) error { return nil })

	svc, err := catalogService(app)
	if err != nil {
		return err
	}
	routes := http.NewServeMux()
	routes.Handle("/v1/", catalog.Handler(svc.Call))
	routes.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if checkErr := svc.DB.Ping(r.Context()); checkErr != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true}`))
	})
	for _, r := range catalog.Routes {
		auth.ScopeTable(app).Route(r.Method, r.Path, catalog.Scope(r.Operation))
	}
	auth.ScopeTable(app).Route("GET", "/readyz", "registry.read")
	inner, err := httphost.Handler(app, httphost.Options{Mounts: []httphost.Mount{{Pattern: "/v1/", Handler: routes, Raw: true}, {Pattern: "/readyz", Handler: routes, Raw: true}}})
	if err != nil {
		return err
	}
	// The guard wraps the whole host, not just /api/: /mcp and routes that modules mount outside /api/ are
	// reached through httphost and would otherwise serve without credentials. Only GET /healthz stays open.
	guarded := auth.Middleware(app, inner)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			inner.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r.WithContext(catalog.WithForwardedCaller(r.Context(), r.Header)))
	})
	return httphost.Serve(ctx, app, handler, httphost.ServeOptions{
		Addr: addr,
		Ready: func(a net.Addr) {
			slog.Info("registry listening", "addr", a.String(), "auth", string(mode)) //nolint:gosec // Bound listener address, not remote input.
			if ready != nil {
				ready(a)
			}
		},
	})
}

func bindAddr(app *module.App) string {
	cfg, _ := app.Config["httphost"].(map[string]any)
	addr, _ := cfg["addr"].(string)
	if addr == "" {
		addr = "127.0.0.1:18190"
	}
	return addr
}

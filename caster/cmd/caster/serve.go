package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"

	"github.com/hollis-labs/caster/internal/api"
	"github.com/hollis-labs/caster/internal/service"
)

const defaultAddr = "127.0.0.1:8991"

// tokenPath is the bearer token file for the HTTP API.
func tokenPath(configDir string) string { return filepath.Join(configDir, "api-token") }

// ensureToken returns the API token, creating the 0600 file on first use.
func ensureToken(configDir string) (string, error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", err
	}
	return tokenfile.Ensure(tokenPath(configDir), tokenfile.Generate)
}

// serve runs the HTTP API until ctx is done. Auth is the shared go-auth guard:
// enforce by default (HOLLIS_AUTH_CASTER / HOLLIS_AUTH override), one static
// API key whose hash is built from the token file, scopes caster.read and
// caster.write. ValidateBind refuses a non-local listener unless enforcing.
func serve(ctx context.Context, svc *service.Service, configDir string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mode, err := auth.ModeFromEnv("caster", auth.ModeEnforce)
	if err != nil {
		return err
	}
	if err := auth.ValidateBind(*addr, mode); err != nil {
		return err
	}
	token, err := ensureToken(configDir)
	if err != nil {
		return fmt.Errorf("api token: %w", err)
	}
	keys := auth.NewAPIKeyVerifier(auth.APIKey{Hash: auth.HashKey(token), ID: "caster/operator", Display: "caster operator key",
		Scopes: []string{service.ScopeRead, service.ScopeWrite}})
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	guard, err := auth.New(auth.Config{Mode: mode, Verifier: keys, Scopes: api.Scopes(), Logger: logger,
		Audit: func(e auth.Event) { logger.Info("audit", "event", e) }})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *addr, Handler: api.Handler(svc, guard), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("caster: serving on http://%s (%s); token file %s", *addr, guard.Status(), tokenPath(configDir))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

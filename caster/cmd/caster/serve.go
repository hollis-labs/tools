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
	"strings"
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
	if err = auth.ValidateBind(*addr, mode); err != nil {
		return err
	}
	token, err := ensureToken(configDir)
	if err != nil {
		return fmt.Errorf("api token: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger) // handlers log pairing attempts through the default logger

	// Identity sources, tried in order; the first that accepts a token wins.
	//   1. paired device tokens (cst_…): caster-native pairing, `caster pair`
	//   2. Tether-issued JWTs, aud=caster, when CASTER_TETHER_URL is set
	//   3. the local operator's static key (the token file), full scopes
	verifiers := []auth.Verifier{svc.Verifier()}
	if tetherURL := os.Getenv("CASTER_TETHER_URL"); tetherURL != "" || os.Getenv("CASTER_JWT_BOOTSTRAP_KEY") != "" {
		jv, jerr := jwtVerifier(tetherURL)
		if jerr != nil {
			return fmt.Errorf("jwt verifier: %w", jerr)
		}
		go jv.Run(ctx)
		verifiers = append(verifiers, jv)
		logger.Info("accepting Tether-issued tokens", "audience", "caster", "issuer", tetherURL)
	}
	verifiers = append(verifiers, auth.NewAPIKeyVerifier(auth.APIKey{Hash: auth.HashKey(token), ID: "caster/operator",
		Display: "local operator key", Kind: auth.KindOperator, Scopes: []string{"caster.*"}}))

	// Authentication only: what an identity may do is decided in the service layer.
	guard, err := auth.New(auth.Config{Mode: mode, Verifier: auth.Chain(verifiers...), Logger: logger, OpenPath: api.OpenPaths,
		Audit: func(e auth.Event) { logger.Info("audit", "event", e) }})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *addr, Handler: api.Handler(svc, guard), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 120 * time.Second}
	go func() { //nolint:gosec // shutdown needs a fresh context: ctx is already canceled here
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

// tetherHeader adds the protocol header Tether's issuer endpoints require.
type tetherHeader struct{ next http.RoundTripper }

func (t tetherHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return t.next.RoundTrip(r)
}

// jwtVerifier verifies Tether-issued Ed25519 JWTs offline against the issuer's
// JWKS. Tokens must carry aud=caster. Deny-list polling is not wired: Tether
// requires a credential to read it, so revocation of an issued token relies on
// its short lifetime (at most 60 minutes) until that is resolved.
func jwtVerifier(tetherURL string) (*auth.JWTVerifier, error) {
	cfg := auth.JWTConfig{
		Issuer:           os.Getenv("CASTER_JWT_ISSUER"),
		Audience:         "caster",
		BootstrapKeyFile: os.Getenv("CASTER_JWT_BOOTSTRAP_KEY"),
		Client:           &http.Client{Timeout: 10 * time.Second, Transport: tetherHeader{http.DefaultTransport}},
	}
	if tetherURL != "" {
		cfg.JWKSURL = strings.TrimRight(tetherURL, "/") + "/.well-known/jwks.json"
	}
	return auth.NewJWTVerifier(cfg)
}

// Command conduit is the composition root of conduit: it resolves the
// app's paths, gates telemetry, opens the store, builds the service layer and
// hands only the service layer to the transports. It is outside the transport
// scope of .golangci.transport.yml.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	hotel "github.com/hollis-labs/go-otel"

	"github.com/hollis-labs/conduit/internal/blob"
	"github.com/hollis-labs/conduit/internal/cli"
	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/runbook"
	"github.com/hollis-labs/conduit/internal/service"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"
)

const (
	// appName is the go-apppaths identity: it names the XDG directories and the
	// CONDUIT_* variables paths.Resolve reads (CONDUIT_DB_PATH, CONDUIT_WORKSPACE).
	appName = "conduit"
	// otelEnvVar turns telemetry on. Telemetry is opt-in: HOLLIS_OTEL_ENABLED
	// (portfolio-wide, and a kill switch when false) wins, then this variable.
	otelEnvVar = "CONDUIT_OTEL_ENABLED"
	// envEnvVar overrides the deployment-environment tag; HOLLIS_ENV wins over it.
	envEnvVar = "CONDUIT_ENV"
	// blobsEnvVar picks the output store: caster, local, or unset (caster when
	// it is on PATH, else a local store under the data dir).
	blobsEnvVar = "CONDUIT_BLOBS"
	// policyEnvVar overrides the policy file (default: <config dir>/policy.yaml).
	policyEnvVar = "CONDUIT_POLICY"
	// runbooksEnvVar overrides the runbook file (default: <config dir>/runbooks.yaml).
	runbooksEnvVar = "CONDUIT_RUNBOOKS"
)

func main() {
	code, err := runGuarded(context.Background())
	if err != nil {
		log.Printf("conduit: %v", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// run holds the wiring so its defers (the telemetry flush, the store close) run
// before main exits; os.Exit in main would skip them.
func run(ctx context.Context) (int, error) {
	// Ctrl-C cancels the running command's process group instead of orphaning it.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Resolve once, inject downward.
	layout, err := paths.Resolve(appName)
	if err != nil {
		return 1, fmt.Errorf("resolve paths: %w", err)
	}

	if hotel.EnabledFromEnv(otelEnvVar) {
		defer hotel.InitOrWarn(ctx, log.Printf, 5*time.Second,
			hotel.WithServiceName(appName),
			hotel.WithEnvironment(hotel.EnvironmentFromEnv(envEnvVar, "dev")),
		)()
	}

	st, err := store.Open(layout.MainDB())
	if err != nil {
		return 1, fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Printf("warning: close store: %v", cerr)
		}
	}()

	blobs, err := blob.Open(os.Getenv(blobsEnvVar), filepath.Join(layout.DataDir(), "blobs"))
	if err != nil {
		return 1, fmt.Errorf("open output store: %w", err)
	}

	policyPath := os.Getenv(policyEnvVar)
	if policyPath == "" {
		policyPath = filepath.Join(layout.ConfigDir(), "policy.yaml")
	}
	pol, err := policy.Load(policyPath)
	if err != nil {
		return 1, err // a malformed policy never degrades to the baseline silently
	}

	runbookPath := os.Getenv(runbooksEnvVar)
	if runbookPath == "" {
		runbookPath = filepath.Join(layout.ConfigDir(), "runbooks.yaml")
	}
	rbs, err := runbook.Load(runbookPath)
	if err != nil {
		return 1, err
	}

	// The service layer is the only thing handed to the transports; they never
	// receive st.
	rd := redact.Default()
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	svc := service.New(st, service.WithBlobs(blobs), service.WithPolicy(pol), service.WithRunbooks(rbs), service.WithBaseContext(ctx),
		service.WithProtect(layout.DataDir(), layout.ConfigDir()), service.WithRedactor(rd))

	// From here on a recovered panic is logged and persisted: remote callers only
	// ever see its ref, and `conduit panics show <ref>` has the detail.
	panics.SetDefault(panics.Config{Reporter: panicReporter{svc: svc, logf: logf}, Redact: rd.Text})
	defer svc.Shutdown(5 * time.Second) // runs before the store closes; lets stopped runs finish recording

	describe := func() string {
		return fmt.Sprintf("data    %s\ndb      %s\nconfig  %s\npolicy  %s\nrunbooks %s\noutput  %s", layout.DataDir(), layout.MainDB(), layout.ConfigDir(), policyPath, runbookPath, blobs.Name())
	}
	return cli.Main(ctx, svc, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, describe), nil
}

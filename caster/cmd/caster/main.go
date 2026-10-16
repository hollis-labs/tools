// Command caster is the composition root of caster: it resolves the
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
	"time"

	"github.com/hollis-labs/go-apppaths/paths"
	hotel "github.com/hollis-labs/go-otel"

	"github.com/hollis-labs/caster/internal/cli"
	"github.com/hollis-labs/caster/internal/mcp"
	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/service"
	"github.com/hollis-labs/caster/internal/store"
)

const (
	// appName is the go-apppaths identity: it names the XDG directories and the
	// CASTER_* variables paths.Resolve reads (CASTER_DB_PATH, CASTER_WORKSPACE).
	appName = "caster"
	// otelEnvVar turns telemetry on. Telemetry is opt-in: HOLLIS_OTEL_ENABLED
	// (portfolio-wide, and a kill switch when false) wins, then this variable.
	otelEnvVar = "CASTER_OTEL_ENABLED"
	// envEnvVar overrides the deployment-environment tag; HOLLIS_ENV wins over it.
	envEnvVar = "CASTER_ENV"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code, err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		log.Print(err)
		code = cli.ExitError
	}
	os.Exit(code)
}

// run holds the wiring so its defers (the telemetry flush, the store close) run
// before main exits; log.Fatal in main would skip them.
func run(ctx context.Context, args []string) (int, error) {
	// Resolve once, inject downward. WithLegacyNames adopts the data of an
	// earlier name of this app (a rename) before anything is created.
	layout, err := paths.Resolve(appName)
	if err != nil {
		return 0, fmt.Errorf("resolve paths: %w", err)
	}

	if hotel.EnabledFromEnv(otelEnvVar) {
		defer hotel.InitOrWarn(ctx, log.Printf, 5*time.Second,
			hotel.WithServiceName(appName),
			hotel.WithEnvironment(hotel.EnvironmentFromEnv(envEnvVar, "dev")),
		)()
	}

	st, err := store.Open(layout.MainDB())
	if err != nil {
		return 0, fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Printf("warning: close store: %v", cerr)
		}
	}()

	// The service layer is the only thing handed to the transports; they never
	// receive st. Remote resolvers are configured here, at the composition root.
	var gw resolver.ToolCaller
	if cmdline := os.Getenv("CASTER_TESSERACT_MCP"); cmdline != "" {
		gw, err = resolver.CommandServer(cmdline)
	} else {
		gw, err = resolver.CatalogServer("tesseract") // nil on error: MCP-store refs report unavailable
	}
	if err != nil {
		gw = nil
	}
	if gw != nil {
		defer gw.Close()
	}
	svc := service.New(st,
		resolver.Torque{BaseURL: os.Getenv("CASTER_TORQUE_URL")},
		resolver.Tesseract{Bin: os.Getenv("CASTER_TESSERACT_BIN"), Gateway: gw},
	)
	if len(args) == 1 && args[0] == "paths" {
		fmt.Printf("data   %s\nindex  %s\nblobs  %s\n", layout.DataDir(), st.Path(), st.BlobRoot())
		return cli.ExitOK, nil
	}
	if len(args) == 1 && args[0] == "mcp" {
		return cli.ExitOK, mcp.Serve(ctx, svc, os.Stdin, os.Stdout, "0.2.0")
	}
	return cli.Run(ctx, svc, args, os.Stdin, os.Stdout, os.Stderr), nil
}

// Command registry is a class 2 tool built on Substrate. The shell (config, logging, health, HTTP)
// comes from the manifest; this package and internal/ are the tool.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/tools/registry/internal/catalog"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if checkErr := run(ctx); checkErr != nil {
		var reported reportedError
		if !errors.As(checkErr, &reported) {
			if slices.Contains(os.Args[1:], "--json") {
				_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"error": commandError(checkErr)})
			} else {
				fmt.Fprintln(os.Stderr, "registry:", checkErr)
			}
		}
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	restore, err := useConfigFile()
	if err != nil {
		return err
	}
	defer restore()
	database.UseMigrationFS(catalog.Migrations, "migrations")
	mods := commandModules(os.Args[1:])
	started, err := boot.StartWith(ctx, substrateApp, mods, substrateConfigJSON, boot.Options{AfterConfig: configureMCP})
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()
	registerMCP(started.App)
	return newRoot(ctx, started.App).ExecuteContext(ctx)
}

// newRoot is the command tree over the started app: Substrate supplies version, config and health.
func newRoot(ctx context.Context, app *module.App) *cobra.Command {
	root := cli.Root(app)
	root.AddCommand(mcpCmd(ctx, app))
	addClientCommands(root)
	root.AddCommand(serveCmd(ctx, app))
	return root
}

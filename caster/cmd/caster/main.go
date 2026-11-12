// Command caster keeps named text and content-addressed notes and hands them around by reference.
// It is built on Substrate: the shell (config, logging, health, HTTP, auth, identity, secrets,
// database, MCP plumbing, CLI skeleton) comes from the manifest; this package and internal/ are
// the app: its schema, its routes, its tools and its commands.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "caster:", err)
		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	layout, err := paths.Resolve(appName)
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	started, err := boot.StartWith(ctx, substrateApp, substrateModules, substrateConfigJSON, boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			resolvePaths(app, layout)
			return nil
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()

	c := &caster{app: started.App, layout: layout}
	root := cli.Root(started.App)
	serve := c.serveCmd(ctx)
	// `caster` with no command serves, like `caster serve`.
	root.RunE = func(cmd *cobra.Command, args []string) error { return serve.RunE(cmd, args) }
	root.Args = cobra.NoArgs
	root.AddCommand(serve)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

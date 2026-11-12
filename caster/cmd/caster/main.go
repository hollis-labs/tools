// Command caster keeps named text and content-addressed notes and hands them around by reference.
// It is built on Substrate: the shell (config, logging, health, HTTP, auth, identity, secrets,
// database, MCP plumbing, CLI skeleton) comes from the manifest; this package and internal/ are
// the app: its schema, its routes, its tools and its commands.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
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
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "caster:", err)
		stop()
		os.Exit(1)
	}
}

// Command kinds, from what a command may create.
const (
	kindFull     = iota // serve: every module
	kindIdentity        // identity setup, identity key create: the database, the signing key, an owner or a key; no listener
	kindReadOnly        // help, version, config, health, unknown words: no module that opens or creates a file
)

// The module sets, in manifest order. A module that opens or creates a file (database, secrets,
// identity, the signing key) is in no read-only set.
var (
	readOnlyModules = []string{"config", "logging", "health", "cli"}
	// the database, the identity module and the auth module it requires; secrets because the boot
	// hook places its directory.
	identityModules = []string{"config", "logging", "secrets", "auth", "database", "identity", "cli"}
)

// plan decides, from the command line, which modules to start, before anything is booted. Only the
// commands that must open or create files get a module that does: serve (exactly `caster serve`,
// nothing after it) and the identity bootstrap commands. Everything else, including bare `caster`
// (it prints help: a typo or an empty command never starts a server), help, version, config, health,
// completion, an unknown command and a bad flag, starts the read-only set.
//
// Every argument that does not start with "-" counts as a command word. That is right only while the
// root command has no global flag that takes a value (cobra's -h/--help is the one flag):
// TestRootHasNoValueFlags fails when such a flag is added, and then plan must learn it.
func plan(args []string) (modules []string, kind int) {
	if len(args) == 1 && args[0] == "serve" {
		return substrateModules, kindFull
	}
	var words []string
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "--version" {
			return pickModules(readOnlyModules), kindReadOnly
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	// A command word must come first: `--bogus identity setup` is a bad flag, not a bootstrap.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && len(words) > 1 && words[0] == "identity" &&
		(words[1] == "setup" || (words[1] == "key" && len(words) > 2 && words[2] == "create")) {
		return pickModules(identityModules), kindIdentity
	}
	return pickModules(readOnlyModules), kindReadOnly
}

// pickModules keeps the start order of substrateModules.
func pickModules(want []string) []string {
	var out []string
	for _, m := range substrateModules {
		if slices.Contains(want, m) {
			out = append(out, m)
		}
	}
	return out
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	return runWith(ctx, args, stdout, nil)
}

// runWith is run with a hook that learns the bound address once the listener is up (tests listen on
// port 0 and stop the server from it).
func runWith(ctx context.Context, args []string, stdout io.Writer, ready func(net.Addr)) error {
	modules, kind := plan(args)
	// Only the commands that write create the data directories; the rest resolve the layout
	// without creating anything.
	var opts []paths.Option
	if kind == kindReadOnly {
		opts = append(opts, paths.WithoutMaterialize())
	}
	layout, err := paths.Resolve(appName, opts...)
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	started, err := boot.StartWith(ctx, substrateApp, modules, substrateConfigJSON, boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			resolvePaths(app, layout)
			return nil
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = started.Stop(context.Background()) }()

	c := &caster{app: started.App, layout: layout, ready: ready}
	root := cli.Root(started.App)
	root.SetOut(stdout)
	serve := c.serveCmd(ctx)
	// Bare `caster` prints help (plan starts no server module for it).
	root.Args = cobra.NoArgs
	root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	root.AddCommand(serve)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

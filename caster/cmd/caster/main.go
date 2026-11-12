// Command caster keeps named text and content-addressed notes and hands them around by reference.
// It is built on Substrate: the shell (config, logging, health, HTTP, auth, identity, secrets,
// database, MCP plumbing, CLI skeleton) comes from the manifest; this package and internal/ are
// the app: its schema, its routes, its tools and its commands.
package main

import (
	"context"
	"errors"
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
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/tools/caster/internal/client"
	"github.com/hollis-labs/tools/caster/internal/storage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		stop()
		// A command that already printed its own message (the helper-protocol exit codes) only sets
		// the code.
		var st exitStatus
		if errors.As(err, &st) {
			os.Exit(int(st))
		}
		fmt.Fprintln(os.Stderr, "caster:", err)
		os.Exit(1)
	}
}

// exitStatus is a command's own exit code (0 ok, 2 not found, 3 denied, 64 bad input: the Hollis
// helper protocol), returned as an error so the deferred cleanup still runs.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// Command kinds, from what a command may create.
const (
	kindFull     = iota // serve: every module
	kindIdentity        // identity setup, identity key create: the database, the signing key, an owner or a key; no listener
	kindReadOnly        // help, version, config, health, unknown words: no module that opens or creates a file
	kindData            // put, get, ls, ...: the local store (may create caster.db and cas/, never a signing key), or the home node over HTTP
	kindMCP             // mcp: the stdio server over the local store or the home node
	kindClient          // login, logout, remote, paths: the link to a home node; no module that opens a file
	kindMigrate         // migrate: the new database and the blob tree, built beside the old files; no identity, no listener
)

// The module sets, in manifest order. A module that opens or creates a file (database, secrets,
// identity, the signing key) is in no read-only set.
var (
	readOnlyModules = []string{"config", "logging", "health", "cli"}
	// the database, the identity module and the auth module it requires; secrets because the boot
	// hook places its directory.
	identityModules = []string{"config", "logging", "secrets", "auth", "database", "identity", "cli"}
	// the local store for a CLI command: no auth, no identity (so no signing key), no listener. The
	// caller is the local principal the command stamps itself.
	dataModules = []string{"config", "logging", "secrets", "database", "cli"}
	// the stdio MCP server over the local store, and over the home node (nothing opens a file).
	mcpLocalModules  = []string{"config", "logging", "secrets", "database", "mcp", "cli"}
	mcpRemoteModules = []string{"config", "logging", "mcp", "cli"}
)

// dataVerbs are the commands that read or write notes; they take the old CLI's arguments verbatim
// (Conduit calls `caster put` and `caster get`).
var dataVerbs = []string{"put", "get", "resolve", "head", "snap", "ls", "log", "search", "scan", "expand", "actions", "act", "shadow", "whoami"}

// plan decides, from the command line, which modules to start, before anything is booted. Only the
// commands that must open or create files get a module that does: serve (exactly `caster serve`,
// nothing after it), the identity bootstrap commands, and the data commands and `mcp` (the local
// store, or none when this machine is a client of a home node). Everything else, including bare
// `caster` (it prints help: a typo or an empty command never starts a server), help at any level,
// version, config, health, completion, an unknown command and a bad flag, starts the read-only set.
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
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || len(words) == 0 {
		return pickModules(readOnlyModules), kindReadOnly
	}
	switch {
	case words[0] == "identity" && len(words) > 1 && (words[1] == "setup" || (words[1] == "key" && len(words) > 2 && words[2] == "create")):
		return pickModules(identityModules), kindIdentity
	case words[0] == "migrate":
		return pickModules(dataModules), kindMigrate
	case words[0] == "mcp" && len(words) == 1:
		return pickModules(mcpLocalModules), kindMCP
	case slices.Contains(dataVerbs, words[0]):
		return pickModules(dataModules), kindData
	case slices.Contains([]string{"login", "logout", "remote", "paths"}, words[0]):
		return pickModules(readOnlyModules), kindClient
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
	layout, err := paths.Resolve(appName, paths.WithoutMaterialize())
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	var profile *client.Profile
	switch kind {
	case kindFull, kindIdentity, kindMigrate:
		if layout, err = paths.Resolve(appName); err != nil {
			return fmt.Errorf("resolve paths: %w", err)
		}
	case kindData, kindMCP:
		// A machine that is a client of a home node has no store of its own: nothing opens a file.
		// Otherwise this is the local store, which may be created (never the signing key).
		if profile, err = client.Active(layout.ConfigDir(), os.Getenv); err != nil {
			return err
		}
		switch {
		case profile != nil && kind == kindMCP:
			modules = pickModules(mcpRemoteModules)
		case profile != nil:
			modules = pickModules(readOnlyModules)
		default:
			if layout, err = paths.Resolve(appName); err != nil {
				return fmt.Errorf("resolve paths: %w", err)
			}
		}
	}
	database.UseMigrationFS(storage.Migrations())
	hooks := map[string]boot.Hook{}
	var mig *migration
	if kind == kindMigrate {
		// migrate is the one command that may build the new store beside the old files: it works in
		// staging names and renames them into place when the copy is proved.
		mig = &migration{verifyOnly: slices.Contains(args, "--verify-only")}
		hooks["database"] = func(_ context.Context, app *module.App) error { return mig.prepare(app) }
	} else if slices.Contains(modules, "database") { // a hook for a module that is not started is an error
		// Before the database module creates caster.db: refuse a data directory that still has
		// only the old main.db.
		hooks["database"] = func(_ context.Context, app *module.App) error { return guardLegacy(app, layout) }
	}
	started, err := boot.StartWith(ctx, substrateApp, modules, substrateConfigJSON, boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			resolvePaths(app, layout)
			return nil
		},
		Before: hooks,
	})
	if err != nil {
		return err
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = started.Stop(context.Background())
		}
	}
	defer stop()

	c := &caster{app: started.App, layout: layout, ready: ready, profile: profile, kind: kind, in: os.Stdin, out: stdout, errw: os.Stderr, mig: mig}
	root := cli.Root(started.App)
	root.SetOut(stdout)
	root.SilenceErrors = true // main prints an error once
	// Bare `caster` prints help (plan starts no server module for it).
	root.Args = cobra.NoArgs
	root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	root.AddCommand(c.serveCmd(ctx), c.mcpCmd(ctx), c.loginCmd(), c.logoutCmd(), c.remoteCmd(), c.pathsCmd(), c.migrateCmd())
	root.AddCommand(c.dataCmds(ctx)...)
	root.SetArgs(args)
	runErr := root.ExecuteContext(ctx)
	if c.finish != nil {
		// The modules are stopped first: the database is closed (and its log checkpointed) before
		// the staged files are renamed into place.
		stop()
		if ferr := c.finish(runErr == nil); runErr == nil {
			runErr = ferr
		}
	}
	return runErr
}

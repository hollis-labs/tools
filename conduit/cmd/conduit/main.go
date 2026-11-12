// Command conduit is the governed command runtime, built on Substrate. The shell (config, logging, health, HTTP, auth,
// identity, secrets, database, MCP, CLI skeleton) comes from the manifest; this package and internal/ are the app: its
// commands, its data and its routes. It is the composition root: it resolves the data directory, opens the store, builds
// the service layer and hands only the service layer to the transports. It is outside the transport scope of
// .golangci.transport.yml.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/tools/conduit/internal/appdirs"
	"github.com/hollis-labs/tools/conduit/internal/blob"
	conduitcli "github.com/hollis-labs/tools/conduit/internal/cli"
	"github.com/hollis-labs/tools/conduit/internal/persistence"
	"github.com/hollis-labs/tools/conduit/internal/policy"
	"github.com/hollis-labs/tools/conduit/internal/runbook"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/settings"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

// exitInternal is EX_SOFTWARE: conduit itself failed.
const exitInternal = panics.ExitSoftware

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

// runGuarded is the outermost boundary: a panic anywhere in start-up or wiring (before the CLI's own guard exists)
// still ends in a ref and a clean exit code, with deferred cleanup already run.
func runGuarded(ctx context.Context) (code int, err error) {
	// Until the store is open there is nowhere to persist a panic, but its text is still untrusted: redact it before it
	// reaches standard error.
	panics.SetDefault(panics.Config{Redact: redact.Default().Text})
	defer func() {
		if v := recover(); v != nil {
			pe := panics.Capture("conduit start-up", v)
			fmt.Fprintf(os.Stderr, "conduit: internal error (ref %s). This is a bug in conduit.\n", pe.Rec.Ref)
			code, err = exitInternal, nil
		}
	}()
	return run(ctx, os.Getenv, os.Args[1:])
}

// panicReporter logs each recovered panic and stores it for `conduit panics`.
type panicReporter struct {
	svc  *service.Service
	logf func(string, ...any)
}

func (p panicReporter) Report(r panics.Record) {
	p.logf("conduit: PANIC recovered %s in %s: %s  (conduit panics show %s)", r.Ref, r.Where, r.Value, r.Ref)
	if err := p.svc.RecordPanic(r); err != nil {
		p.logf("conduit: could not record panic %s: %v", r.Ref, err)
	}
}

// run holds the wiring so its defers (the store close, the module stop) run before main exits.
func run(ctx context.Context, getenv func(string) string, args []string) (int, error) {
	// Ctrl-C cancels the running command's process group instead of orphaning it.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pl := plan(args, commandNames())
	fsys, root := persistence.Migrations()
	database.UseMigrationFS(fsys, root)

	// The data directory is only known at run time, so the paths are resolved right after the config (and the
	// SUBSTRATE_CONFIG override) is loaded; a data directory in the old layout is refused just before the database
	// module opens, and so migrates, the file. The check only exists where the database module starts (a hook for a
	// module outside the list is an error).
	var dirs appdirs.Dirs
	var before map[string]boot.Hook
	if slices.Contains(pl.modules, "database") {
		before = map[string]boot.Hook{
			"database": func(_ context.Context, app *module.App) error {
				path, ok := dbPathOf(app)
				if !ok {
					return errors.New("no database path configured")
				}
				return persistence.Check(dirs.Data, path, pl.open)
			},
		}
	}
	started, err := boot.StartWith(ctx, substrateApp, pl.modules, substrateConfigJSON, boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) (err error) {
			dirs, err = resolvePaths(app, getenv, pl)
			return err
		},
		Before: before,
	})
	if err != nil {
		return 1, err
	}
	defer func() { _ = started.Stop(context.Background()) }()
	app := started.App

	rootCmd := cli.Root(app)
	rootCmd.Short = "Governed command runtime"
	if !pl.app {
		addCommands(rootCmd, nil)
		return execute(ctx, rootCmd, args)
	}

	svc, describe, err := buildService(ctx, app, dirs, pl)
	if err != nil {
		return 1, err
	}
	defer svc.Shutdown(5 * time.Second) // runs before the store closes; lets stopped runs finish recording
	rd := redact.Default()
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	// From here on a recovered panic is logged and persisted: remote callers only ever see its ref, and
	// `conduit panics show <ref>` has the detail.
	panics.SetDefault(panics.Config{Reporter: panicReporter{svc: svc, logf: logf}, Redact: rd.Text})
	addCommands(rootCmd, &forwarder{ctx: ctx, svc: svc, describe: describe})
	return execute(ctx, rootCmd, args)
}

// buildService makes the service layer over the started framework.
func buildService(ctx context.Context, app *module.App, dirs appdirs.Dirs, pl runPlan) (*service.Service, func() string, error) {
	set, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		return nil, nil, err
	}
	policyPath := set.PolicyFile
	if policyPath == "" {
		policyPath = filepath.Join(dirs.Config, "policy.yaml")
	}
	pol, err := policy.Load(policyPath)
	if err != nil {
		return nil, nil, err // a malformed policy never degrades to the baseline silently
	}
	runbookPath := set.RunbooksFile
	if runbookPath == "" {
		runbookPath = filepath.Join(dirs.Config, "runbooks.yaml")
	}
	rbs, err := runbook.Load(runbookPath)
	if err != nil {
		return nil, nil, err
	}
	opts := []service.Option{service.WithPolicy(pol), service.WithRunbooks(rbs), service.WithBaseContext(ctx),
		service.WithProtect(dirs.Data, dirs.Config), service.WithRedactor(redact.Default())}
	blobName := "none"
	dbPath := "none"
	if !pl.noStore {
		cs := database.From(app)
		if cs == nil {
			return nil, nil, errors.New("the database module must be started")
		}
		dbPath, _ = dbPathOf(app)
		blobs, err := blob.Open(set.Blobs, filepath.Join(dirs.Data, appdirs.BlobsDir))
		if err != nil {
			return nil, nil, fmt.Errorf("open output store: %w", err)
		}
		blobName = blobs.Name()
		opts = append(opts, service.WithBlobs(blobs))
		svc := service.New(store.New(cs, dbPath), opts...)
		return svc, describer(dirs, dbPath, policyPath, runbookPath, blobName), nil
	}
	return service.New(nil, opts...), describer(dirs, dbPath, policyPath, runbookPath, blobName), nil
}

func describer(dirs appdirs.Dirs, db, policyPath, runbookPath, blobName string) func() string {
	return func() string {
		return fmt.Sprintf("data    %s\ndb      %s\nconfig  %s\npolicy  %s\nrunbooks %s\noutput  %s", dirs.Data, db, dirs.Config, policyPath, runbookPath, blobName)
	}
}

// forwarder hands a command line to the command implementations in internal/cli, which parse their own flags.
type forwarder struct {
	ctx      context.Context
	svc      *service.Service
	describe func() string
	code     int
}

// legacy names the commands implemented in internal/cli. token is gone (identity API keys replace it); serve and mcp
// are added by their own files.
var legacy = []struct{ name, short string }{
	{"run", "Run a command through policy; output is captured, redacted and stored"},
	{"check", "Policy dry run: verdict and reasons (exit 0 allow, 4 ask, 3 deny)"},
	{"parse", "Show how a command is parsed: segments, wrappers, effect classes"},
	{"ls", "List runs"},
	{"show", "Show one run with its attempts"},
	{"recall", "Print the stored output of a run"},
	{"pick", "Fuzzy picker over command history"},
	{"init", "Shell integration for bash or zsh"},
	{"pending", "Runs waiting for approval"},
	{"approve", "Review the parsed command and approve it"},
	{"reject", "Reject a pending run"},
	{"cancel", "Cancel a run"},
	{"runbooks", "Named operations: fixed argv, typed parameters"},
	{"reap", "Clean up after a crash: fail orphaned runs, stop their process groups"},
	{"panics", "Internal errors conduit recovered from"},
	{"paths", "Where data lives"},
}

func commandNames() []string {
	names := make([]string, 0, len(legacy))
	for _, c := range legacy {
		names = append(names, c.name)
	}
	return names
}

// addCommands adds the app's commands. With a nil forwarder (the help plan) they only print the usage text.
func addCommands(root *cobra.Command, f *forwarder) {
	for _, l := range legacy {
		name := l.name
		root.AddCommand(&cobra.Command{
			Use: name, Short: l.short, DisableFlagParsing: true, SilenceUsage: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				if f == nil {
					conduitcli.PrintUsage(cmd.OutOrStdout())
					return nil
				}
				f.code = conduitcli.Main(f.ctx, f.svc, append([]string{name}, args...), os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr(), f.describe)
				return nil
			},
		})
	}
	root.PersistentPostRunE = func(*cobra.Command, []string) error { return nil }
	if f != nil {
		exitCode = &f.code
	}
}

// exitCode points at the code the forwarded command returned.
var exitCode *int

func execute(ctx context.Context, root *cobra.Command, args []string) (int, error) {
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		return 1, err
	}
	if exitCode != nil {
		return *exitCode, nil
	}
	return 0, nil
}

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hollis-labs/tools/conduit/internal/persistence"
)

func TestPlanPrintOnlyCommandsStartNoFileOpeningModule(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"-h"}, {"help"}, {"--version"}, {"run", "--help"}, {"serve", "--help"}, {"frobnicate"}, {"identity", "--help"}} {
		pl := plan(args, commandNames())
		if pl.app || pl.prepare {
			t.Errorf("%v: plan %+v", args, pl)
		}
		for _, m := range pl.modules {
			if m == "database" || m == "identity" || m == "secrets" || m == "httphost" || m == "mcp" || m == "auth" {
				t.Errorf("%v starts %s", args, m)
			}
		}
	}
	// `version`, `config` and `health` are framework commands: they are not command words of this tree, so they take the
	// help plan too.
	for _, w := range []string{"version", "config", "health", "completion"} {
		if pl := plan([]string{w}, commandNames()); pl.app || slicesHas(pl.modules, "database") {
			t.Errorf("%s: %+v", w, pl)
		}
	}
}

func TestPlanWhoMayCreate(t *testing.T) {
	create := map[string]bool{"serve": true, "migrate-legacy": true, "run": true}
	for w, want := range create {
		pl := plan([]string{w}, commandNames())
		if pl.prepare != want || !pl.app {
			t.Errorf("%s: %+v", w, pl)
		}
	}
	for _, args := range [][]string{{"identity", "setup"}, {"identity", "key", "create"}} {
		if pl := plan(args, commandNames()); !pl.prepare || pl.open != persistence.Create {
			t.Errorf("%v: %+v", args, pl)
		}
	}
	for _, w := range []string{"ls", "show", "recall", "pending", "panics", "mcp"} {
		if pl := plan([]string{w}, commandNames()); pl.prepare || pl.open != persistence.ReadExisting {
			t.Errorf("%s may create: %+v", w, pl)
		}
	}
	for _, w := range []string{"check", "parse", "runbooks", "init", "paths"} {
		if pl := plan([]string{w}, commandNames()); !pl.noStore || slicesHas(pl.modules, "database") {
			t.Errorf("%s opens a database: %+v", w, pl)
		}
	}
}

func slicesHas(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// TestRootHasNoValueFlags: plan treats every argument that does not start with "-" as a command word, which is right
// only while the root command has no global flag that takes a value.
func TestRootHasNoValueFlags(t *testing.T) {
	root := &cobra.Command{Use: "conduit"}
	addCommands(root, nil)
	root.Flags().VisitAll(func(f *pflag.Flag) {
		if f.NoOptDefVal == "" && f.Value.Type() != "bool" {
			t.Errorf("root flag --%s takes a value; plan must learn it", f.Name)
		}
	})
}

// emptyHome runs the real entry point on an empty HOME and data directory and returns the exit code and what appeared.
func emptyHome(t *testing.T, args ...string) (int, []string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("SUBSTRATE_CONFIG", "")
	data := filepath.Join(base, "data")
	code, _ := run(t.Context(), func(k string) string {
		if k == "CONDUIT_DATA_DIR" {
			return data
		}
		return ""
	}, args)
	var found []string
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if p != base && p != home {
			found = append(found, strings.TrimPrefix(p, base))
		}
		return nil
	})
	return code, found
}

func TestPrintOnlyCommandsCreateNothingUnderAnEmptyHome(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"version"}, {"config"}, {"frobnicate"}, {"serve", "--help"}, {"check", "--", "ls"}, {"parse", "ls"}, {"paths"}} {
		_, found := emptyHome(t, args...)
		if len(found) != 0 {
			t.Errorf("%v created %v", args, found)
		}
	}
}

func TestOldDatabaseAloneIsRefusedAndNothingIsCreated(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	old := filepath.Join(data, persistence.LegacyDatabase)
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("not opened"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("SUBSTRATE_CONFIG", "")
	getenv := func(k string) string {
		if k == "CONDUIT_DATA_DIR" {
			return data
		}
		return ""
	}
	for _, args := range [][]string{{"run", "--", "true"}, {"serve"}, {"ls"}, {"mcp"}, {"identity", "setup", "--username", "x"}} {
		code, err := run(t.Context(), getenv, args)
		if err == nil || code == 0 || !strings.Contains(err.Error(), "pre-Substrate") {
			t.Errorf("%v: code %d err %v", args, code, err)
		}
	}
	var seen []string
	_ = filepath.WalkDir(data, func(p string, d os.DirEntry, err error) error {
		seen = append(seen, strings.TrimPrefix(p, data))
		return nil
	})
	if got := strings.Join(seen, " "); got != " /workspaces /workspaces/default /workspaces/default/main.db" {
		t.Errorf("the old directory changed: %q", got)
	}
	if b, _ := os.ReadFile(old); string(b) != "not opened" { //nolint:gosec // a test reading its own temp file
		t.Error("the old database was written")
	}
}

// approve and reject are clients of a running server, signed in as a person: they never open the database, and there
// is no local-trust path to them. token is gone.
func TestApproveAndRejectAreServerClientsOnly(t *testing.T) {
	for _, w := range []string{"approve", "reject"} {
		pl := plan([]string{w, "--server", "http://127.0.0.1:1", "--user", "u", "run"}, commandNames())
		if !pl.noStore || pl.prepare || slices.Contains(pl.modules, "database") || slices.Contains(pl.modules, "identity") {
			t.Errorf("%s must open no store and create nothing: %+v", w, pl)
		}
	}
	for _, w := range commandNames() {
		if w == "token" {
			t.Errorf("command %q must not exist", w)
		}
	}
}

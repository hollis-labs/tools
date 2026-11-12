package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/pflag"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/modules/cli"
)

func TestPlan(t *testing.T) {
	for _, tc := range []struct {
		args []string
		kind int
	}{
		{nil, kindReadOnly},
		{[]string{"serve"}, kindFull},
		{[]string{"serve", "--bogus"}, kindReadOnly},
		{[]string{"--nosuchflag"}, kindReadOnly},
		{[]string{"--help"}, kindReadOnly},
		{[]string{"help"}, kindReadOnly},
		{[]string{"version"}, kindReadOnly},
		{[]string{"config"}, kindReadOnly},
		{[]string{"health"}, kindReadOnly},
		{[]string{"completion", "bash"}, kindReadOnly},
		{[]string{"bogus"}, kindReadOnly},
		{[]string{"identity", "setup", "--username", "owner"}, kindIdentity},
		{[]string{"identity", "key", "create", "--name", "x"}, kindIdentity},
		{[]string{"identity", "--help"}, kindReadOnly},
		{[]string{"identity", "bogus"}, kindReadOnly},
		{[]string{"--bogus", "identity", "setup"}, kindReadOnly},
	} {
		mods, kind := plan(tc.args)
		if kind != tc.kind {
			t.Errorf("plan(%v) kind = %d, want %d", tc.args, kind, tc.kind)
		}
		if kind == kindReadOnly {
			for _, m := range []string{"database", "secrets", "identity", "auth", "httphost", "mcp"} {
				if slices.Contains(mods, m) {
					t.Errorf("plan(%v) starts %s, a module that opens or creates files", tc.args, m)
				}
			}
		}
	}
}

// plan reads every non-dash argument as a command word, which is right only while the root command
// has no flag that takes a value.
func TestRootHasNoValueFlags(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("SUBSTRATE_CONFIG", "")
	mods, _ := plan([]string{"version"})
	started, err := boot.StartWith(context.Background(), substrateApp, mods, substrateConfigJSON, boot.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = started.Stop(context.Background()) }()
	root := cli.Root(started.App)
	check := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Value.Type() != "bool" {
				t.Errorf("root flag --%s takes a value (%s): teach plan about it", f.Name, f.Value.Type())
			}
		})
	}
	check(root.Flags())
	check(root.PersistentFlags())
}

// The command a person types first on a host that holds the live data must not touch it: no
// directory, no file, no signing key. Each of these runs the real binary with an empty HOME and no
// data directory in the environment, and HOME must still be empty afterwards.
func TestPrintOnlyCommandsCreateNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	bin := filepath.Join(t.TempDir(), "caster")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { //nolint:gosec // builds this package
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{}, {"--help"}, {"help"}, {"version"}, {"config"}, {"health"}, {"completion", "bash"},
		{"serve", "--help"}, {"identity", "--help"},
		{"bogus"}, {"--nosuchflag"}, {"serve", "--nosuchflag"}, {"--nosuchflag", "identity", "setup"},
	} {
		home := t.TempDir()
		cmd := exec.Command(bin, args...) //nolint:gosec // the binary this test just built
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
		_ = cmd.Run() // some of these are meant to fail; only what they leave behind matters
		entries, err := os.ReadDir(home)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			var names []string
			_ = filepath.WalkDir(home, func(p string, _ os.DirEntry, _ error) error { names = append(names, p); return nil })
			t.Errorf("caster %v created %v", args, names)
		}
	}
}

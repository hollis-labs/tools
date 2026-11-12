package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points every directory the app resolves at a temp dir, so the test never sees a real
// installation, and returns the workspace directory (where main.db and caster.db live).
// serveAndStop boots every module, serves on a random loopback port and stops as soon as the
// listener is up: the start-up path (guard, migrations) has run by then.
func serveAndStop() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return runWith(ctx, []string{"serve"}, io.Discard, func(net.Addr) { cancel() })
}

func isolate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	// A listener on a random loopback port: the default address is the live service's.
	cfg := filepath.Join(root, "substrate.test.yaml")
	if err := os.WriteFile(cfg, []byte("httphost:\n  addr: \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBSTRATE_CONFIG", cfg)
	return filepath.Join(root, "data", appName, "workspaces", "default")
}

func TestOldDatabaseIsRefusedAndNeverTouched(t *testing.T) {
	ws := isolate(t)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(ws, "main.db")
	content := []byte("not really a database, but it must come out byte for byte the same")
	if err := os.WriteFile(old, content, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(old)

	err := serveAndStop()
	if err == nil || !strings.Contains(err.Error(), "caster migrate") {
		t.Fatalf("want a refusal naming `caster migrate`, got %v", err)
	}
	after, _ := os.Stat(old)
	got, _ := os.ReadFile(old) //nolint:gosec // old is a path under the test's temp directory
	if !bytes.Equal(got, content) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the old database was written")
	}
	if _, err := os.Stat(filepath.Join(ws, "caster.db")); err == nil {
		t.Fatal("a Substrate database was created beside the old one")
	}
}

func TestFreshDirectoryStarts(t *testing.T) {
	ws := isolate(t)
	if err := serveAndStop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "caster.db")); err != nil {
		t.Fatalf("the database was not created: %v", err)
	}
}

func TestMigratedDirectoryStartsEvenWithOldFile(t *testing.T) {
	ws := isolate(t)
	if err := serveAndStop(); err != nil { // creates caster.db
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "main.db"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serveAndStop(); err != nil {
		t.Fatalf("a directory with both files is the migrated state: %v", err)
	}
}

// On the live host before migration only main.db is there. help, version and a mistyped command
// must still answer, and must leave the directory exactly as it was (listing before and after).
func TestPrintOnlyCommandsBesideTheOldDatabaseChangeNothing(t *testing.T) {
	ws := isolate(t)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "main.db"), []byte("the live file"), 0o600); err != nil {
		t.Fatal(err)
	}
	list := func() string {
		var b strings.Builder
		root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(ws)))) // the temp root
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, _ error) error {
			if info, err := d.Info(); err == nil {
				b.WriteString(p + " " + info.Mode().String() + " " + info.ModTime().String() + "\n")
			}
			return nil
		})
		return b.String()
	}
	before := list()
	for _, args := range [][]string{{"--help"}, {"version"}, {"config"}, {"nonsense"}} {
		_ = run(context.Background(), args, io.Discard) // the unknown command fails; that is fine
		if after := list(); after != before {
			t.Fatalf("caster %v changed the directory:\n%s\n--- after ---\n%s", args, before, after)
		}
	}
}

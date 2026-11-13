package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Conduit stores its blobs by running `caster put -m MIME -s SOURCE -t TAGS -` (content on stdin, the
// ref alone on stdout) and `caster get REF` (the bytes on stdout, exit 2 for a ref that is not there)
// against its own throwaway store. This pins that contract; conduit/internal/blob has the other half.
func TestConduitContract(t *testing.T) {
	s := newSite(t)
	data := "conduit blob test: ref agreement\n"
	sum := sha256.Sum256([]byte(data))
	want := "cas://sha256/" + hex.EncodeToString(sum[:]) + "\n"

	out, errs, code := s.run(data, nil, "put", "-m", "text/plain", "-s", "conduit://test", "-t", "a b", "-")
	if code != 0 || out != want {
		t.Fatalf("put: code %d stdout %q (want %q) stderr %q", code, out, want, errs)
	}
	got, errs, code := s.run("", nil, "get", strings.TrimSpace(want))
	if code != 0 || got != data {
		t.Fatalf("get: code %d %q %q", code, got, errs)
	}
	missing := "cas://sha256/" + strings.Repeat("0", 64)
	if _, _, code = s.run("", nil, "get", missing); code != 2 {
		t.Fatalf("a ref that is not there must exit 2, got %d", code)
	}
	if _, _, code = s.run("", nil, "get"); code != 64 {
		t.Fatalf("a bad invocation must exit 64, got %d", code)
	}
	// CASTER_DB_PATH, the data-directory override Conduit's test sets, places the store: caster.db
	// and cas/ go in the directory it names (the old build's database file was the path itself).
	elsewhere := filepath.Join(s.dir, "elsewhere")
	if _, errs, code = s.run(data, []string{"CASTER_DB_PATH=" + filepath.Join(elsewhere, "caster.db")}, "put", "-"); code != 0 {
		t.Fatalf("put with CASTER_DB_PATH: %s", errs)
	}
	for _, f := range []string{"caster.db", filepath.Join("cas", "sha256", hex.EncodeToString(sum[:1]), hex.EncodeToString(sum[1:]))} {
		if _, err := os.Stat(filepath.Join(elsewhere, f)); err != nil {
			t.Fatalf("CASTER_DB_PATH was not honored: %v", err)
		}
	}
}

// A CLI command on the local store is the local principal: it reads and writes, and cannot act or read
// the shadow report (those need a key), and it never creates the signing key.
func TestLocalCLIIsTheNarrowPrincipal(t *testing.T) {
	s := newSite(t)
	if _, errs, code := s.run("x", nil, "put", "-n", "t/a", "-"); code != 0 {
		t.Fatalf("put: %s", errs)
	}
	who, _, code := s.run("", nil, "whoami")
	if code != 0 || !strings.Contains(who, "local:operator") || strings.Contains(who, "caster.admin") || strings.Contains(who, "caster.act") {
		t.Fatalf("whoami: %d %s", code, who)
	}
	for _, args := range [][]string{{"shadow", "report"}, {"shadow", "label", "t/a", "true"}, {"act", "save-handoff", "t/a"}} {
		if _, errs, code := s.run("", nil, args...); code != 3 || !strings.Contains(errs, "scope") {
			t.Fatalf("caster %v: want exit 3 and a scope message, got %d %q", args, code, errs)
		}
	}
	for _, f := range s.dataFiles() {
		if strings.Contains(f, "identity-signing") {
			t.Fatalf("a data command created the signing key: %v", s.dataFiles())
		}
	}
}

// With only the old main.db present, a data command refuses and names `caster migrate`, as serve does.
func TestDataCommandsRefuseTheOldDatabase(t *testing.T) {
	s := newSite(t)
	ws := filepath.Join(s.dir, "data", "caster", "workspaces", "default")
	writeFile(t, filepath.Join(ws, "main.db"), "the live file")
	before := s.dataFiles()
	for _, args := range [][]string{{"put", "-"}, {"get", "t/a"}, {"ls"}, {"mcp"}} {
		if _, errs, code := s.run("x", nil, args...); code == 0 || !strings.Contains(errs, "caster migrate") {
			t.Fatalf("caster %v: want a refusal naming `caster migrate`, got %d %q", args, code, errs)
		}
	}
	if after := s.dataFiles(); !slices.Equal(before, after) {
		t.Fatalf("a refused command changed the data directory: %v -> %v", before, after)
	}
}

// The database is private: created 0600 (the process umask is not trusted), an existing wider one is
// tightened at start, and so are its -wal and -shm. The migrated store is 0600 too.
func TestDatabaseFileIsPrivate(t *testing.T) {
	s := newSite(t)
	ws := filepath.Join(s.dir, "data", appName, "workspaces", "default")
	mode := func(name string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(filepath.Join(ws, name))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	umaskRun := func(args ...string) { // a data command creates the database; 0 = the widest umask
		t.Helper()
		cmd := exec.Command("sh", append([]string{"-c", `umask 0; exec "$0" "$@"`, s.bin}, args...)...) //nolint:gosec // the binary this test just built
		cmd.Env, cmd.Stdin = s.env, strings.NewReader("hello\n")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	umaskRun("put", "-n", "a/b", "-")
	if m := mode("caster.db"); m != 0o600 {
		t.Fatalf("a new caster.db must be 0600 whatever the umask, got %o", m)
	}
	// Widen it (and its sidecar files, if any) the way the live one was; the next start tightens it.
	for _, n := range []string{"caster.db", "caster.db-wal", "caster.db-shm"} {
		if _, err := os.Stat(filepath.Join(ws, n)); err == nil {
			must(t, os.Chmod(filepath.Join(ws, n), 0o644)) //nolint:gosec // widening on purpose, to prove the next start tightens it
		}
	}
	umaskRun("put", "-n", "a/c", "-")
	for _, n := range []string{"caster.db", "caster.db-wal", "caster.db-shm"} {
		if _, err := os.Stat(filepath.Join(ws, n)); err == nil && mode(n) != 0o600 {
			t.Fatalf("%s must be tightened to 0600, got %o", n, mode(n))
		}
	}
}

// A configured database path whose parent directory does not exist is created (private), as the
// database module itself would, and the database inside is 0600.
func TestDatabaseInAMissingDirectoryIsCreated(t *testing.T) {
	s := newSite(t)
	db := filepath.Join(s.dir, "nodir", "sub", "caster.db")
	cfg := filepath.Join(s.dir, "custom.yaml")
	must(t, os.WriteFile(cfg, []byte("database:\n  sqlite:\n    path: "+db+"\n"), 0o600))
	cmd := exec.Command(s.bin, "put", "-n", "a/b", "-") //nolint:gosec // the binary this test just built
	cmd.Env, cmd.Stdin = append(append([]string{}, s.env...), "SUBSTRATE_CONFIG="+cfg), strings.NewReader("hello\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("put with a database in a missing directory: %v\n%s", err, out)
	}
	fi, err := os.Stat(db)
	must(t, err)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("got %o", fi.Mode().Perm())
	}
	if di, err := os.Stat(filepath.Dir(db)); err != nil || di.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the created directory must be private: %v %v", di, err)
	}
}

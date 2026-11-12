package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
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

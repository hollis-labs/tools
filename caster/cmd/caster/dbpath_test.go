package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// CASTER_DB_PATH is read as the location of the old database file: its parent is the store directory.
// Both spellings in use (a file path ending in caster.db, as Conduit sets it, and any other file name)
// mean the same directory; this pins the rule so it is not an accident.
func TestCasterDBPathRule(t *testing.T) {
	s := newSite(t)
	dir := filepath.Join(s.dir, "store")
	for _, v := range []string{
		filepath.Join(dir, "caster.db"), filepath.Join(dir, "main.db"), filepath.Join(dir, "anything.sqlite"),
	} {
		out, errs, code := s.run("", []string{"CASTER_DB_PATH=" + v}, "paths")
		if code != 0 {
			t.Fatalf("paths: %s", errs)
		}
		if !strings.Contains(out, "db      "+filepath.Join(dir, "caster.db")+"\n") || !strings.Contains(out, "blobs   "+filepath.Join(dir, "cas")+"\n") {
			t.Fatalf("CASTER_DB_PATH=%s must place the store in %s:\n%s", v, dir, out)
		}
	}
	// A bare directory name is read the same way, so it means the directory above it.
	out, _, _ := s.run("", []string{"CASTER_DB_PATH=" + dir}, "paths")
	if !strings.Contains(out, "db      "+filepath.Join(s.dir, "caster.db")+"\n") {
		t.Fatalf("a directory value means its parent:\n%s", out)
	}
}

package entry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tools/localinvocation/entry"
)

func TestResolveShimSkipsSelfAndKeepsNextExecutable(t *testing.T) {
	root := t.TempDir()
	shimDir, realDir := filepath.Join(root, "shim"), filepath.Join(root, "real")
	for _, dir := range []string{shimDir, realDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	shim := filepath.Join(shimDir, "localinvocation-shim")
	if err := os.WriteFile(shim, []byte("shim"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shim, filepath.Join(shimDir, "tool")); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(realDir, "tool")
	if err := os.WriteFile(real, []byte("real"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := entry.ResolveShimTarget("tool", shim, shimDir+string(os.PathListSeparator)+realDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Fatalf("got %q, want %q", got, real)
	}
}

func TestShimName(t *testing.T) {
	name, args, err := entry.ShimName("/bin/localinvocation-shim", []string{"tool", "a b", ""})
	if err != nil || name != "tool" || len(args) != 2 || args[0] != "a b" || args[1] != "" {
		t.Fatalf("%q %#v %v", name, args, err)
	}
	name, _, err = entry.ShimName("/shim/tool", []string{"x"})
	if err != nil || name != "tool" {
		t.Fatalf("alias: %q %v", name, err)
	}
}

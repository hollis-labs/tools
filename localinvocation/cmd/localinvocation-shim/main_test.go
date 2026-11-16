package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/localinvocation/invocation"
)

func TestDirectShimRunsResolvedExecutable(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("true is unavailable")
	}
	journal := filepath.Join(t.TempDir(), "records.jsonl")
	t.Setenv(invocation.StoreEnv, journal)
	if code := run("/tmp/localinvocation-shim", []string{"true"}); code != 0 {
		t.Fatalf("shim exited %d", code)
	}
	data, err := os.ReadFile(journal)
	if err != nil || !strings.Contains(string(data), `"argv":["/`) {
		t.Fatalf("journal: %q %v", data, err)
	}
}

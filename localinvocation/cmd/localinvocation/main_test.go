package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalCommandRecordsAndRuns(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "records.jsonl")
	if code := run([]string{"run", "--store", journal, "--", "/usr/bin/true"}); code != 0 {
		t.Fatalf("run exited %d", code)
	}
	data, err := os.ReadFile(journal)
	if err != nil || !strings.Contains(string(data), `"argv":["/usr/bin/true"]`) {
		t.Fatalf("journal: %q %v", data, err)
	}
}

func TestHookRecordsStructuredArgvWithoutEnvironment(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "records.jsonl")
	input := filepath.Join(t.TempDir(), "hook.json")
	if err := os.WriteFile(input, []byte(`{"tool_input":{"argv":["tool","a b",""],"cwd":"/tmp","env":{"COLOR":"sentinel-value"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	old := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = old }()
	if code := run([]string{"hook", "--store", journal}); code != 0 {
		t.Fatalf("hook exited %d", code)
	}
	data, err := os.ReadFile(journal)
	if err != nil || !strings.Contains(string(data), `"argv":["tool","a b",""]`) || strings.Contains(string(data), "sentinel-value") {
		t.Fatalf("journal: %q %v", data, err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidBatchLeavesSourcesAndNoOutput(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"project/1","metadata":{"type":"service"}}`,
		`{"schema":"project/1","class":2,"metadata":{"type":"wrong","disposition":"continue"}}`,
		`{"schema":"project/1","class":2,"metadata":{"type":"service","disposition":"wrong"}}`,
		`{"schema":"project/1","class":2,"class":"lib","metadata":{"disposition":"continue","type":"lib"}}`,
	} {
		dir := t.TempDir()
		source := filepath.Join(dir, "source.json")
		output := filepath.Join(dir, "output")
		if err := os.WriteFile(source, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := migrate(output, []string{source}); err == nil {
			t.Fatal("invalid batch accepted")
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("failed batch created output")
		}
		unchanged, err := os.ReadFile(source)
		if err != nil || string(unchanged) != raw {
			t.Fatal("source changed")
		}
	}
}

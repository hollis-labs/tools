package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tools/registry/internal/catalog"
	"github.com/spf13/cobra"
)

func TestJSONSnapshotConnectorRerunThroughHTTP(t *testing.T) {
	endpoint := testService(t)
	credential := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(credential, []byte("test-write-credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.json")
	raw := []byte(`{"import":{"source_authority":"fixture","mapping_version":"fixture/1","checkpoint":"immutable-fixture-1","entries":[{"source_key":"guide","source_revision":"fixture-1","record":{"schema":"document/1","key":"connector-guide","payload":{"schema":"document/1","id":"connector-guide","name":"Guide","subtype":"sop","format":"markdown","body":"Guide"}}}]}}`)
	if err := os.WriteFile(snapshot, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		root := &cobra.Command{Use: "registry"}
		addClientCommands(root)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{"import-run", "--file", snapshot, "--connector", "temporary-fixture", "--url", endpoint, "--token-file", credential})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var result catalog.ImportResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if i == 0 && result.Counts.Created != 1 || i == 1 && result.Counts.Unchanged != 1 {
			t.Fatal(result)
		}
	}
}

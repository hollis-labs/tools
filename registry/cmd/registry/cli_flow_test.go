package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/registry/internal/catalog"
	"github.com/spf13/cobra"
)

func TestCLIWriteSearchConflictAndArchive(t *testing.T) {
	endpoint := testService(t)
	t.Setenv("REGISTRY_TOKEN", "test-write-credential")
	invoke := func(body string, args ...string) ([]byte, error) {
		root := &cobra.Command{Use: "registry", SilenceUsage: true, SilenceErrors: true}
		addClientCommands(root)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetIn(strings.NewReader(body))
		root.SetArgs(append(args, "--url", endpoint, "--json"))
		err := root.Execute()
		return out.Bytes(), err
	}
	body := `{"record":{"schema":"document/1","key":"cli-guide","payload":{"schema":"document/1","id":"cli/../guide","name":"CLI guide","subtype":"workflow","format":"markdown","body":"Original content"}}}`
	raw, err := invoke(body, "create", "--file", "-")
	if err != nil {
		t.Fatal(err)
	}
	var created catalog.Record
	if checkErr := json.Unmarshal(raw, &created); checkErr != nil {
		t.Fatal(checkErr)
	}
	body = strings.Replace(body, "Original content", "Updated content", 1)
	raw, err = invoke(body, "update", created.ID, "--version", created.VersionToken, "--file", "-")
	if err != nil {
		t.Fatal(err)
	}
	var updated catalog.Record
	if checkErr := json.Unmarshal(raw, &updated); checkErr != nil {
		t.Fatal(checkErr)
	}
	raw, err = invoke(body, "update", created.ID, "--version", created.VersionToken, "--file", "-")
	if err == nil || !bytes.Contains(raw, []byte(`"code":"version_conflict"`)) {
		t.Fatalf("structured CLI conflict: %s %v", raw, err)
	}
	raw, err = invoke("", "search", "--query", "Updated content", "--full")
	if err != nil || !bytes.Contains(raw, []byte("Updated content")) {
		t.Fatalf("search: %s %v", raw, err)
	}
	raw, err = invoke("", "archive", updated.ID, "--version", updated.VersionToken)
	if err != nil {
		t.Fatal(err)
	}
	var archived catalog.Record
	if checkErr := json.Unmarshal(raw, &archived); checkErr != nil {
		t.Fatal(checkErr)
	}
	raw, err = invoke("", "delete", archived.ID, "--version", archived.VersionToken)
	if err != nil || !bytes.Contains(raw, []byte(`"lifecycle": "deleted"`)) {
		t.Fatalf("delete: %s %v", raw, err)
	}
}

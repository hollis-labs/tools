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
	raw, err = invoke("", "search", "--publication", "draft", "--query", "Updated content", "--full")
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

func TestCLIJSONInputErrorsEmitOneEnvelope(t *testing.T) {
	for _, tc := range []struct {
		body string
		args []string
	}{{`{"unexpected":true}`, []string{"create", "--file", "-", "--json"}}, {`{`, []string{"create", "--file", "-", "--json"}},
		{`{"record":{"metadata":{"number":9007199254740993}}}`, []string{"create", "--file", "-", "--json"}},
		{`{"record":{"metadata":{"number":1e-400}}}`, []string{"create", "--file", "-", "--json"}},
		{`{"record":{"metadata":{"number":1e400}}}`, []string{"create", "--file", "-", "--json"}}, {"", []string{"create", "--file", "/nonexistent/registry-input", "--json"}}} {
		root := &cobra.Command{Use: "registry"}
		addClientCommands(root)
		var stderr bytes.Buffer
		root.SetErr(&stderr)
		root.SetIn(strings.NewReader(tc.body))
		root.SetArgs(tc.args)
		if err := root.Execute(); err == nil {
			t.Fatal("invalid input succeeded")
		}
		var wire struct {
			Error catalog.Error `json:"error"`
		}
		if err := json.Unmarshal(stderr.Bytes(), &wire); err != nil {
			t.Fatalf("not exactly one JSON envelope: %s", stderr.Bytes())
		}
		if wire.Error.Code != "validation_error" || wire.Error.Remedy == "" {
			t.Fatalf("missing actionable error: %+v", wire)
		}
	}
}

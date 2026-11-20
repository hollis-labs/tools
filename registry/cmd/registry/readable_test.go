package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReadableCLIUsesPublishedHTTPClient(t *testing.T) {
	endpoint := testService(t)
	root := &cobra.Command{Use: "registry"}
	addClientCommands(root)
	var out bytes.Buffer
	root.SetOut(&out)
	t.Setenv("REGISTRY_TOKEN", "test-write-credential")
	root.SetArgs([]string{"list", "--url", endpoint, "--readable"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PUBLICATION") || !strings.Contains(out.String(), "has_more: false") || json.Valid(out.Bytes()) {
		t.Fatal(out.String())
	}
}
func TestReadableEscapesAuthoredControlCharacters(t *testing.T) {
	var out bytes.Buffer
	value := json.RawMessage(`{"records":[{"id":"tool:mcp/a/../b","schema":"tool/1","name":"Injected\tcolumn\nrow\u001b[0m","publication":"draft","revision_id":"rev"}],"cursor":"next","has_more":true}`)
	if err := renderReadable(&out, value); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "tool:mcp/a/../b") || !strings.Contains(text, "cursor: next") || !strings.Contains(text, "has_more: true") {
		t.Fatal(text)
	}
}

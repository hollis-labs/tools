package kinds_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

// The copied upstream fixtures in *_test.go exercise the same admission and
// rejection cases. This independent manifest prevents unnoticed schema drift.
func TestPinnedSchemaBytesUnchanged(t *testing.T) {
	raw, err := os.ReadFile("schema-provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SHA256 map[string]string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.SHA256) != 15 {
		t.Fatalf("want 13 record and 2 auxiliary schemas, got %d", len(manifest.SHA256))
	}
	for name, want := range manifest.SHA256 {
		data, err := os.ReadFile("schemas/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
			t.Fatalf("%s differs from pinned upstream", name)
		}
		version := strings.TrimSuffix(name, "-1.json") + "/1"
		version = strings.ReplaceAll(version, "mcp-server", "mcp_server")
		version = strings.ReplaceAll(version, "launch-profile", "launch_profile")
		// Agency-extended schemas preserve their staged source
		// byte-for-byte pinned here, while Schema exposes the authored extension.
		if version == kinds.DocumentSchema || version == kinds.AgentSchema || version == kinds.LaunchProfileSchema {
			continue
		}
		exposed, err := kinds.Schema(version)
		if err != nil || string(exposed) != string(data) {
			t.Fatalf("%s not exposed unchanged: %v", version, err)
		}
	}
}

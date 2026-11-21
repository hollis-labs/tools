package kinds_test

import (
	"encoding/json"
	kinds "github.com/hollis-labs/tools/registry/contracts"
	"strings"
	"testing"
)

func packageFixture() map[string]any {
	return map[string]any{
		"schema": "tool-package/1", "id": "agt_fixture/hello@1.2.3", "publisher": "agt_fixture", "name": "hello", "version": "1.2.3",
		"digest": "sha256:" + strings.Repeat("a", 64), "artifact": "cas://sha256/" + strings.Repeat("b", 64), "entry": "bin/hello",
		"execution": map[string]any{"kind": "command"}, "env": []any{"PATH"},
		"needs":      []any{map[string]any{"audience": "registry", "scopes": []any{"registry.read"}, "env": []any{"REGISTRY_TOKEN"}}},
		"provenance": map[string]any{"publisher": "msg://agent/example/agt_fixture"},
	}
}
func TestToolPackageContract(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		valid  bool
	}{
		{"command", func(m map[string]any) {}, true},
		{"mcp", func(m map[string]any) { m["execution"].(map[string]any)["kind"] = "mcp" }, true},
		{"semver prerelease", func(m map[string]any) {
			m["version"] = "1.2.3-rc.1+build.2"
			m["id"] = "agt_fixture/hello@1.2.3-rc.1+build.2"
		}, true},
		{"identity mismatch", func(m map[string]any) { m["name"] = "other" }, false},
		{"publisher mismatch", func(m map[string]any) {
			m["provenance"].(map[string]any)["publisher"] = "msg://agent/example/agt_other"
		}, false},
		{"leading zero", func(m map[string]any) { m["version"] = "01.2.3" }, false},
		{"numeric prerelease zero", func(m map[string]any) { m["version"] = "1.2.3-01" }, false},
		{"digest", func(m map[string]any) { m["digest"] = "sha256:abc" }, false},
		{"artifact", func(m map[string]any) { m["artifact"] = "https://example.org/package.tar" }, false},
		{"unknown", func(m map[string]any) { m["extra"] = true }, false},
		{"nested unknown", func(m map[string]any) { m["execution"].(map[string]any)["args"] = []any{} }, false},
		{"env assignment", func(m map[string]any) { m["env"] = []any{"TOKEN=value"} }, false},
		{"env map", func(m map[string]any) { m["env"] = map[string]any{"TOKEN": "value"} }, false},
		{"need env assignment", func(m map[string]any) { m["needs"].([]any)[0].(map[string]any)["env"] = []any{"TOKEN=value"} }, false},
		{"need token", func(m map[string]any) { m["needs"].([]any)[0].(map[string]any)["token"] = "value" }, false},
		{"token value", func(m map[string]any) { m["env"] = []any{"Bearer " + strings.Repeat("x", 40)} }, false},
		{"token scope", func(m map[string]any) {
			m["needs"].([]any)[0].(map[string]any)["scopes"] = []any{"Bearer " + strings.Repeat("x", 40)}
		}, false},
		{"traversal", func(m map[string]any) { m["entry"] = "bin/../hello" }, false},
		{"absolute", func(m map[string]any) { m["entry"] = "/hello" }, false},
		{"windows", func(m map[string]any) { m["entry"] = `C:\hello` }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := packageFixture()
			tt.mutate(m)
			raw, _ := json.Marshal(m)
			err := kinds.ValidateJSON(kinds.ToolPackageSchema, raw)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v: %v", tt.valid, err)
			}
			if tt.valid {
				if _, err := kinds.ParseContentJSON(kinds.ToolPackageSchema, raw); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, field := range []string{"schema", "id", "publisher", "name", "version", "digest", "artifact", "entry", "execution", "needs", "env", "provenance"} {
		t.Run("required "+field, func(t *testing.T) {
			m := packageFixture()
			delete(m, field)
			raw, _ := json.Marshal(m)
			if kinds.ValidateJSON(kinds.ToolPackageSchema, raw) == nil {
				t.Fatal("missing field accepted")
			}
		})
	}
	id, err := kinds.CanonicalContentID(kinds.ToolPackageSchema, "agt_fixture/hello@1.2.3")
	if err != nil || id != "tool-package:agt_fixture/hello@1.2.3" {
		t.Fatalf("%s %v", id, err)
	}
}

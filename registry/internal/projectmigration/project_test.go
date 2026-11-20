package projectmigration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRewritePromotesAndPreserves(t *testing.T) {
	for name, raw := range map[string]string{
		"descriptor": `{"schema":"project/1","id":"example","class":2,"metadata":{"disposition":"continue","type":"service","note":"keep"}}`,
		"input":      `{"schema":"project/1","key":"example","summary":"keep","metadata":{"disposition":"continue","note":"keep"},"payload":{"schema":"project/1","id":"example","class":2,"metadata":{"type":"service"}}}`,
		"args":       `{"publication":"draft","id":"project:example","version":"old-token","reason":"upgrade","record":{"schema":"project/1","key":"example","payload":{"schema":"project/1","id":"example","class":2},"metadata":{"disposition":"continue","type":"service","note":"keep"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Rewrite([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			payload, err := Payload(got)
			if err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			if err := json.Unmarshal(payload, &p); err != nil {
				t.Fatal(err)
			}
			if p["schema"] != "project/2" || p["type"] != "service" || p["disposition"] != "continue" || p["class"] != float64(2) {
				t.Fatalf("bad promoted payload: %s", payload)
			}
			if !strings.Contains(string(got), `"note": "keep"`) || strings.Contains(string(got), "project/1") {
				t.Fatalf("unrelated fields lost or schema alias retained: %s", got)
			}
			assertNoAliases(t, got)
			if _, err := Rewrite(got); err == nil {
				t.Fatal("second rewrite accepted")
			}
		})
	}
}

func assertNoAliases(t *testing.T, raw []byte) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(map[string]any)
	walk = func(m map[string]any) {
		if metadata, ok := m["metadata"].(map[string]any); ok {
			for _, field := range []string{"disposition", "type"} {
				if _, exists := metadata[field]; exists {
					t.Fatalf("metadata.%s retained", field)
				}
			}
		}
		for _, key := range []string{"record", "payload"} {
			if child, ok := m[key].(map[string]any); ok {
				walk(child)
			}
		}
	}
	walk(doc)
}

func TestRewriteLibraryAndConflicts(t *testing.T) {
	got, err := Rewrite([]byte(`{"schema":"project/1","class":"lib","metadata":{"disposition":"harvested"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	_ = json.Unmarshal(got, &p)
	if _, exists := p["class"]; exists || p["type"] != "lib" {
		t.Fatalf("library tier retained: %s", got)
	}
	for _, raw := range []string{
		`null`, `[]`, `{"record":null}`, `{"schema":"project/1","payload":null}`,
		`{"schema":"project/2"}`,
		`{"schema":"project/1","class":2,"metadata":{"type":"service"}}`,
		`{"schema":"project/1","class":"lib","metadata":{"type":"service","disposition":"continue"}}`,
		`{"schema":"project/1","type":"app","metadata":{"type":"service","disposition":"continue"}}`,
		`{"schema":"project/1","metadata":{"type":"app","disposition":"continue"},"payload":{"schema":"project/1","metadata":{"type":"service","disposition":"continue"}}}`,
		`{"schema":"project/1","metadata":[]}`,
	} {
		if _, err := Rewrite([]byte(raw)); err == nil {
			t.Errorf("accepted invalid draft: %s", raw)
		}
	}
}

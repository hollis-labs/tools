package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Compare the HTTP response with the authored contracts in this checkout, not
// kinds.Schema: both the service and that function use the pinned module and
// would agree even when go.mod accidentally leaves the service behind HEAD.
func TestServedSchemasMatchAuthoredContracts(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	handler := Handler(s.Call)
	for _, tc := range []struct {
		schema, file string
		fields       []string
	}{
		{"launch_profile/1", "launch-profile-1-registry.json", []string{"launcher", "host", "runtime"}},
		{"agent/1", "agent-1-registry.json", []string{"context", "variants"}},
		{"document/1", "document-1-registry.json", []string{"architecture", "template", "communication"}},
		{"host/1", "host-1.json", []string{"roles", "os", "tailnet", "services", "operator", "project", "config", "runbook"}},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/schema?schema="+url.QueryEscape(tc.schema), nil).WithContext(writer)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("schema_get: HTTP %d: %s", response.Code, response.Body)
			}
			var served map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
				t.Fatal(err)
			}
			properties, ok := served["properties"].(map[string]any)
			if !ok {
				t.Fatal("served schema has no properties")
			}
			for _, field := range tc.fields {
				if _, ok := properties[field]; !ok {
					t.Errorf("served %s schema lacks %q; update the contracts dependency", tc.schema, field)
				}
			}
			raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "schemas", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			var authored map[string]any
			if err := json.Unmarshal(raw, &authored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(served, authored) {
				t.Errorf("served %s schema differs from contracts/schemas/%s; update the contracts dependency", tc.schema, tc.file)
			}
		})
	}
}

package api

import (
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openapi.yaml is the contract the generated client in client/ is built from. It is hand-written, so
// this holds it to the routes the API serves: the same methods, the same paths, and the same scope
// per route (x-scope; "authenticated" is the spec's word for the empty table scope).
func TestSpecDeclaresTheServedRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Scope string `yaml:"x-scope"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	declared := map[string]string{}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			declared[strings.ToUpper(method)+" "+path] = op.Scope
		}
	}
	served := map[string]string{}
	for _, rt := range Routes() {
		scope := rt.Scope
		if scope == ScopeAuthenticated {
			scope = "authenticated"
		}
		served[rt.Method+" "+rt.Pattern] = scope
	}

	for k, scope := range served {
		got, ok := declared[k]
		switch {
		case !ok:
			t.Errorf("%s is served but not in openapi.yaml", k)
		case got != scope:
			t.Errorf("%s: openapi.yaml x-scope %q, served scope %q", k, got, scope)
		}
	}
	var extra []string
	for k := range declared {
		if _, ok := served[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		t.Errorf("%s is in openapi.yaml but not served", k)
	}
}

package catalog

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openapi.yaml is the contract the generated client in client/ is built from. It is hand-written, so
// this holds it to the three places the service already states the same contract:
//   - Routes: the same operations on the same methods and paths, nothing extra either way;
//   - Scope: each operation's x-scope is the scope Call enforces for it;
//   - argumentSchema: each operation's argument names and required set are the MCP input schema's,
//     whichever of the id/schema selectors, the args envelope, the body or the Idempotency-Key header
//     carries them.
func TestSpecMatchesRoutesScopesAndArguments(t *testing.T) {
	raw, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	declared := map[string]map[string]any{}
	for path, item := range asMap(spec["paths"]) {
		for method, op := range asMap(item) {
			declared[strings.ToUpper(method)+" "+path] = asMap(op)
		}
	}

	served := map[string]bool{}
	for _, rt := range Routes {
		key := rt.Method + " " + rt.Path
		served[key] = true
		op, ok := declared[key]
		if !ok {
			t.Errorf("%s (%s) is served but not in openapi.yaml", key, rt.Operation)
			continue
		}
		if got := op["x-operation"]; got != rt.Operation {
			t.Errorf("%s: x-operation %v, served operation %q", key, got, rt.Operation)
		}
		if got := op["x-scope"]; got != Scope(rt.Operation) {
			t.Errorf("%s: x-scope %v, enforced scope %q", key, got, Scope(rt.Operation))
		}

		names, required := specArguments(t, spec, op)
		wantProps := asMap(argumentSchema(rt.Operation).(map[string]any)["properties"])
		var want []string
		for n := range wantProps {
			want = append(want, n)
		}
		if got, w := sortedKeys(names), sorted(want); strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("%s: spec arguments %v, MCP input schema %v", key, got, w)
		}
		wantRequired, _ := argumentSchema(rt.Operation).(map[string]any)["required"].([]string)
		if got, w := sortedKeys(required), sorted(wantRequired); strings.Join(got, ",") != strings.Join(w, ",") {
			t.Errorf("%s: spec requires %v, MCP input schema requires %v", key, got, w)
		}
	}
	var extra []string
	for key := range declared {
		if !served[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		t.Errorf("%s is in openapi.yaml but not served", key)
	}
}

// specArguments is the set of argument names an operation takes in the spec, and the required ones.
func specArguments(t *testing.T, spec, op map[string]any) (names, required map[string]bool) {
	t.Helper()
	names, required = map[string]bool{}, map[string]bool{}
	addSchema := func(s map[string]any) {
		for n := range asMap(s["properties"]) {
			names[n] = true
		}
		if rs, ok := s["required"].([]any); ok {
			for _, r := range rs {
				required[fmt.Sprint(r)] = true
			}
		}
	}
	jsonSchema := func(holder map[string]any) map[string]any {
		return deref(t, spec, asMap(asMap(asMap(holder["content"])["application/json"])["schema"]))
	}
	params, _ := op["parameters"].([]any)
	for _, p := range params {
		p := deref(t, spec, asMap(p))
		name, in, req := fmt.Sprint(p["name"]), fmt.Sprint(p["in"]), p["required"] == true
		switch {
		case in == "query" && name == "args":
			addSchema(jsonSchema(p))
		case in == "query" && (name == "id" || name == "schema"):
			names[name] = true
			if req {
				required[name] = true
			}
		case in == "header" && name == "Idempotency-Key":
			names["idempotency_key"] = true
			if req {
				required["idempotency_key"] = true
			}
		}
	}
	if body, ok := op["requestBody"]; ok {
		addSchema(jsonSchema(deref(t, spec, asMap(body))))
	}
	return names, required
}

// deref follows a local "#/..." $ref chain.
func deref(t *testing.T, root, node map[string]any) map[string]any {
	t.Helper()
	for {
		ref, ok := node["$ref"].(string)
		if !ok {
			return node
		}
		cur := root
		for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			next, ok := cur[seg].(map[string]any)
			if !ok {
				t.Fatalf("unresolved $ref %s", ref)
			}
			cur = next
		}
		node = cur
	}
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func sorted(s []string) []string { s = append([]string(nil), s...); sort.Strings(s); return s }
func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

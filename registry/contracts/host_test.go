package kinds_test

import (
	"encoding/json"
	kinds "github.com/hollis-labs/tools/registry/contracts"
	"reflect"
	"strings"
	"testing"
)

const hostWire = `{"schema":"host/1","id":"example-host","name":"Example host","roles":["worker","build"],"os":"Example Linux","tailnet":{"hostname":"host.example.test","address":"192.0.2.10"},"services":[{"authority":"registry","kind":"service","id":"service:example"}],"owner":"user:example","operator":{"authority":"registry","kind":"agency","id":"agency:fleet"},"project":{"authority":"registry","kind":"project","id":"project:example"},"config":[{"authority":"git","kind":"config","id":"inventory/host"}],"runbook":{"authority":"git","kind":"document","id":"docs/host.md"}}`

func TestHostContract(t *testing.T) {
	v, err := kinds.ParseContentJSON(kinds.HostSchema, []byte(hostWire))
	if err != nil {
		t.Fatal(err)
	}
	h := v.(*kinds.Host)
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	_ = json.Unmarshal([]byte(hostWire), &before)
	_ = json.Unmarshal(raw, &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("roundtrip changed host")
	}
	id, err := kinds.CanonicalContentID(kinds.HostSchema, h.ID)
	if err != nil || id != "host:example-host" {
		t.Fatalf("identity %q %v", id, err)
	}
	refs, err := kinds.SemanticReferences(kinds.HostSchema, raw)
	want := []kinds.SemanticReference{{Relation: kinds.OperatedBy, Reference: h.Operator}, {Relation: kinds.PartOf, Reference: h.Project}}
	if err != nil || !reflect.DeepEqual(refs, want) {
		t.Fatalf("edges %+v error %v", refs, err)
	}
	for _, tailnet := range []kinds.HostTailnet{{Hostname: "host.example.test"}, {Address: "2001:db8::1"}} {
		h.Tailnet = tailnet
		h.Services = []kinds.Reference{}
		if err := h.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostRejectsInvalidInventory(t *testing.T) {
	cases := map[string]string{
		"id": `"bad/host"`, "name": `""`, "roles": `["server"]`, "os": `""`,
		"tailnet": `{}`, "services": `[{"name":"inline service"}]`, "operator": `"fleet"`,
		"project": `{"authority":"registry","kind":"service","id":"example"}`,
		"config":  `[{"authority":"git","kind":"service","id":"example"}]`,
		"runbook": `{"authority":"git","kind":"config","id":"example"}`,
		"history": `[]`, "credentials": `"forbidden"`,
	}
	check := func(t *testing.T, field, value string) {
		t.Helper()
		var v map[string]json.RawMessage
		if err := json.Unmarshal([]byte(hostWire), &v); err != nil {
			t.Fatal(err)
		}
		if value == "" {
			delete(v, field)
		} else {
			v[field] = json.RawMessage(value)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kinds.ParseContentJSON(kinds.HostSchema, raw); err == nil {
			t.Fatal("accepted invalid host")
		}
	}
	for field, value := range cases {
		t.Run(field, func(t *testing.T) { check(t, field, value) })
	}
	for _, field := range []string{"id", "name", "roles", "os", "tailnet", "services", "owner", "operator", "project"} {
		t.Run("missing/"+field, func(t *testing.T) { check(t, field, "") })
	}
	check(t, "id", `"`+strings.Repeat("a", 64)+`"`)
	for _, value := range []string{`[]`, `["hub","hub"]`} {
		check(t, "roles", value)
	}
	for _, value := range []string{`{"address":"https://example.test"}`, `{"address":"fe80::1%eth0"}`, `{"hostname":"bad/host"}`, `{"hostname":"host.example.test","token":"forbidden"}`} {
		check(t, "tailnet", value)
	}
}

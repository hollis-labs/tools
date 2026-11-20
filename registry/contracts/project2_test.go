package kinds_test

import (
	"encoding/json"
	"strings"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

func TestProjectVersionsAreIndependent(t *testing.T) {
	v1 := []byte(projectJSON)
	if err := kinds.ValidateJSON("project/1", v1); err != nil {
		t.Fatal(err)
	}
	if err := kinds.ValidateJSON("project/2", v1); err == nil {
		t.Fatal("project/1 accepted as project/2")
	}
	v2 := strings.Replace(projectJSON, `"schema":"project/1"`, `"schema":"project/2","type":"service","disposition":"continue"`, 1)
	if err := kinds.ValidateJSON("project/1", []byte(v2)); err == nil {
		t.Fatal("project/2 accepted as project/1")
	}
	for _, typ := range []string{"framework", "monorepo", "lib", "service", "app", "tool"} {
		for _, disposition := range []string{"continue", "tbd", "experiment", "decompose", "dissolve", "retire", "parked", "harvested"} {
			raw := strings.Replace(v2, `"type":"service"`, `"type":"`+typ+`"`, 1)
			raw = strings.Replace(raw, `"disposition":"continue"`, `"disposition":"`+disposition+`"`, 1)
			if typ == "lib" {
				raw = strings.Replace(raw, `"type":"cli"`, `"type":"lib"`, 1)
			}
			if err := kinds.ValidateJSON("project/2", []byte(raw)); err != nil {
				t.Fatal(typ, disposition, err)
			}
			for _, tier := range []string{"1", "2", "3"} {
				tiered := strings.Replace(raw, `"class":2`, `"class":`+tier, 1)
				if err := kinds.ValidateJSON("project/2", []byte(tiered)); err != nil {
					t.Fatal(err)
				}
			}
			noTier := strings.Replace(raw, `"class":2,`, "", 1)
			err := kinds.ValidateJSON("project/2", []byte(noTier))
			optional := typ == "lib" || typ == "framework" || typ == "monorepo"
			if (err == nil) != optional {
				t.Fatalf("%s optional=%v: %v", typ, optional, err)
			}
			if optional {
				p, err := kinds.ParseProjectJSON([]byte(noTier))
				if err != nil {
					t.Fatal(err)
				}
				out, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				if err := kinds.ValidateJSON("project/2", out); err != nil {
					t.Fatal(err)
				}
				if id, err := p.CanonicalID(); err != nil || id != "project:sample" {
					t.Fatal(id, err)
				}
			}
		}
	}
	for _, mutate := range []func(string) string{
		func(s string) string { return strings.Replace(s, `,"type":"service"`, "", 1) },
		func(s string) string { return strings.Replace(s, `,"disposition":"continue"`, "", 1) },
		func(s string) string { return strings.Replace(s, `"type":"service"`, `"type":"unknown"`, 1) },
		func(s string) string {
			return strings.Replace(s, `"disposition":"continue"`, `"disposition":"unlisted"`, 1)
		},
		func(s string) string { return strings.Replace(s, `"class":2`, `"class":"lib"`, 1) },
		func(s string) string { return strings.Replace(s, `"class":2`, `"class":"2"`, 1) },
		func(s string) string { return strings.Replace(s, `"class":2`, `"class":null`, 1) },
		func(s string) string { return strings.Replace(s, `"type":"service"`, `"type":"lib"`, 1) },
		func(s string) string {
			return strings.Replace(s, `"type":"service","disposition":"continue"`, `"metadata":{"type":"service","disposition":"continue"}`, 1)
		},
	} {
		if err := kinds.ValidateJSON("project/2", []byte(mutate(v2))); err == nil {
			t.Fatal("invalid project/2 accepted", mutate(v2))
		}
	}
	yaml := "schema: project/2\nid: sample\nname: Sample\ntype: framework\ndisposition: tbd\nlifecycle: active\nvisibility: public\nowner: user:owner\ninterfaces: [{type: cli}]\n"
	if _, err := kinds.ParseProjectYAML([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
}

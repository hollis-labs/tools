package kinds_test

import (
	"encoding/json"
	"reflect"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

func TestDocumentSubtypes(t *testing.T) {
	for _, fields := range []string{
		`"subtype":"architecture","architecture":{"state":"current","scope":"service boundaries"}`,
		`"subtype":"architecture","architecture":{"state":"target","scope":"service boundaries"}`,
		`"subtype":"template","template":{"templates":"task"}`,
		`"subtype":"template","template":{"templates":"plan"}`,
		`"subtype":"template","template":{"templates":"report"}`,
		`"subtype":"template","template":{"templates":"custom artifact"}`,
		`"subtype":"communication","communication":{"channel":"email","audience":"maintainers"}`,
	} {
		t.Run(fields, func(t *testing.T) {
			raw := []byte(`{"schema":"document/1","id":"native/../guide","name":"Guide","format":"markdown","body":"See source.\n",` + fields + `,"references":[{"authority":"repo","kind":"document","id":"docs/../guide.md","uri":"https://example.com/guide"},{"authority":"tesseract","kind":"knowledge","id":"project/example/knowledge/guide"},{"authority":"caster","kind":"artifact","id":"native/ref"}]}`)
			if err := kinds.ValidateJSON(kinds.DocumentSchema, raw); err != nil {
				t.Fatal(err)
			}
			out, err := kinds.ParseContentJSON(kinds.DocumentSchema, raw)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err = json.Unmarshal(raw, &before); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(wire, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("roundtrip lost fields: %s", wire)
			}
			if err = kinds.ValidateJSON(kinds.DocumentSchema, wire); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDocumentSubtypeRequirements(t *testing.T) {
	valid := map[string]map[string]any{
		"architecture":  {"state": "current", "scope": "service"},
		"template":      {"templates": "task"},
		"communication": {"channel": "email", "audience": "maintainers"},
	}
	for subtype, details := range valid {
		base := func() map[string]any {
			copyDetails := map[string]any{}
			for k, v := range details {
				copyDetails[k] = v
			}
			return map[string]any{"schema": "document/1", "id": "d", "name": "D", "format": "plain_text", "body": "See source.", "subtype": subtype, subtype: copyDetails}
		}
		reject := func(name string, record map[string]any) {
			t.Helper()
			t.Run(subtype+"/"+name, func(t *testing.T) {
				raw, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err = kinds.ValidateJSON(kinds.DocumentSchema, raw); err == nil {
					t.Fatalf("accepted %s", raw)
				}
				if _, err = kinds.ParseContentJSON(kinds.DocumentSchema, raw); err == nil {
					t.Fatalf("parsed %s", raw)
				}
			})
		}
		r := base()
		delete(r, subtype)
		reject("missing details", r)
		r = base()
		r[subtype] = nil
		reject("null details", r)
		r = base()
		r[subtype] = "text"
		reject("wrong details type", r)
		for key := range details {
			r = base()
			delete(r[subtype].(map[string]any), key)
			reject("missing "+key, r)
			for _, value := range []any{"", nil, 42} {
				r = base()
				r[subtype].(map[string]any)[key] = value
				reject("invalid "+key, r)
			}
		}
		r = base()
		r[subtype].(map[string]any)["extra"] = true
		reject("unknown detail", r)
		r = base()
		r["subtype"] = "document"
		reject("details on generic subtype", r)
		for other, extra := range valid {
			if other != subtype {
				r = base()
				r[other] = extra
				reject("mixed "+other, r)
			}
		}
		if subtype == "architecture" {
			r = base()
			r[subtype].(map[string]any)["state"] = "future"
			reject("unknown state", r)
		}
	}
	// Every original subtype retains the minimal original shape.
	for _, subtype := range []string{"sop", "workflow", "procedure", "engineering_bible", "document"} {
		raw := []byte(`{"schema":"document/1","id":"d","name":"D","subtype":"` + subtype + `","format":"markdown","body":"Original body"}`)
		if err := kinds.ValidateJSON(kinds.DocumentSchema, raw); err != nil {
			t.Fatal(err)
		}
	}
}

package catalog

import (
	"encoding/json"
	"path/filepath"
	"testing"

	kinds "github.com/hollis-labs/tools/registry/contracts"
)

func TestDocumentSubtypeAdmissionAndRetrieval(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	for _, fields := range []string{
		`"subtype":"architecture","architecture":{"state":"target","scope":"service"}`,
		`"subtype":"template","template":{"templates":"plan"}`,
		`"subtype":"communication","communication":{"channel":"email","audience":"maintainers"}`,
	} {
		var payload map[string]any
		raw := json.RawMessage(`{"schema":"document/1","id":"doc","name":"D","format":"markdown","body":"See source.",` + fields + `}`)
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		subtype := payload["subtype"].(string)
		payload["id"] = subtype
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r := create(t, s, &Input{Schema: kinds.DocumentSchema, Key: subtype, Payload: raw})
		got := callRecord(t, s, writer, "record_get", Args{ID: r.ID, Publication: "draft"})
		if string(got.Payload) != string(raw) {
			t.Fatalf("subtype details changed: %s", got.Payload)
		}
		delete(payload, subtype)
		invalid, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = prepare(&Input{Schema: kinds.DocumentSchema, Key: subtype, Payload: invalid}, "registry"); err == nil {
			t.Fatalf("accepted incomplete %s", subtype)
		}
	}
}

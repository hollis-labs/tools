package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	kinds "github.com/hollis-labs/tools/registry/contracts"
)

func packageInput() *Input {
	return &Input{Schema: kinds.ToolPackageSchema, Key: "agt_fixture/hello@1.2.3", Payload: json.RawMessage(`{"schema":"tool-package/1","id":"agt_fixture/hello@1.2.3","publisher":"agt_fixture","name":"hello","version":"1.2.3","digest":"sha256:` + strings.Repeat("a", 64) + `","artifact":"cas://sha256/` + strings.Repeat("b", 64) + `","entry":"hello","execution":{"kind":"command"},"env":[],"needs":[],"provenance":{"publisher":"msg://agent/example/agt_fixture"}}`)}
}
func TestToolPackageImmutableMapping(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	r := create(t, s, packageInput())
	if r.ID != "tool-package:agt_fixture/hello@1.2.3" || r.Publication != "draft" {
		t.Fatalf("unexpected record: %+v", r)
	}
	for _, field := range []string{"digest", "artifact", "summary", "entry"} {
		t.Run(field, func(t *testing.T) {
			in := packageInput()
			var payload map[string]any
			_ = json.Unmarshal(in.Payload, &payload)
			switch field {
			case "digest":
				payload[field] = "sha256:" + strings.Repeat("c", 64)
			case "artifact":
				payload[field] = "cas://sha256/" + strings.Repeat("c", 64)
			default:
				payload[field] = "changed"
			}
			in.Payload, _ = json.Marshal(payload)
			_, err := s.Call(writer, "record_update", Args{Publication: "draft", ID: r.ID, Version: r.VersionToken, Record: in})
			code(t, err, "version_conflict")
			// Imports use savePrepared/saveRecord too. Check that the common boundary
			// refuses changed bytes even when a caller has bypassed native update.
			next, err := prepare(in, "registry")
			if err != nil {
				t.Fatal(err)
			}
			err = s.DB.WriteTx(context.Background(), func(ctx context.Context, tx store.Tx) error { return save(ctx, tx, &next) })
			code(t, err, "version_conflict")
		})
	}
	// Human keys and publication authority cannot create another mapping for
	// the same publisher/name@version.
	in := packageInput()
	in.Key = "alternate-key"
	in.Payload = json.RawMessage(strings.Replace(string(in.Payload), strings.Repeat("a", 64), strings.Repeat("c", 64), 1))
	_, err := s.Call(writer, "record_create", Args{Publication: "draft", Record: in})
	code(t, err, "version_conflict")
	_, err = s.Call(caller("other", "registry.write"), "record_create", Args{Publication: "draft", Record: in})
	code(t, err, "version_conflict")
	// A distinct semantic version may point at new bytes.
	newer := packageInput()
	newer.Key = "agt_fixture/hello@1.2.4"
	newer.Payload = json.RawMessage(strings.ReplaceAll(strings.Replace(string(newer.Payload), strings.Repeat("a", 64), strings.Repeat("c", 64), 1), "1.2.3", "1.2.4"))
	created := create(t, s, newer)
	if created.ID != "tool-package:agt_fixture/hello@1.2.4" {
		t.Fatal("new version identity lost")
	}
	// Object ordering and whitespace do not change an immutable package.
	reordered := packageInput()
	var fields map[string]any
	_ = json.Unmarshal(reordered.Payload, &fields)
	reordered.Payload, _ = json.Marshal(fields)
	var indented bytes.Buffer
	_ = json.Indent(&indented, reordered.Payload, "", "  ")
	reordered.Payload = indented.Bytes()
	r = callRecord(t, s, writer, "record_update", Args{Publication: "draft", ID: r.ID, Version: r.VersionToken, Record: reordered})
	// Identical payload updates and archive preserve the original pin.
	r = callRecord(t, s, writer, "record_update", Args{Publication: "draft", ID: r.ID, Version: r.VersionToken, Record: packageInput()})
	r = callRecord(t, s, writer, "record_archive", Args{Publication: "draft", ID: r.ID, Version: r.VersionToken})
	if r.Lifecycle != "archived" {
		t.Fatal("archive refused")
	}
	_, err = s.Call(writer, "record_create", Args{Publication: "draft", Record: in})
	code(t, err, "version_conflict")
	r = callRecord(t, s, writer, "record_delete", Args{Publication: "draft", ID: r.ID, Version: r.VersionToken})
	_, err = s.Call(writer, "record_create", Args{Publication: "draft", Record: in})
	code(t, err, "version_conflict")
}

package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestImportMappingChangeCanRenormalizeSameSourceRevision(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	request := batch("one", "", "a")
	first := importPlan(t, s, request)
	if _, err := s.Call(writer, "import_commit", commitArgs(first, "first")); err != nil {
		t.Fatal(err)
	}
	request.ExpectedCheckpoint = "one"
	request.MappingVersion = "fixture/2"
	request.Entries[0].Record.Summary = "new normalization"
	second := importPlan(t, s, request)
	if second.Counts.Updated != 1 || second.Counts.Rejected != 0 {
		t.Fatal(second)
	}
	if _, err := s.Call(writer, "import_commit", commitArgs(second, "second")); err != nil {
		t.Fatal(err)
	}
	r := callRecord(t, s, writer, "record_get", Args{Publication: "draft", ID: "project:a"})
	if r.Summary != "new normalization" || r.MappingVersion != "fixture/2" {
		t.Fatal(r)
	}
	// A mapping change does not authorize overwriting a local edit.
	native := input("a")
	native.Summary = "native edit"
	r = callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: native})
	request.MappingVersion = "fixture/3"
	request.Entries[0].Record.Summary = "another normalization"
	conflict := importPlan(t, s, request)
	if conflict.Items[0].Reason != "both_sides_changed" {
		t.Fatal(conflict)
	}
	// The immutable-source invariant still holds within the same mapping.
	other := openTest(t, filepath.Join(t.TempDir(), "db"))
	request = batch("one", "", "a")
	first = importPlan(t, other, request)
	if _, err := other.Call(writer, "import_commit", commitArgs(first, "first")); err != nil {
		t.Fatal(err)
	}
	request.ExpectedCheckpoint = "one"
	request.Entries[0].Record.Summary = "changed bytes"
	rejected := importPlan(t, other, request)
	if rejected.Items[0].Reason != "source_revision_reused" {
		t.Fatal(rejected)
	}
}

func TestImportChecksExistingForbiddenEdgeInReachableCycle(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	b := input("b")
	var payload map[string]any
	if err := json.Unmarshal(b.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["edges"] = []map[string]string{{"type": "part_of", "to": "project:a"}}
	b.Payload, _ = json.Marshal(payload)
	existing := callRecord(t, s, writer, "record_create", Args{Record: b, AllowUnresolved: true})
	request := batch("one", "", "a")
	if err := json.Unmarshal(request.Entries[0].Record.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["edges"] = []map[string]string{{"type": "consumes", "to": "project:b"}}
	request.Entries[0].Record.Payload, _ = json.Marshal(payload)
	plan := importPlan(t, s, request)
	if plan.Counts.Rejected != 1 || plan.GraphDiagnostic == nil || plan.GraphDiagnostic.RecordID != existing.ID || len(plan.GraphDiagnostic.CyclePath) < 3 {
		t.Fatal(plan)
	}
	_, err := s.Call(writer, "import_commit", commitArgs(plan, "reject"))
	code(t, err, "validation_error")
	if rowCount(t, s, "records") != 1 || rowCount(t, s, "import_sources") != 0 || rowCount(t, s, "import_checkpoints") != 0 {
		t.Fatal("cycle rejection mutated canonical state")
	}
	current := callRecord(t, s, writer, "record_get", Args{Publication: "draft", ID: existing.ID})
	if current.RevisionID != existing.RevisionID {
		t.Fatal("existing record changed")
	}
}

func TestNativeUpdateResetsImportAdmissionMapping(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	plan := importPlan(t, s, batch("one", "", "a"))
	if _, err := s.Call(writer, "import_commit", commitArgs(plan, "first")); err != nil {
		t.Fatal(err)
	}
	before := callRecord(t, s, writer, "record_get", Args{Publication: "draft", ID: "project:a"})
	native := input("a")
	native.Summary = "native"
	after := callRecord(t, s, writer, "record_update", Args{ID: before.ID, Version: before.VersionToken, Record: native})
	if after.MappingVersion != "native/1" {
		t.Fatal(after.MappingVersion)
	}
	subject, err := subjectFor(context.Background(), s.DB, after, nil)
	if err != nil || subject.Admission.MappingVersion != "native/1" {
		t.Fatal(subject, err)
	}
	old, found, err := sourceState(context.Background(), s.DB, "registry", "fixture", "a")
	if err != nil || !found || old.MappingVersion != "fixture/1" || old.RegistryRevision != before.RevisionID {
		t.Fatal(old, err)
	}
}

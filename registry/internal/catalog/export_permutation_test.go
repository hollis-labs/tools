package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/libs/core/store"
)

// These are isolated historical publication fixtures, including config. They do
// not introduce a human approval path. Separate databases retain the same IDs
// and revision tokens while exercising different authored input representations.
func exportPermutationFixture(t *testing.T, root string, reversed bool, mapping string) (*Service, []string) {
	t.Helper()
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	labels := map[string]any{}
	nested := map[string]any{}
	files := []SourceFile{{Path: "references/a.md", Content: "Alpha\n"}, {Path: "references/z.md", Content: "Zulu\n"}}
	config := `{"schema":"config/1","id":"ordering","name":"Ordering","target":{"authority":"registry","kind":"project","id":"project:ordering"},"scope":"project","settings_schema":{"type":"object"},"settings":{"alpha":{"first":1,"second":2},"zulu":true}}`
	if reversed {
		labels["zulu"] = "last"
		labels["alpha"] = "first"
		nested["second"] = 2
		nested["first"] = 1
		files[0], files[1] = files[1], files[0]
		config = `{"settings":{"zulu":true,"alpha":{"second":2,"first":1}},"scope":"project","settings_schema":{"type":"object"},"target":{"id":"project:ordering","kind":"project","authority":"registry"},"name":"Ordering","id":"ordering","schema":"config/1"}`
	} else {
		labels["alpha"] = "first"
		labels["zulu"] = "last"
		nested["first"] = 1
		nested["second"] = 2
	}
	metadata := map[string]any{}
	if reversed {
		metadata["nested"] = nested
		metadata["labels"] = labels
		metadata["skill_files"] = files
	} else {
		metadata["skill_files"] = files
		metadata["labels"] = labels
		metadata["nested"] = nested
	}
	inputs := []*Input{
		{Schema: "project/2", Key: "ordering", Payload: json.RawMessage(`{"schema":"project/2","id":"ordering","name":"Ordering","type":"service","disposition":"continue","class":2,"lifecycle":"active","visibility":"private","owner":"user:test","interfaces":[{"type":"cli","auth":"none"}]}`)},
		{Schema: "skill/1", Key: "ordering", Metadata: metadata, Payload: json.RawMessage(`{"schema":"skill/1","id":"ordering","name":"Ordering","summary":"Ordering fixture","instructions":"Read the attached fixture files."}`)},
		{Schema: "config/1", Key: "ordering", Payload: json.RawMessage(config)},
	}
	ids := make([]string, 0, len(inputs))
	for _, in := range inputs {
		r, err := prepare(in, "registry")
		if err != nil {
			t.Fatal(err)
		}
		r.RevisionID = "fixture-revision-" + r.Kind
		r.VersionToken = r.RevisionID
		r.MappingVersion = mapping
		r.CreatedAt = "2026-01-01T00:00:00Z"
		r.UpdatedAt = r.CreatedAt
		if err = s.DB.WriteTx(context.Background(), func(ctx context.Context, tx store.Tx) error {
			if e := savePrepared(ctx, tx, &r); e != nil {
				return e
			}
			r.Publication = "published"
			r.PublishedBy = "fixture-historical-approval"
			return writePublished(ctx, tx, r)
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return s, ids
}

func readPermutationExport(t *testing.T, s *Service, ids []string) ExportBundle {
	t.Helper()
	v, err := s.Call(writer, "export_plan", Args{Export: &ExportRequest{Target: "fixture", IDs: ids}})
	if err != nil {
		t.Fatal(err)
	}
	return v.(ExportPlan).Bundle
}

func assertExportOutputBytesEqual(t *testing.T, want, got ExportBundle) {
	t.Helper()
	sources, err := json.Marshal(want.Sources)
	if err != nil {
		t.Fatal(err)
	}
	other, err := json.Marshal(got.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sources, other) {
		t.Fatal("source manifest changed under permutation")
	}
	if len(want.Sources) != 5 || len(want.Artifacts.Entries) != 6 || len(got.Artifacts.Entries) != 6 {
		t.Fatal("fixture did not render project, config, skill, attachments and manifest")
	}
	foundManifest := false
	for i, entry := range want.Artifacts.Entries {
		actual := got.Artifacts.Entries[i]
		if entry.Path != actual.Path || !bytes.Equal(entry.Bytes, actual.Bytes) || !reflect.DeepEqual(entry, actual) {
			t.Fatalf("artifact bytes or provenance changed for %s", entry.Path)
		}
		if entry.Path == "registry-sources.json" {
			foundManifest = len(entry.Bytes) > 0
		}
	}
	if !foundManifest {
		t.Fatal("rendered source manifest missing")
	}
}

func TestExportPermutationsPreserveBytesAndAdmissionIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	s, ids := exportPermutationFixture(t, root, false, "fixture/1")
	first := readPermutationExport(t, s, ids)
	// Same database and exact published revisions: only caller scope order changes.
	reversedIDs := []string{ids[2], ids[1], ids[0]}
	reorderedScope := readPermutationExport(t, s, reversedIDs)
	assertExportOutputBytesEqual(t, first, reorderedScope)
	if !reflect.DeepEqual(first.Subjects, reorderedScope.Subjects) || first.Generation != reorderedScope.Generation || first.Digest != reorderedScope.Digest {
		t.Fatal("scope order changed pinned subjects or bundle identity")
	}
	if !reflect.DeepEqual(reversedIDs, []string{ids[2], ids[1], ids[0]}) {
		t.Fatal("planning mutated caller scope")
	}
	// Reorder attachment arrays, map insertion and nested JSON input keys. Fixed
	// source identities/revisions make source manifests directly comparable.
	permuted, permutedIDs := exportPermutationFixture(t, root, true, "fixture/1")
	reorderedInput := readPermutationExport(t, permuted, permutedIDs)
	assertExportOutputBytesEqual(t, first, reorderedInput)
	for i, subject := range reorderedInput.Subjects {
		prior := first.Subjects[i]
		if subject.Record.ID != prior.Record.ID || subject.Record.Revision != prior.Record.Revision || subject.Admission != prior.Admission {
			t.Fatal("input permutation changed source revision or admission fixture")
		}
		if subject.Record.Kind != "skill/1" && subject.Record != prior.Record {
			t.Fatal("JSON object key order changed canonical content identity")
		}
	}
	// Authored arrays remain exact admission inputs even when they render the
	// same files: output determinism must not erase their reviewed content hash.
	if first.Digest == reorderedInput.Digest {
		t.Fatal("authored attachment order lost from exact-content admission")
	}
	// Changing only mapping admission leaves rendered bytes intact but must remain
	// distinguishable in both the approved subject and exported bundle identity.
	remapped, remappedIDs := exportPermutationFixture(t, root, true, "fixture/2")
	changedAdmission := readPermutationExport(t, remapped, remappedIDs)
	assertExportOutputBytesEqual(t, reorderedInput, changedAdmission)
	if reorderedInput.Generation == changedAdmission.Generation || reorderedInput.Digest == changedAdmission.Digest {
		t.Fatal("mapping admission change hidden by output canonicalization")
	}
	for i, subject := range changedAdmission.Subjects {
		prior := reorderedInput.Subjects[i]
		if subject.Record != prior.Record || subject.Admission.MappingVersion != "fixture/2" || prior.Admission.MappingVersion != "fixture/1" {
			t.Fatal("mapping fixture changed record revision/content instead of admission")
		}
	}
}

package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/substrate/kinds"
)

func exportPlan(t *testing.T, s *Service, r ExportRequest) ExportReceipt {
	t.Helper()
	v, e := s.Call(writer, "export_plan", Args{Export: &r})
	if e != nil {
		t.Fatal(e)
	}
	b := v.(ExportPlan).Bundle
	v, e = s.Call(writer, "export_authorize", Args{Bundle: &b, Hash: b.Digest, IdempotencyKey: newID()})
	if e != nil {
		t.Fatal(e)
	}
	return v.(ExportReceipt)
}
func exportDiffArgs(r ExportReceipt) Args { return Args{Bundle: &r.Bundle, Hash: r.Bundle.Digest} }
func exportArgs(r ExportReceipt) Args     { return Args{ReceiptID: r.ID, Hash: r.Bundle.Digest} }
func TestExportDeterminismPublishedOnlyReceiptAndDrift(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "output")
	if e := s.ConfigureExportTargets(map[string]string{"fixture": root}); e != nil {
		t.Fatal(e)
	}
	a := create(t, s, input("a"))
	create(t, s, input("unpublished"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "pub")
	request := ExportRequest{Target: "fixture"}
	first := exportPlan(t, s, request)
	second := exportPlan(t, s, request)
	if !reflect.DeepEqual(first.Bundle, second.Bundle) || len(first.Bundle.Sources) != 1 || first.Bundle.Sources[0].ID != a.ID {
		t.Fatal("non-deterministic or draft leak", first, second)
	}
	if _, e := os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("plan wrote output", e)
	}
	// Draft edits preserve authorized published output.
	next := input("a")
	next.Summary = "draft only"
	a2 := callRecord(t, s, writer, "record_update", Args{ID: a.ID, Version: a.VersionToken, Record: next})
	v, e := s.Call(writer, "export_diff", exportDiffArgs(first))
	if e != nil {
		t.Fatal(e)
	}
	if len(v.(ExportPlan).Materialization.Report.Changes) != 2 {
		t.Fatal(v)
	}
	_, e = s.Call(reviewer, "export_apply", exportArgs(first))
	code(t, e, "forbidden")
	_, e = s.Call(caller("other", "registry.read", "registry.write"), "export_apply", exportArgs(first))
	code(t, e, "not_found")
	bad := exportArgs(first)
	bad.Hash = "changed"
	_, e = s.Call(writer, "export_apply", bad)
	code(t, e, "forbidden")
	v, e = s.Call(writer, "export_apply", exportArgs(first))
	if e != nil {
		t.Fatal(e)
	}
	manifest := v.(ExportOutcome).Handle.Manifest
	sourceBytes, e := os.ReadFile(filepath.Join(root, "registry-sources.json")) // #nosec G304 -- isolated test fixture root
	if e != nil {
		t.Fatal(e)
	}
	var sources []ExportSource
	if e = json.Unmarshal(sourceBytes, &sources); e != nil || !reflect.DeepEqual(sources, first.Bundle.Sources) {
		t.Fatal(string(sourceBytes), e)
	}
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a2.RevisionID, ExpectedPublishedRevision: a.RevisionID})), "pub2")
	_, e = s.Call(writer, "export_diff", exportDiffArgs(first))
	code(t, e, "version_conflict")
	refresh := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: manifest.Generation})
	path := filepath.Join(root, refresh.Bundle.Sources[0].Path)
	if e = os.WriteFile(path, []byte("external drift"), 0o600); e != nil {
		t.Fatal(e)
	}
	v, e = s.Call(writer, "export_diff", exportDiffArgs(refresh))
	if e != nil {
		t.Fatal(e)
	}
	conflict := false
	for _, c := range v.(ExportPlan).Materialization.Report.Changes {
		if c.Kind == materialize.ChangeConflict {
			conflict = true
		}
	}
	if !conflict {
		t.Fatal("drift not reported", v)
	}
	_, e = s.Call(writer, "export_apply", exportArgs(refresh))
	code(t, e, "version_conflict")
	raw, e := os.ReadFile(path) // #nosec G304 -- isolated test fixture output
	if e != nil || string(raw) != "external drift" {
		t.Fatal("drift overwritten", e)
	}
}
func TestExportRenderMappingsAndOpaqueIDs(t *testing.T) {
	values := []struct{ schema, payload, kind string }{
		{kinds.AgentSchema, `{"schema":"agent/1","id":"../alpha","name":"Alpha","charter":"Charter","roles":["worker"]}`, "agent"},
		{kinds.SkillSchema, `{"schema":"skill/1","id":"skill-a","name":"Skill","summary":"Summary","instructions":"Instructions"}`, "skill"},
		{kinds.DocumentSchema, `{"schema":"document/1","id":"doc-a","name":"Doc","subtype":"procedure","format":"markdown","body":"Body"}`, "document"},
		{kinds.ConfigSchema, `{"schema":"config/1","id":"cfg-a","name":"Config","target":{"authority":"external","kind":"project","id":"fixture"},"scope":"project","settings_schema":{"type":"object"},"settings":{"enabled":true}}`, "config"},
	}
	for _, tt := range values {
		t.Run(tt.kind, func(t *testing.T) {
			r, e := prepare(&Input{Schema: tt.schema, Key: tt.kind, Payload: json.RawMessage(tt.payload)}, "registry")
			if e != nil {
				t.Fatal(e)
			}
			path, raw, e := renderExport(r)
			if e != nil || len(raw) == 0 || filepath.IsAbs(path) || filepath.Clean(path) != path {
				t.Fatal(path, string(raw), e)
			}
			path2, raw2, e := renderExport(r)
			if e != nil || path != path2 || string(raw) != string(raw2) {
				t.Fatal("render changed")
			}
		})
	}
}
func TestExportDisabledBindingsAndPublicationSetCAS(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	_, e := s.Call(writer, "export_plan", Args{Export: &ExportRequest{Target: "arbitrary"}})
	code(t, e, "forbidden")
	if e = s.ConfigureExportTargets(map[string]string{"fixture": filepath.Join(t.TempDir(), "output")}); e != nil {
		t.Fatal(e)
	}
	p := exportPlan(t, s, ExportRequest{Target: "fixture"})
	a := create(t, s, input("new"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "new-pub")
	_, e = s.Call(writer, "export_authorize", Args{Bundle: &p.Bundle, Hash: p.Bundle.Digest, IdempotencyKey: newID()})
	code(t, e, "version_conflict")
	if _, e = s.Call(writer, "export_apply", exportArgs(p)); e != nil {
		t.Fatal("issued authorization must survive later publication", e)
	}
	// Stored receipts are still verified at the materializer boundary.
	var raw []byte
	if e = s.DB.QueryRowContext(context.Background(), `SELECT body FROM export_receipts WHERE id=?`, p.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var tampered ExportReceipt
	if e = json.Unmarshal(raw, &tampered); e != nil {
		t.Fatal(e)
	}
	tampered.Bundle.Artifacts.Entries[0].Bytes = []byte("tampered")
	raw, _ = json.Marshal(tampered)
	if _, e = s.DB.ExecContext(context.Background(), `UPDATE export_receipts SET body=? WHERE id=?`, raw, p.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Call(writer, "export_apply", exportArgs(p))
	code(t, e, "forbidden")
}
func TestExportApplyRerunUsesSameGeneration(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "out")
	if e := s.ConfigureExportTargets(map[string]string{"fixture": root}); e != nil {
		t.Fatal(e)
	}
	a := create(t, s, input("a"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "publish")
	first := exportPlan(t, s, ExportRequest{Target: "fixture"})
	v, e := s.Call(writer, "export_apply", exportArgs(first))
	if e != nil {
		t.Fatal(e)
	}
	generation := v.(ExportOutcome).Handle.Manifest.Generation
	_, e = s.Call(writer, "export_apply", exportArgs(first))
	code(t, e, "version_conflict")
	fresh := exportPlan(t, s, ExportRequest{Target: "fixture"})
	v, e = s.Call(writer, "export_apply", exportArgs(fresh))
	if e != nil {
		t.Fatal(e)
	}
	if v.(ExportOutcome).Handle.Manifest.Generation != generation {
		t.Fatal("generation changed on replay")
	}
	next := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: generation})
	v, e = s.Call(writer, "export_apply", exportArgs(next))
	if e != nil {
		t.Fatal(e)
	}
	if v.(ExportOutcome).Handle.Manifest.Generation != generation {
		t.Fatal("generation changed for same content")
	}
	for _, c := range v.(ExportOutcome).Handle.Report.Changes {
		if c.Kind != materialize.ChangeUnchanged {
			t.Fatal("rerun changed files", c)
		}
	}
}

func TestExportOutputPreflight(t *testing.T) {
	root := t.TempDir()
	before, err := inspectExportOutput(root)
	if err != nil || before != "absent" {
		t.Fatal(before, err)
	}
	path := materialize.ManifestPath(root)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := inspectExportOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := inspectExportOutput(root)
	if err != nil || first == second {
		t.Fatal("manifest bytes not distinguished", err)
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err = inspectExportOutput(root); err == nil {
		t.Fatal("accepted nested symlink")
	}
	if _, err = inspectExportOutput(filepath.Join(root, "link", "new")); err == nil {
		t.Fatal("accepted symlink ancestor")
	}
}

func TestExportAuthorizationPinsOutputManifest(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "output")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	a := create(t, s, input("manifest"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "pub")
	first := exportPlan(t, s, ExportRequest{Target: "fixture"})
	v, err := s.Call(writer, "export_apply", exportArgs(first))
	if err != nil {
		t.Fatal(err)
	}
	next := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: v.(ExportOutcome).Handle.Manifest.Generation})
	path := materialize.ManifestPath(root)
	raw, err := os.ReadFile(path) // #nosec G304 -- isolated output fixture
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil { // #nosec G703 -- isolated output fixture
		t.Fatal(err)
	}
	_, err = s.Call(writer, "export_apply", exportArgs(next))
	code(t, err, "version_conflict")
	v, err = s.Call(writer, "export_attempt_get", Args{ReceiptID: next.ID})
	if err != nil || v.(ExportOutcome).State != "failed" {
		t.Fatal(v, err)
	}
}

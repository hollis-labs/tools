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
	plan := v.(ExportPlan)
	v, e = s.Call(writer, "export_authorize", Args{Export: &r, Hash: plan.Bundle.Digest})
	if e != nil {
		t.Fatal(e)
	}
	return v.(ExportReceipt)
}
func exportArgs(r ExportReceipt) Args { return Args{ReceiptID: r.ID, Hash: r.Bundle.Digest} }
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
	v, e := s.Call(writer, "export_diff", exportArgs(first))
	if e != nil {
		t.Fatal(e)
	}
	if len(v.(materialize.Plan).Report.Changes) != 2 {
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
	manifest := v.(materialize.Handle).Manifest
	sourceBytes, e := os.ReadFile(filepath.Join(root, "registry-sources.json")) // #nosec G304 -- isolated test fixture root
	if e != nil {
		t.Fatal(e)
	}
	var sourceManifest struct {
		Sources []ExportSource `json:"sources"`
		Pins    []ExportPin    `json:"pins"`
	}
	if e = json.Unmarshal(sourceBytes, &sourceManifest); e != nil || !reflect.DeepEqual(sourceManifest.Sources, first.Bundle.Sources) {
		t.Fatal(string(sourceBytes), e)
	}
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a2.RevisionID, ExpectedPublishedRevision: a.RevisionID})), "pub2")
	_, e = s.Call(writer, "export_diff", exportArgs(first))
	code(t, e, "version_conflict")
	refresh := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: manifest.Generation})
	path := filepath.Join(root, refresh.Bundle.Sources[0].Path)
	if e = os.WriteFile(path, []byte("external drift"), 0o600); e != nil {
		t.Fatal(e)
	}
	v, e = s.Call(writer, "export_diff", exportArgs(refresh))
	if e != nil {
		t.Fatal(e)
	}
	conflict := false
	for _, c := range v.(materialize.Plan).Report.Changes {
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
	_, e = s.Call(writer, "export_apply", exportArgs(p))
	if e != nil {
		t.Fatal("later publication revoked snapshot", e)
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
	generation := v.(materialize.Handle).Manifest.Generation
	_, e = s.Call(writer, "export_apply", exportArgs(first))
	code(t, e, "version_conflict")
	next := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: generation})
	v, e = s.Call(writer, "export_apply", exportArgs(next))
	if e != nil {
		t.Fatal(e)
	}
	if v.(materialize.Handle).Manifest.Generation != generation {
		t.Fatal("generation changed for same content")
	}
	for _, c := range v.(materialize.Handle).Report.Changes {
		if c.Kind != materialize.ChangeUnchanged {
			t.Fatal("rerun changed files", c)
		}
	}
}

func TestExportReadOnlyPlanAndSnapshotAuthorization(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "out")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b.RevisionID})), "b")
	a = callRecord(t, s, writer, "relation_create", Args{ID: a.ID, Version: a.VersionToken, Relation: &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: a.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: b.ID}, Source: "authored", Status: "proposed"}})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "a")
	request := ExportRequest{Target: "fixture", IDs: []string{a.ID}}
	v, err := s.Call(writer, "export_plan", Args{Export: &request})
	if err != nil {
		t.Fatal(err)
	}
	first := v.(ExportPlan)
	var count int
	if err = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM export_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("plan wrote receipt", count, err)
	}
	b2 := callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b.VersionToken, Record: input("b")})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b2.RevisionID, ExpectedPublishedRevision: b.RevisionID})), "b2")
	v, err = s.Call(writer, "export_plan", Args{Export: &request})
	if err != nil {
		t.Fatal(err)
	}
	second := v.(ExportPlan)
	if first.Bundle.Digest != second.Bundle.Digest {
		t.Fatal("new target publication silently repinned export")
	}
	pinned := false
	for _, p := range second.Bundle.Pins {
		if p.ID == b.ID && p.Revision == b.RevisionID {
			pinned = true
		}
	}
	if !pinned {
		t.Fatal("dependency not pinned")
	}
	v, err = s.Call(writer, "export_authorize", Args{Export: &request, Hash: second.Bundle.Digest})
	if err != nil {
		t.Fatal(err)
	}
	receipt := v.(ExportReceipt)
	callRecord(t, s, writer, "record_archive", Args{ID: b2.ID, Version: b2.VersionToken})
	_, err = s.Call(writer, "export_authorize", Args{Export: &request, Hash: second.Bundle.Digest})
	code(t, err, "reference_unavailable")
	if _, err = s.Call(writer, "export_apply", exportArgs(receipt)); err != nil {
		t.Fatal("archive after authorization revoked issued snapshot", err)
	}
	v, err = s.Call(writer, "export_attempt_get", Args{ReceiptID: receipt.ID})
	if err != nil || v.(ExportAttempt).State != "completed" {
		t.Fatal(v, err)
	}
}

func TestExportClaimSurvivesRestartAndRejectsDestinationChange(t *testing.T) {
	db := filepath.Join(t.TempDir(), "db")
	s := openTest(t, db)
	root := filepath.Join(t.TempDir(), "out")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	p := exportPlan(t, s, ExportRequest{Target: "fixture"})
	other := filepath.Join(t.TempDir(), "other")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": other}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Call(writer, "export_apply", exportArgs(p))
	code(t, err, "forbidden")
	if _, err = os.Stat(other); !os.IsNotExist(err) {
		t.Fatal("rebound root was written")
	}
	if err = s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	// Persist the state a crash would leave after its single-start claim.
	raw, _ := json.Marshal(ExportAttempt{ReceiptID: p.ID, State: "started", StartedAt: "fixture"})
	if _, err = s.DB.ExecContext(context.Background(), `UPDATE export_attempts SET body=? WHERE receipt_id=?`, raw, p.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, db)
	if err = s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(writer, "export_apply", exportArgs(p))
	code(t, err, "version_conflict")
	v, err := s.Call(writer, "export_attempt_get", Args{ReceiptID: p.ID})
	if err != nil || v.(ExportAttempt).State != "started" {
		t.Fatal(v, err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("uncertain attempt restarted")
	}
}

func TestExportExpiredConcurrentAndFailedAttempts(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "out")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	p := exportPlan(t, s, ExportRequest{Target: "fixture"})
	expired := p
	expired.StartDeadline = "2000-01-01T00:00:00Z"
	raw, _ := json.Marshal(expired)
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE export_receipts SET body=? WHERE id=?`, raw, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.Call(writer, "export_apply", exportArgs(p))
	code(t, err, "version_conflict")
	p = exportPlan(t, s, ExportRequest{Target: "fixture"})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := s.Call(writer, "export_apply", exportArgs(p)); results <- err }()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else {
			code(t, err, "version_conflict")
		}
	}
	if successes != 1 {
		t.Fatal("receipt started more than once", successes)
	}
	p = exportPlan(t, s, ExportRequest{Target: "fixture"})
	manifest := filepath.Join(root, "registry-sources.json")
	if err = os.WriteFile(manifest, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(writer, "export_apply", exportArgs(p))
	code(t, err, "version_conflict")
	v, err := s.Call(writer, "export_attempt_get", Args{ReceiptID: p.ID})
	if err != nil || v.(ExportAttempt).State != "failed" || v.(ExportAttempt).Handle == nil {
		t.Fatal("failed outcome not retained", v, err)
	}
	_, err = s.Call(writer, "export_apply", exportArgs(p))
	code(t, err, "version_conflict")
}

func TestExportRefusesControlSymlinkAndMissingNativeBundle(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "out")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	p := exportPlan(t, s, ExportRequest{Target: "fixture"})
	if _, err := s.Call(writer, "export_apply", exportArgs(p)); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Rename(filepath.Join(root, ".materialize"), filepath.Join(root, "saved-control")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".materialize")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Call(writer, "export_plan", Args{Export: &ExportRequest{Target: "fixture"}})
	code(t, err, "version_conflict")
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("control symlink wrote outside", err)
	}
	r, err := prepare(&Input{Schema: kinds.AgentSchema, Key: "agent", Payload: json.RawMessage(`{"schema":"agent/1","id":"agent","name":"Agent","charter":"Read","roles":["reader"]}`)}, "registry")
	if err != nil {
		t.Fatal(err)
	}
	_, err = renderExportFiles(r)
	code(t, err, "non_materializable")
}

func TestExportRemovesOnlyUndriftedOwnedFiles(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "out")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	r := create(t, s, input("removed"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})), "published")
	first := exportPlan(t, s, ExportRequest{Target: "fixture"})
	v, err := s.Call(writer, "export_apply", exportArgs(first))
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(root, first.Bundle.Sources[0].Path)
	unowned := filepath.Join(root, "operator.txt")
	if err = os.WriteFile(unowned, []byte("operator"), 0600); err != nil {
		t.Fatal(err)
	}
	callRecord(t, s, writer, "record_archive", Args{ID: r.ID, Version: r.VersionToken})
	next := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: v.(materialize.Handle).Manifest.Generation})
	if _, err = s.Call(writer, "export_apply", exportArgs(next)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(owned); !os.IsNotExist(err) {
		t.Fatal("withdrawn output retained", err)
	}
	raw, err := os.ReadFile(unowned)
	if err != nil || string(raw) != "operator" {
		t.Fatal("unowned output changed", err)
	}
}

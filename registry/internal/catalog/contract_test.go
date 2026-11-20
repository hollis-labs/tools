package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize/artifact"
)

func rowCount(t *testing.T, s *Service, table string) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestPreparationDurableReplayExpiryAndNoCanonicalReservation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	request := batch("one", "", "a", "b")
	args := Args{Import: &request, IdempotencyKey: "durable-key"}
	v, e := s.Call(writer, "import_prepare", args)
	if e != nil {
		t.Fatal(e)
	}
	first := v.(ImportPlan)
	v, e = s.Call(writer, "import_prepare", args)
	if e != nil || !reflect.DeepEqual(first, v) {
		t.Fatal("lost prepare response changed manifest", e)
	}
	if rowCount(t, s, "records") != 0 || rowCount(t, s, "publication_events") != 0 || first.Items[0].ProposedRevision == "" {
		t.Fatal("staging leaked or failed to allocate")
	}
	// Another preparation can target exactly the same canonical identities.
	other := importPlan(t, s, request)
	if other.Items[0].ProposedRevision == first.Items[0].ProposedRevision {
		t.Fatal("revision reused")
	}
	v, e = s.Call(writer, "import_commit", commitArgs(first, "commit"))
	if e != nil {
		t.Fatal(e)
	}
	result := v.(ImportResult)
	r := callRecord(t, s, writer, "record_get", Args{ID: first.Items[0].ID, Publication: "draft"})
	if r.RevisionID != first.Items[0].ProposedRevision {
		t.Fatal("commit regenerated prepared revision")
	}
	_, e = s.Call(writer, "import_commit", commitArgs(other, "other-commit"))
	code(t, e, "version_conflict")
	if rowCount(t, s, "import_failures") != 1 || rowCount(t, s, "records") != 2 || rowCount(t, s, "import_plans") != 2 {
		t.Fatal("failed commit lost staging or altered canonical rows")
	}
	now = now.Add(25 * time.Hour)
	v, e = s.Call(writer, "import_prepare", args)
	if e != nil {
		t.Fatal(e)
	}
	expired := v.(ImportPlan)
	if !expired.Expired || expired.ID != first.ID || expired.Digest != first.Digest || expired.Items[0].ProposedRevision != first.Items[0].ProposedRevision {
		t.Fatal("expired preparation reallocated", expired)
	}
	_, e = s.Call(writer, "import_commit", commitArgs(other, "expired-commit"))
	code(t, e, "version_conflict")
	v, e = s.Call(writer, "import_commit", commitArgs(first, "commit"))
	if e != nil || !v.(ImportResult).Replayed || v.(ImportResult).ReceiptID != result.ReceiptID {
		t.Fatal("committed replay failed after expiry", v, e)
	}
	changed := request
	changed.Checkpoint = "different"
	_, e = s.Call(writer, "import_prepare", Args{Import: &changed, IdempotencyKey: "durable-key"})
	code(t, e, "version_conflict")
	if rowCount(t, s, "import_plans") != 2 {
		t.Fatal("expired retry allocated")
	}
}
func TestPreparationQuotasNoAllocationAndConfiguredCaps(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	defaults := DefaultImportLimits()
	if defaults.MaxActive != 20 || defaults.MaxBytes != 2<<20 || defaults.MaxItems != 200 || defaults.EligibilitySeconds != 86400 {
		t.Fatal(defaults)
	}
	limits := defaults
	limits.MaxActive = 2
	if e := s.ConfigureImportLimits(limits); e != nil {
		t.Fatal(e)
	}
	request := batch("one", "", "a")
	p := importPlan(t, s, request)
	importPlan(t, s, request)
	_, e := s.Call(writer, "import_prepare", Args{Import: &request, IdempotencyKey: "quota"})
	code(t, e, "quota_exceeded")
	if rowCount(t, s, "import_plans") != 2 || rowCount(t, s, "import_prepare_keys") != 2 {
		t.Fatal("quota allocated")
	}
	// Existing keyed preparation replay is not a new allocation at the quota.
	var key string
	if e = s.DB.QueryRowContext(context.Background(), `SELECT key FROM import_prepare_keys WHERE plan_id=?`, p.ID).Scan(&key); e != nil {
		t.Fatal(e)
	}
	v, e := s.Call(writer, "import_prepare", Args{Import: &request, IdempotencyKey: key})
	if e != nil || v.(ImportPlan).ID != p.ID {
		t.Fatal(v, e)
	}
	limits.MaxBytes = 128
	if e = s.ConfigureImportLimits(limits); e != nil {
		t.Fatal(e)
	}
	v, e = s.Call(writer, "import_prepare", Args{Import: &request, IdempotencyKey: key})
	if e != nil || v.(ImportPlan).ID != p.ID {
		t.Fatal("configured cap must not rewrite prior replay", v, e)
	}
	_, e = s.Call(writer, "import_prepare", Args{Import: &request, IdempotencyKey: "bytes"})
	code(t, e, "quota_exceeded")
	if rowCount(t, s, "import_plans") != 2 {
		t.Fatal("byte cap allocated")
	}
}
func TestImportFailureCountsAndPostResultSourceCheckpoint(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	bad := batch("one", "", "a", "b", "c")
	bad.Entries[1].Record = nil
	plan := importPlan(t, s, bad)
	_, e := s.Call(writer, "import_commit", commitArgs(plan, "reject"))
	code(t, e, "validation_error")
	var wire *Error
	if !errors.As(e, &wire) {
		t.Fatal(e)
	}
	report := wire.Details.(ImportFailure)
	if report.Counts.Created != 0 || report.Counts.Updated != 0 || report.Counts.Unchanged != 0 || report.Counts.Rejected != 1 || report.Counts.NotCommitted != 2 {
		t.Fatal(report)
	}
	if rowCount(t, s, "records") != 0 || rowCount(t, s, "import_sources") != 0 || rowCount(t, s, "import_checkpoints") != 0 {
		t.Fatal("failed counts hid mutation")
	}
	request := batch("one", "", "a")
	plan = importPlan(t, s, request)
	v, e := s.Call(writer, "import_commit", commitArgs(plan, "first"))
	if e != nil {
		t.Fatal(e)
	}
	receipt := v.(ImportResult)
	state, found, e := sourceState(context.Background(), s.DB, "registry", "fixture", "a")
	if e != nil || !found {
		t.Fatal(state, e)
	}
	if state.RegistryRevision != plan.Items[0].ProposedRevision || state.EdgeSetRevision != state.RegistryRevision || state.ReceiptID != receipt.ReceiptID || state.MappingVersion != "fixture/1" {
		t.Fatal("checkpoint retained old base", state)
	}
	request.ExpectedCheckpoint = "one"
	next := importPlan(t, s, request)
	if next.Counts.Unchanged != 1 {
		t.Fatal(next)
	}
	request.MappingVersion = "fixture/2"
	next = importPlan(t, s, request)
	if next.Counts.Updated != 1 || next.Items[0].ProposedRevision == state.RegistryRevision {
		t.Fatal("mapping change not a new candidate", next)
	}
	if _, e = s.Call(writer, "import_commit", commitArgs(next, "mapping")); e != nil {
		t.Fatal(e)
	}
	request.MappingVersion = "fixture/2"
	request.Checkpoint = "two"
	request.Entries[0].SourceRevision = "two"
	request.Entries[0].Record.Summary = "source changed"
	unordered := importPlan(t, s, request)
	if unordered.Items[0].Reason != "source_order_unverified" {
		t.Fatal(unordered)
	}
	request.Entries[0].ExpectedSourceRevision = "one"
	ordered := importPlan(t, s, request)
	if ordered.Counts.Updated != 1 {
		t.Fatal(ordered)
	}
}
func cyclicBatch(relation string) ImportRequest {
	request := batch("cycle", "", "a", "b")
	for i, id := range []string{"a", "b"} {
		target := "a"
		if id == "a" {
			target = "b"
		}
		var payload map[string]any
		_ = json.Unmarshal(request.Entries[i].Record.Payload, &payload)
		payload["edges"] = []map[string]string{{"type": relation, "to": "project:" + target}}
		request.Entries[i].Record.Payload, _ = json.Marshal(payload)
	}
	return request
}
func TestImportCyclePolicyAndAtomicPublication(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	p := importPlan(t, s, cyclicBatch("consumes"))
	if p.Counts.Rejected != 0 {
		t.Fatal(p)
	}
	if _, e := s.Call(writer, "import_commit", commitArgs(p, "cycle")); e != nil {
		t.Fatal(e)
	}
	cs := []Candidate{}
	for _, item := range p.Items {
		cs = append(cs, Candidate{ID: item.ID, Revision: item.ProposedRevision})
	}
	ap := approvePublication(t, s, preparePublication(t, s, cs...))
	published := publish(t, s, ap, "publish-cycle")
	if len(published.Records) != 2 {
		t.Fatal(published)
	}
	other := openTest(t, filepath.Join(t.TempDir(), "db"))
	forbidden := importPlan(t, other, cyclicBatch("part_of"))
	if forbidden.Counts.Rejected == 0 {
		t.Fatal("forbidden cycle accepted")
	}
	_, e := other.Call(writer, "import_commit", commitArgs(forbidden, "forbidden"))
	code(t, e, "validation_error")
	if rowCount(t, other, "records") != 0 {
		t.Fatal("forbidden cycle committed")
	}
}
func TestExportReadOnlyPlanAndSnapshotAuthorizationBoundary(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	if e := s.ConfigureExportTargets(map[string]string{"fixture": filepath.Join(t.TempDir(), "out")}); e != nil {
		t.Fatal(e)
	}
	a := create(t, s, input("a"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "publish")
	before := rowCount(t, s, "export_receipts")
	v, e := s.Call(writer, "export_plan", Args{Export: &ExportRequest{Target: "fixture"}})
	if e != nil {
		t.Fatal(e)
	}
	b := v.(ExportPlan).Bundle
	if rowCount(t, s, "export_receipts") != before || rowCount(t, s, "export_attempts") != 0 {
		t.Fatal("plan wrote authorization or outcome")
	}
	args := Args{Bundle: &b, Hash: b.Digest, IdempotencyKey: "authorize"}
	v, e = s.Call(writer, "export_authorize", args)
	if e != nil {
		t.Fatal(e)
	}
	receipt := v.(ExportReceipt)
	callRecord(t, s, writer, "record_archive", Args{ID: a.ID, Version: a.VersionToken})
	v, e = s.Call(writer, "export_authorize", args)
	if e != nil || v.(ExportReceipt).ID != receipt.ID {
		t.Fatal("authorization replay changed historical receipt", v, e)
	}
	fresh := args
	fresh.IdempotencyKey = "new-attempt"
	_, e = s.Call(writer, "export_authorize", fresh)
	if e == nil {
		t.Fatal("archive must block new authorization")
	}
	v, e = s.Call(writer, "export_apply", exportArgs(receipt))
	if e != nil || v.(ExportOutcome).State != "completed" {
		t.Fatal("archive retracted already-issued snapshot", v, e)
	}
	_, e = s.Call(writer, "export_apply", exportArgs(receipt))
	code(t, e, "version_conflict")
	v, e = s.Call(writer, "export_attempt_get", Args{ReceiptID: receipt.ID})
	if e != nil || v.(ExportOutcome).State != "completed" {
		t.Fatal(v, e)
	}
}
func TestExportDeadlineBoundsStartAndNoDatabaseLockDuringFilesystemWork(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	if e := s.ConfigureExportTargets(map[string]string{"fixture": filepath.Join(t.TempDir(), "out")}); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	a := create(t, s, input("a"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "publish")
	expired := exportPlan(t, s, ExportRequest{Target: "fixture"})
	now = now.Add(16 * time.Minute)
	_, e := s.Call(writer, "export_apply", exportArgs(expired))
	code(t, e, "version_conflict")
	if rowCount(t, s, "export_attempts") != 0 {
		t.Fatal("expired receipt started")
	}
	receipt := exportPlan(t, s, ExportRequest{Target: "fixture"})
	called := false
	s.materializerOptions = materialize.EngineOptions{BeforeWrite: func(_ string, _ artifact.Entry) error {
		if called {
			return nil
		}
		called = true
		// Crossing the deadline after start does not withdraw a running attempt.
		now = now.Add(time.Hour)
		ctx, cancel := context.WithTimeout(writer, 2*time.Second)
		defer cancel()
		_, err := s.Call(ctx, "record_archive", Args{ID: a.ID, Version: a.VersionToken})
		return err
	}}
	v, e := s.Call(writer, "export_apply", exportArgs(receipt))
	if e != nil || !called || v.(ExportOutcome).State != "completed" {
		t.Fatal("filesystem held Registry DB lock or deadline became a fence", v, e)
	}
}
func TestExportFailedAttemptIsRecordedAndCannotRestart(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "out")
	if e := s.ConfigureExportTargets(map[string]string{"fixture": root}); e != nil {
		t.Fatal(e)
	}
	a := create(t, s, input("a"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "publish")
	receipt := exportPlan(t, s, ExportRequest{Target: "fixture"})
	s.materializerOptions = materialize.EngineOptions{BeforeRename: func(_, _ string) error { return errors.New("fixture publication failure") }}
	v, e := s.Call(writer, "export_apply", exportArgs(receipt))
	code(t, e, "unavailable")
	if v.(ExportOutcome).State != "failed" {
		t.Fatal("failure outcome absent", v)
	}
	v, e = s.Call(writer, "export_attempt_get", Args{ReceiptID: receipt.ID})
	if e != nil || v.(ExportOutcome).State != "failed" || v.(ExportOutcome).Failure == nil {
		t.Fatal("failure not persisted", v, e)
	}
	_, e = s.Call(writer, "export_apply", exportArgs(receipt))
	code(t, e, "version_conflict")
	s.materializerOptions = materialize.EngineOptions{}
	fresh := exportPlan(t, s, ExportRequest{Target: "fixture"})
	if _, e = s.Call(writer, "export_apply", exportArgs(fresh)); e != nil {
		t.Fatal("fresh recovery authorization could not run", e)
	}
}
func TestExportPinnedScopeDoesNotRepinOrInvalidateOnUnrelatedPublication(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	if e := s.ConfigureExportTargets(map[string]string{"fixture": filepath.Join(t.TempDir(), "out")}); e != nil {
		t.Fatal(e)
	}
	target := create(t, s, input("target"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: target.ID, Revision: target.RevisionID})), "target")
	srcInput := input("source")
	var payload map[string]any
	_ = json.Unmarshal(srcInput.Payload, &payload)
	payload["edges"] = []map[string]string{{"type": "consumes", "to": target.ID}}
	srcInput.Payload, _ = json.Marshal(payload)
	source := create(t, s, srcInput)
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: source.ID, Revision: source.RevisionID})), "source")
	first := exportPlan(t, s, ExportRequest{Target: "fixture", IDs: []string{source.ID}})
	next := input("target")
	next.Summary = "New unrelated-to-pin target revision"
	target2 := callRecord(t, s, writer, "record_update", Args{ID: target.ID, Version: target.VersionToken, Record: next})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: target.ID, Revision: target2.RevisionID, ExpectedPublishedRevision: target.RevisionID})), "target2")
	second := exportPlan(t, s, ExportRequest{Target: "fixture", IDs: []string{source.ID}})
	if first.Bundle.Digest != second.Bundle.Digest {
		t.Fatal("published target update repinned old dependency")
	}
	found := false
	for _, subject := range second.Bundle.Subjects {
		if subject.Record.ID == target.ID {
			found = true
			if subject.Record.Revision != target.RevisionID {
				t.Fatal("latest substituted for pin", subject)
			}
		}
	}
	if !found {
		t.Fatal("missing pinned dependency subject")
	}
	callRecord(t, s, writer, "record_archive", Args{ID: target.ID, Version: target2.VersionToken})
	_, e := s.Call(writer, "export_authorize", Args{Bundle: &second.Bundle, Hash: second.Bundle.Digest, IdempotencyKey: newID()})
	code(t, e, "reference_unavailable")
	if _, e = s.Call(writer, "export_apply", exportArgs(second)); e != nil {
		t.Fatal("later archive retracted snapshot", e)
	}
}
func TestRejectedPayloadOmittedAndCommitTargetRevalidated(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	invalid := batch("one", "", "a")
	// This sensitive setting name is rejected by typed admission even though the
	// harmless example value is not a token. Raw rejected content is not retained.
	invalid.Entries[0].Record.Metadata = map[string]any{sensitiveSettingName: "fixture-rejected-value"}
	p := importPlan(t, s, invalid)
	if p.Request.Entries[0].Record != nil {
		t.Fatal("rejected content retained")
	}
	var raw string
	if e := s.DB.QueryRowContext(context.Background(), `SELECT body FROM import_plans WHERE id=?`, p.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(raw, "fixture-rejected-value") {
		t.Fatal("unredacted rejection persisted")
	}
	target := create(t, s, input("target"))
	request := batch("two", "", "child")
	var payload map[string]any
	_ = json.Unmarshal(request.Entries[0].Record.Payload, &payload)
	payload["edges"] = []map[string]string{{"type": "consumes", "to": target.ID}}
	request.Entries[0].Record.Payload, _ = json.Marshal(payload)
	staged := importPlan(t, s, request)
	if staged.Counts.Rejected != 0 {
		t.Fatal(staged)
	}
	callRecord(t, s, writer, "record_archive", Args{ID: target.ID, Version: target.VersionToken})
	_, e := s.Call(writer, "import_commit", commitArgs(staged, "stale-target"))
	code(t, e, "reference_unavailable")
	if rowCount(t, s, "records") != 1 || rowCount(t, s, "import_checkpoints") != 0 || rowCount(t, s, "import_plans") != 2 {
		t.Fatal("target failure lost stage or inserted child")
	}
}
func TestConnectorExpiredPreparationNeedsExplicitNewKey(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	c := &fixtureConnector{request: batch("one", "", "a")}
	request, e := connectorSnapshot(writer, c, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Call(writer, "import_prepare", Args{Import: &request, IdempotencyKey: "expired-run"}); e != nil {
		t.Fatal(e)
	}
	now = now.Add(25 * time.Hour)
	_, e = RunConnector(writer, s.Call, c, "expired-run")
	code(t, e, "version_conflict")
	if rowCount(t, s, "import_plans") != 1 {
		t.Fatal("connector silently reallocated expired preparation")
	}
	result, e := RunConnectorWithPreparationKey(writer, s.Call, c, "explicit-new-preparation")
	if e != nil || result.Counts.Created != 1 {
		t.Fatal(result, e)
	}
	if rowCount(t, s, "import_plans") != 2 {
		t.Fatal("explicit new key did not create independent preparation")
	}
}
func TestConnectorNewRerunDoesNotHideLocalDriftBehindOldReplay(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	c := &fixtureConnector{request: batch("one", "", "a")}
	for i := 0; i < 3; i++ {
		result, e := RunConnector(writer, s.Call, c, newID())
		if e != nil {
			t.Fatal(e)
		}
		if i > 0 && result.Counts.Unchanged != 1 {
			t.Fatal(result)
		}
	}
	r := callRecord(t, s, writer, "record_get", Args{ID: "project:a", Publication: "draft"})
	changed := input("a")
	changed.Summary = "later local edit"
	callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: changed})
	_, e := RunConnector(writer, s.Call, c, newID())
	code(t, e, "validation_error")
	var wire *Error
	if !errors.As(e, &wire) || wire.Details.(ImportFailure).Counts.Rejected != 1 {
		t.Fatal("local drift returned historical success", e)
	}
}

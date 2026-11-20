package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/tools/registry/contracts"
)

func TestFTSNamesSummariesBodiesAndImmutablePublicationBoundary(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	for _, id := range []string{"a", "b", "c"} {
		in := input(id)
		switch id {
		case "a":
			in.Payload = json.RawMessage(strings.ReplaceAll(string(in.Payload), `"name":"a"`, `"name":"nebula"`))
		case "b":
			in.Summary = "Nebula in a summary"
		case "c":
			in = &Input{Schema: "document/1", Key: id, Payload: json.RawMessage(`{"schema":"document/1","id":"c","name":"Guide","subtype":"sop","format":"markdown","body":"# Guide\n\nNebula lives in the body."}`)}
		}
		r := create(t, s, in)
		publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})), id)
	}
	q := Query{Search: "NEBULA", Limit: 1}
	v, err := s.Call(writer, "record_search", Args{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	first := v.(Page)
	if !first.HasMore || len(first.Records) != 1 {
		t.Fatal(first)
	}
	// Update and republish after the boundary: old FTS rows remain searchable.
	r := callRecord(t, s, writer, "record_get", Args{ID: "project:b", Publication: "draft"})
	next := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("b")})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: next.RevisionID, ExpectedPublishedRevision: r.RevisionID})), "b2")
	q.Cursor = first.Cursor
	v, err = s.Call(writer, "record_search", Args{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	second := v.(Page)
	q.Cursor = second.Cursor
	v, err = s.Call(writer, "record_search", Args{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	third := v.(Page)
	if second.Records[0].ID != "project:a" || third.Records[0].RevisionID != r.RevisionID || third.HasMore {
		t.Fatalf("FTS boundary lost: %+v %+v", second, third)
	}
	v, err = s.Call(writer, "record_search", Args{Query: Query{Search: "nebula"}})
	if err != nil || len(v.(Page).Records) != 2 {
		t.Fatalf("stale FTS result: %v %v", v, err)
	}
	for _, text := range []string{`"`, "OR", `a\" OR *`, "   ", "NEBULA"} {
		if _, err = s.Call(writer, "record_search", Args{Query: Query{Search: text, Publication: "draft"}}); err != nil {
			t.Fatalf("literal search %q: %v", text, err)
		}
	}
	var plan strings.Builder
	rows, err := s.DB.QueryContext(context.Background(), `EXPLAIN QUERY PLAN SELECT rowid FROM revision_fts WHERE revision_fts MATCH ?`, `"nebula"`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "VIRTUAL TABLE INDEX") {
		t.Fatal(plan.String())
	}
}

func TestRetentionPreservesPublishedHistoryApprovalsReceiptsAndCursors(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	s.reviewers["owner"] = "owner"
	admin := caller("owner", "registry.review")
	cfg := MaintenanceConfig{CursorTTL: 24 * time.Hour, ReceiptTTL: 24 * time.Hour, DraftHistoryTTL: 24 * time.Hour}
	s.Maintenance = cfg
	// Age eligibility using a future cutoff through old timestamps, without
	// mutating publication history or replacing production time sources.
	r := create(t, s, input("published"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})), "published")
	current := r
	for i := 0; i < 3; i++ {
		current = callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: current.VersionToken, Record: input("published")})
	}
	d := create(t, s, input("draft"))
	d2 := callRecord(t, s, writer, "record_update", Args{ID: d.ID, Version: d.VersionToken, Record: input("draft")})
	a := create(t, s, input("approval"))
	preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})
	callRecord(t, s, writer, "record_update", Args{ID: a.ID, Version: a.VersionToken, Record: input("approval")})
	receipt := callRecord(t, s, writer, "record_create", Args{Record: input("receipt"), IdempotencyKey: "retained"})
	callRecord(t, s, writer, "record_update", Args{ID: receipt.ID, Version: receipt.VersionToken, Record: input("receipt")})
	// Historical immutable payload timestamps are changed solely in this fixture.
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE revisions SET body=json_set(body,'$.updated_at','2000-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	page, err := s.Call(writer, "record_list", Args{Query: Query{Publication: "draft", Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if page.(Page).Cursor == "" {
		t.Fatal("cursor missing")
	}
	v, err := s.Call(admin, "retention_run", Args{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !v.(RetentionResult).HistoryDeferred {
		t.Fatal(v)
	}
	// Expire just the cursor; live receipts still pin their original response.
	if _, err = s.DB.ExecContext(context.Background(), `UPDATE cursors SET created_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	preview, err := s.Call(admin, "retention_run", Args{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.(RetentionResult).DraftRevisions != 1 || preview.(RetentionResult).Cursors != 1 {
		t.Fatal(preview)
	}
	if _, err = s.Call(writer, "revision_get", Args{ID: d.ID, Revision: d.RevisionID}); err != nil {
		t.Fatal("preview deleted revision", err)
	}
	v, err = s.Call(admin, "retention_run", Args{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.(RetentionResult).DraftRevisions != 1 {
		t.Fatal(v)
	}
	_, err = s.Call(writer, "revision_get", Args{ID: d.ID, Revision: d.RevisionID})
	code(t, err, "not_found")
	_, err = s.Call(writer, "record_list", Args{Query: Query{Publication: "draft", Limit: 1, Cursor: page.(Page).Cursor}})
	code(t, err, "validation_error")
	for _, keep := range []Record{r, current, d2, a, receipt} {
		if _, err = s.Call(writer, "revision_get", Args{ID: keep.ID, Revision: keep.RevisionID}); err != nil {
			t.Fatalf("protected history %s lost: %v", keep.ID, err)
		}
	}
	// All revisions for a published identity survive, including never-published edits.
	var count int
	if err = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM revisions WHERE id=?`, r.ID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("published history: %d %v", count, err)
	}
	_, err = s.Call(reviewer, "retention_run", Args{Apply: true})
	code(t, err, "forbidden")
}

func TestConsistentBackupAndIsolatedRestore(t *testing.T) {
	root := t.TempDir()
	s := openTest(t, filepath.Join(root, "live.db"))
	s.reviewers["owner"] = "owner"
	s.Maintenance.BackupDirectory = filepath.Join(root, "backups")
	original := create(t, s, input("before"))
	target := create(t, s, input("dependency"))
	initialApproval := approvePublication(t, s, preparePublication(t, s, Candidate{ID: original.ID, Revision: original.RevisionID}, Candidate{ID: target.ID, Revision: target.RevisionID}))
	publish(t, s, initialApproval, "backup-initial-publication")
	relation := kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes,
		From: kinds.Reference{Authority: "registry", Kind: "project", ID: original.ID},
		To:   kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}
	proposed := callRecord(t, s, writer, "relation_create", Args{ID: original.ID, Version: original.VersionToken, Relation: &relation})
	edge := proposed.Edges[0]
	r := callRecord(t, s, ground, "relation_decide", Args{ID: proposed.ID, Version: proposed.VersionToken, EdgeID: edge.ID, Hash: edge.Hash, Decision: "reviewed", Reason: "Verified backup graph fixture"})
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID, ExpectedPublishedRevision: original.RevisionID}))
	publish(t, s, ap, "backup-replay")
	unpublished := input("before")
	unpublished.Summary = "Unpublished restore fixture edit"
	draft := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: unpublished})
	wal, err := os.Stat(filepath.Join(root, "live.db-wal"))
	if err != nil || wal.Size() == 0 {
		t.Fatalf("fixture must exercise committed WAL: %v %v", wal, err)
	}
	v, err := s.Call(caller("owner", "registry.review"), "backup_create", Args{})
	if err != nil {
		t.Fatal(err)
	}
	result := v.(store.BackupResult)
	if !result.IntegrityOK {
		t.Fatal(result)
	}
	if _, err = os.Stat(result.Path + ".manifest.json"); err != nil {
		t.Fatal(err)
	}
	create(t, s, input("after"))
	// Restore to a distinct DB, then open it through the same migration/service
	// path. The live service continues and the backup itself is not opened writable.
	restoredPath := filepath.Join(root, "isolated", "restored.db")
	if err = os.Mkdir(filepath.Dir(restoredPath), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(restoredPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	verified, err := store.VerifyBackup(context.Background(), restoredPath)
	if err != nil || verified.SHA256 != result.SHA256 {
		t.Fatalf("restore integrity: %+v %v", verified, err)
	}
	restored := openTest(t, restoredPath)
	got := callRecord(t, restored, writer, "record_get", Args{ID: r.ID})
	if got.RevisionID != r.RevisionID || got.Publication != "published" || len(got.Edges) != 1 || got.Edges[0].TargetState != "resolved" || got.TargetPins[r.Edges[0].ID] != target.RevisionID {
		t.Fatalf("restored published graph/pins: %+v", got)
	}
	gotEdge := got.Edges[0]
	wantEdge := r.Edges[0]
	if gotEdge.Relation.Status != "reviewed" || gotEdge.ReviewedBy != wantEdge.ReviewedBy || gotEdge.ReviewedAt != wantEdge.ReviewedAt || gotEdge.ReviewHash != edge.Hash || gotEdge.Reason != wantEdge.Reason {
		t.Fatalf("restored graph review evidence differs: %+v want %+v", gotEdge, wantEdge)
	}
	restoredDraft := callRecord(t, restored, writer, "record_get", Args{ID: r.ID, Publication: "draft"})
	if restoredDraft.RevisionID != draft.RevisionID || restoredDraft.Summary != unpublished.Summary || restoredDraft.Publication != "draft" {
		t.Fatalf("draft/published distinction lost: %+v", restoredDraft)
	}
	historic, err := restored.Call(writer, "revision_get", Args{ID: original.ID, Revision: original.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	var historicRecord Record
	if err = json.Unmarshal(historic.(json.RawMessage), &historicRecord); err != nil || historicRecord.RevisionID != original.RevisionID || len(historicRecord.Edges) != 0 {
		t.Fatalf("original immutable history: %+v %v", historicRecord, err)
	}
	history, err := restored.Call(writer, "revision_list", Args{ID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	revisions := history.(map[string]any)["revisions"].([]Record)
	wantIDs := []string{draft.RevisionID, r.RevisionID, proposed.RevisionID, original.RevisionID}
	if len(revisions) != len(wantIDs) {
		t.Fatalf("restored history: %v", history)
	}
	for i, want := range wantIDs {
		if revisions[i].RevisionID != want {
			t.Fatalf("restored revision %d: %s want %s", i, revisions[i].RevisionID, want)
		}
	}
	approval, err := restored.Call(writer, "publication_approval_get", Args{ApprovalID: ap.ID})
	if err != nil {
		t.Fatal(err)
	}
	restoredApproval := approval.(Approval)
	if restoredApproval.Revision != ap.Revision || restoredApproval.ManifestDigest != ap.ManifestDigest || restoredApproval.Principal != ap.Principal || restoredApproval.Decision != "approved" {
		t.Fatalf("approval review evidence differs: %+v", restoredApproval)
	}
	// Both publication events must retain exact immutable revision identities.
	rows, err := restored.DB.QueryContext(context.Background(), `SELECT revision FROM publication_events WHERE id=? ORDER BY seq`, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var publishedIDs []string
	for rows.Next() {
		var revision string
		if err = rows.Scan(&revision); err != nil {
			t.Fatal(err)
		}
		publishedIDs = append(publishedIDs, revision)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if len(publishedIDs) != 2 || publishedIDs[0] != original.RevisionID || publishedIDs[1] != r.RevisionID {
		t.Fatalf("restored publication history: %v", publishedIDs)
	}
	_, err = restored.Call(writer, "record_get", Args{ID: "project:after", Publication: "draft"})
	code(t, err, "not_found")
	replay := publish(t, restored, ap, "backup-replay")
	if !replay.Replayed || replay.ApprovalID != ap.ID || replay.ApprovalRevision != ap.Revision {
		t.Fatal("exact publication receipt binding not restored")
	}
	found, err := restored.Call(writer, "record_search", Args{Query: Query{Search: "before"}})
	if err != nil || len(found.(Page).Records) != 1 {
		t.Fatalf("restored FTS: %v %v", found, err)
	}
	for _, table := range []string{"revisions", "publication_events", "publication_policies", "publication_decisions"} {
		var count int
		if err = restored.DB.QueryRowContext(context.Background(), fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err != nil || count == 0 {
			t.Fatalf("restore %s: %d %v", table, count, err)
		}
	}
	_, err = s.Call(writer, "backup_create", Args{})
	code(t, err, "forbidden")
}

func TestRelationshipReviewWeightsReferencesAndPublication(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	source := create(t, s, input("source"))
	target := create(t, s, input("target"))
	weight := 0.75
	rel := kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: source.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Weight: &weight, Source: "mined", Status: "proposed"}
	source = callRecord(t, s, writer, "relation_create", Args{ID: source.ID, Version: source.VersionToken, Relation: &rel})
	e := source.Edges[0]
	args := Args{ID: source.ID, Version: source.VersionToken, EdgeID: e.ID, Hash: e.Hash, Decision: "reviewed", Reason: "Checked relationship and authored weight"}
	_, err := s.Call(writer, "relation_decide", args)
	code(t, err, "forbidden")
	s.reviewers["writer"] = "ground"
	_, err = s.Call(caller("writer", "registry.review"), "relation_decide", args)
	code(t, err, "forbidden")
	reviewed := callRecord(t, s, ground, "relation_decide", args)
	if reviewed.Edges[0].Relation.Status != "reviewed" || reviewed.Edges[0].ReviewedBy != "ground" || *reviewed.Edges[0].Relation.Weight != weight {
		t.Fatal(reviewed.Edges)
	}
	_, err = s.Call(ground, "relation_decide", args)
	code(t, err, "version_conflict")
	// Review does not publish. The published graph remains separately approved.
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: target.ID, Revision: target.RevisionID}, Candidate{ID: source.ID, Revision: reviewed.RevisionID})), "graph")
	got := callRecord(t, s, writer, "record_get", Args{ID: source.ID})
	if got.Edges[0].ReviewedBy != "ground" {
		t.Fatal(got)
	}
	// Missing, archived and external targets are visible in a paged report.
	missing := rel
	missing.To.ID = "project:missing"
	missing.Source = "authored"
	missing.Weight = nil
	next := callRecord(t, s, writer, "relation_create", Args{ID: source.ID, Version: reviewed.VersionToken, Relation: &missing, AllowUnresolved: true})
	external := rel
	doc := create(t, s, &Input{Schema: "document/1", Key: "external", Payload: json.RawMessage(`{"schema":"document/1","id":"external","name":"Guide","subtype":"sop","format":"markdown","body":"Reference"}`)})
	external.From = kinds.Reference{Authority: "registry", Kind: "document", ID: doc.ID}
	external.To.Authority = "external"
	external.To.ID = "external:one"
	callRecord(t, s, writer, "relation_create", Args{ID: doc.ID, Version: doc.VersionToken, Relation: &external})
	ex, err := s.Call(writer, "references_report", Args{Query: Query{Publication: "draft", Kind: "document"}})
	if err != nil || len(ex.(ReferenceReport).Issues) != 1 || ex.(ReferenceReport).Issues[0].Edge.TargetState != "unchecked" {
		t.Fatalf("external report: %v %v", ex, err)
	}
	report, err := s.Call(writer, "references_report", Args{Query: Query{Publication: "draft", Kind: "project", Full: true, Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	rr := report.(ReferenceReport)
	if len(rr.Issues) != 1 || !rr.HasMore || rr.Issues[0].Edge.TargetState != "unresolved" {
		t.Fatal(rr)
	}
	// Invalid shared weight and spoofed review provenance are rejected.
	bad := rel
	bad.Weight = new(float64)
	*bad.Weight = 1.1
	_, err = s.Call(writer, "relation_create", Args{ID: source.ID, Version: reviewed.VersionToken, Relation: &bad})
	code(t, err, "version_conflict")
	current := next
	_, err = s.Call(writer, "relation_create", Args{ID: source.ID, Version: current.VersionToken, Relation: &bad})
	code(t, err, "validation_error")
	bad = rel
	bad.Status = "reviewed"
	bad.Source = "reviewed"
	_, err = s.Call(writer, "relation_create", Args{ID: source.ID, Version: current.VersionToken, Relation: &bad})
	code(t, err, "forbidden")
}

func TestToolExamplesPersistBindApprovalAndRejectSecrets(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	in := &Input{Schema: "tool/1", Key: "example", Payload: json.RawMessage(`{"schema":"tool/1","id":"tool:mcp/example/read","source":"mcp","origin":"example","lifecycle":"active","definition":{"name":"read","input_schema":{"type":"object"}}}`), Examples: []ToolExample{{Name: "Lookup", Arguments: json.RawMessage(`{"id":"document:guide"}`), Result: json.RawMessage(`{"found":true}`)}}}
	r := create(t, s, in)
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID, Publication: "draft"})
	if len(got.Examples) != 1 {
		t.Fatal(got)
	}
	sub, err := subjectFor(context.Background(), s.DB, r, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	in.Examples[0].Description = "More details"
	next := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: in})
	after, err := subjectFor(context.Background(), s.DB, next, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Record.ContentDigest == after.Record.ContentDigest || r.Review.DefinitionHash != next.Review.DefinitionHash {
		t.Fatal("examples must bind publication but leave tool definition classification alone")
	}
	secretFixture, _ := json.Marshal(map[string]string{sensitiveSettingName: "do-not-store"})
	in.Examples[0].Arguments = secretFixture
	_, err = s.Call(writer, "record_update", Args{ID: r.ID, Version: next.VersionToken, Record: in})
	code(t, err, "validation_error")
	in.Examples[0].Arguments = json.RawMessage(`[]`)
	_, err = s.Call(writer, "record_update", Args{ID: r.ID, Version: next.VersionToken, Record: in})
	code(t, err, "validation_error")
}

func TestExplicitKindMigrationIsAtomicAndLogged(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("legacy"))
	// Model a retained old kind version; migration targets must exist in the
	// pinned agency-owned Registry contracts. Project/1 -> project/2 is tested
	// separately with generated backup fixtures and no historical row edits.
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE revisions SET body=json_set(body,'$.schema','project/0') WHERE token=?`, r.RevisionID); err != nil {
		t.Fatal(err)
	}
	_, err := s.Call(writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("legacy")})
	code(t, err, "validation_error")
	migrated := callRecord(t, s, writer, "record_migrate", Args{ID: r.ID, Version: r.VersionToken, Record: input("legacy"), Reason: "Explicit re-admission of legacy version"})
	if migrated.Schema != "project/1" || migrated.Publication != "draft" {
		t.Fatal(migrated)
	}
	v, err := s.Call(writer, "migration_list", Args{ID: r.ID})
	if err != nil {
		t.Fatal(err)
	}
	logs := v.(map[string]any)["migrations"].([]KindMigration)
	if len(logs) != 1 || logs[0].FromSchema != "project/0" || logs[0].FromRevision != r.RevisionID || logs[0].ToRevision != migrated.RevisionID || logs[0].Actor != "writer" {
		t.Fatal(logs)
	}
	unknown := input("legacy")
	unknown.Schema = "project/999"
	_, err = s.Call(writer, "record_migrate", Args{ID: r.ID, Version: migrated.VersionToken, Record: unknown, Reason: "unsupported"})
	code(t, err, "unknown_schema")
	_, err = s.Call(writer, "record_migrate", Args{ID: r.ID, Version: migrated.VersionToken, Record: input("legacy"), Reason: "same version"})
	code(t, err, "validation_error")
}

func TestAdditiveMigrationBackfillsExistingSearchAndRetentionClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	cfg := store.Config{Driver: store.DriverSQLite, SchemaMode: store.SchemaAuto, SQLite: store.SQLiteConfig{Path: path}}
	db, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual prior SQL files, then upgrade that real database.
	old := fstest.MapFS{}
	for _, name := range []string{"001_registry.sql", "002_publication.sql"} {
		b, err := Migrations.ReadFile("migrations/sqlite/" + name)
		if err != nil {
			t.Fatal(err)
		}
		old["migrations/sqlite/"+name] = &fstest.MapFile{Data: b}
	}
	if err = migrate.Ensure(context.Background(), cfg, db, migrate.Source{FS: old, Root: "migrations"}); err != nil {
		t.Fatal(err)
	}
	s := New(db, map[string]string{"writer": "registry"}, map[string]string{"ground": "ground"})
	r := callRecord(t, s, writer, "record_create", Args{Record: input("backfilled"), IdempotencyKey: "old-receipt"})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})), "old-publication")
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded := openTest(t, path)
	for _, publication := range []string{"draft", "published"} {
		v, err := upgraded.Call(writer, "record_search", Args{Query: Query{Search: "backfilled", Publication: publication}})
		if err != nil || len(v.(Page).Records) != 1 {
			t.Fatalf("backfill %s: %v %v", publication, v, err)
		}
	}
	var at string
	if err = upgraded.DB.QueryRowContext(context.Background(), `SELECT created_at FROM receipts WHERE key='old-receipt'`).Scan(&at); err != nil || at == "" {
		t.Fatalf("legacy receipt clock: %q %v", at, err)
	}
	if _, err = upgraded.Call(writer, "revision_get", Args{ID: r.ID, Revision: r.RevisionID}); err != nil {
		t.Fatal(err)
	}
}

func TestTypedDependencyReviewRemainsPublishable(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	doc := create(t, s, &Input{Schema: "document/1", Key: "guide", Payload: json.RawMessage(`{"schema":"document/1","id":"guide","name":"Guide","subtype":"sop","format":"markdown","body":"Instructions"}`)})
	skill := create(t, s, &Input{Schema: "skill/1", Key: "reader", Payload: json.RawMessage(`{"schema":"skill/1","id":"reader","name":"Reader","instructions":"Read the guide","documents":[{"authority":"registry","kind":"document","id":"document:guide"}]}`)})
	if len(skill.Edges) != 1 || !isPayloadEdge(skill.Edges[0]) {
		t.Fatal(skill)
	}
	edge := skill.Edges[0]
	reviewed := callRecord(t, s, ground, "relation_decide", Args{ID: skill.ID, Version: skill.VersionToken, EdgeID: edge.ID, Hash: edge.Hash, Decision: "reviewed", Reason: "Verified the guide reference"})
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: doc.ID, Revision: doc.RevisionID}, Candidate{ID: skill.ID, Revision: reviewed.RevisionID}))
	publish(t, s, ap, "typed-reviewed")
	got := callRecord(t, s, writer, "record_get", Args{ID: skill.ID})
	if got.Edges[0].Relation.Status != "reviewed" || got.TargetPins[edge.ID] != doc.RevisionID {
		t.Fatal(got)
	}
}

func TestRetentionKeepsNeverPublishedRelationshipReviewAfterRemoval(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	s.reviewers["owner"] = "owner"
	s.Maintenance.DraftHistoryTTL = time.Hour
	source := create(t, s, input("reviewed-source"))
	target := create(t, s, input("reviewed-target"))
	rel := kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes,
		From: kinds.Reference{Authority: "registry", Kind: "project", ID: source.ID},
		To:   kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}
	proposed := callRecord(t, s, writer, "relation_create", Args{ID: source.ID, Version: source.VersionToken, Relation: &rel})
	edge := proposed.Edges[0]
	reviewed := callRecord(t, s, ground, "relation_decide", Args{ID: source.ID, Version: proposed.VersionToken, EdgeID: edge.ID, Hash: edge.Hash, Decision: "reviewed", Reason: "Keep durable independent relationship evidence"})
	removed := callRecord(t, s, writer, "relation_remove", Args{ID: source.ID, Version: reviewed.VersionToken, EdgeID: edge.ID})
	// An unrelated draft-only identity still needs ordinary history expiration.
	unreviewed := create(t, s, input("ordinary-draft"))
	callRecord(t, s, writer, "record_update", Args{ID: unreviewed.ID, Version: unreviewed.VersionToken, Record: input("ordinary-draft")})
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE revisions SET body=json_set(body,'$.updated_at','2000-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	result, err := s.Call(caller("owner", "registry.review"), "retention_run", Args{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []Record{source, proposed, reviewed, removed} {
		if _, err = s.Call(writer, "revision_get", Args{ID: source.ID, Revision: want.RevisionID}); err != nil {
			t.Fatalf("relationship proposal/decision history %s lost after removal: %v", want.RevisionID, err)
		}
	}
	retained, err := s.Call(writer, "revision_get", Args{ID: source.ID, Revision: reviewed.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	var decision Record
	if err = json.Unmarshal(retained.(json.RawMessage), &decision); err != nil {
		t.Fatal(err)
	}
	if len(decision.Edges) != 1 || decision.Edges[0].ReviewedBy != "ground" || decision.Edges[0].ReviewHash != edge.Hash || decision.Edges[0].Reason != reviewed.Edges[0].Reason {
		t.Fatalf("relationship decision evidence changed: %+v", decision)
	}
	latest := callRecord(t, s, writer, "record_get", Args{ID: source.ID, Publication: "draft"})
	if latest.RevisionID != removed.RevisionID || len(latest.Edges) != 0 {
		t.Fatal("retention revived removed relationship", latest)
	}
	_, err = s.Call(writer, "revision_get", Args{ID: unreviewed.ID, Revision: unreviewed.RevisionID})
	code(t, err, "not_found")
	if result.(RetentionResult).DraftRevisions != 1 {
		t.Fatalf("only unrelated unreviewed history should expire: %+v", result)
	}
}

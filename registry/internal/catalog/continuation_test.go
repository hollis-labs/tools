package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/tools/registry/contracts"
)

func TestPhase3UpgradesAlreadyHardenedDatabase(t *testing.T) {
	ctx := context.Background()
	cfg := store.Config{Driver: store.DriverSQLite, SchemaMode: store.SchemaAuto, SQLite: store.SQLiteConfig{Path: filepath.Join(t.TempDir(), "db")}}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	prior := fstest.MapFS{}
	for _, name := range []string{"001_registry.sql", "002_publication.sql", "004_hardening.sql"} {
		path := "migrations/sqlite/" + name
		raw, e := Migrations.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		prior[path] = &fstest.MapFile{Data: raw}
	}
	if err = migrate.Ensure(ctx, cfg, db, migrate.Source{FS: prior, Root: "migrations"}); err != nil {
		t.Fatal(err)
	}
	s := New(db, map[string]string{"writer": "registry"}, nil)
	original := create(t, s, input("retained"))
	if err = migrate.Ensure(ctx, cfg, db, migrate.Source{FS: Migrations, Root: "migrations"}); err != nil {
		t.Fatal(err)
	}
	got := callRecord(t, s, writer, "record_get", Args{ID: original.ID, Publication: "draft"})
	if got.RevisionID != original.RevisionID {
		t.Fatal("upgrade replaced authored revision")
	}
	plan := importPlan(t, s, batch("one", "", "imported"))
	if _, err = s.Call(writer, "import_commit", commitArgs(plan, "commit")); err != nil {
		t.Fatal(err)
	}
}

func TestProject2ImportPublicationAndExportUseRegistryContracts(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	if err := s.ConfigureExportTargets(map[string]string{"fixture": filepath.Join(t.TempDir(), "out")}); err != nil {
		t.Fatal(err)
	}
	request := batch("one", "", "project2")
	in := request.Entries[0].Record
	in.Schema = "project/2"
	in.Payload = json.RawMessage(strings.Replace(string(in.Payload), `"schema":"project/1"`, `"schema":"project/2","type":"service","disposition":"continue"`, 1))
	p := importPlan(t, s, request)
	if p.Counts.Rejected != 0 || p.Admission.EnvelopeVersion != "registry.authored-envelope/2" {
		t.Fatal(p)
	}
	if _, err := s.Call(writer, "import_commit", commitArgs(p, "import")); err != nil {
		t.Fatal(err)
	}
	r := callRecord(t, s, writer, "record_get", Args{ID: "project:project2", Publication: "draft"})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})), "publish")
	v, err := s.Call(writer, "export_plan", Args{Export: &ExportRequest{Target: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	bundle := v.(ExportPlan).Bundle
	if len(bundle.Artifacts.Entries) != 2 || len(bundle.Sources) != 1 {
		t.Fatal(bundle)
	}
	// Inspect the actual artifact selected by its source manifest, rather than
	// rerendering separately or accepting YAML-looking substrings.
	source := bundle.Sources[0]
	if source.ID != r.ID || source.Revision != r.RevisionID || source.EdgeSetRevision != r.RevisionID {
		t.Fatal("project manifest lost its imported source revision")
	}
	var raw []byte
	for _, entry := range bundle.Artifacts.Entries {
		if entry.Path == source.Path {
			raw = entry.Bytes
		}
	}
	if len(raw) == 0 || source.Digest != "sha256:"+artifactDigest(raw).Hex {
		t.Fatal("project artifact missing or different from source manifest digest")
	}
	project, err := kinds.ParseProjectYAML(raw)
	if err != nil {
		t.Fatal("exported project YAML does not parse under Registry contracts", err)
	}
	if project.Schema != "project/2" || project.Type != "service" || project.Disposition != "continue" {
		t.Fatal("export changed the project version, type or disposition", project)
	}
	roundtrip, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if err = kinds.ValidateJSON("project/2", roundtrip); err != nil {
		t.Fatal("exported manifest roundtrip fails project/2 schema", err)
	}
	originalDigest, err := canonicalDigest(r.Payload)
	if err != nil {
		t.Fatal(err)
	}
	roundtripDigest, err := canonicalDigest(json.RawMessage(roundtrip))
	if err != nil || originalDigest != roundtripDigest {
		t.Fatal("YAML roundtrip changed the complete admitted project payload", err)
	}
	reparsed, err := kinds.ParseProjectJSON(roundtrip)
	if err != nil || !reflect.DeepEqual(project, reparsed) {
		t.Fatal("typed project does not survive JSON roundtrip", err)
	}
}

func TestRetentionKeepsImportEvidenceAndPublicationReplayKeys(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	s.Maintenance = MaintenanceConfig{ReceiptTTL: time.Hour, DraftHistoryTTL: time.Hour}
	p := importPlan(t, s, batch("one", "", "imported"))
	if _, err := s.Call(writer, "import_commit", commitArgs(p, "commit")); err != nil {
		t.Fatal(err)
	}
	r := callRecord(t, s, writer, "record_get", Args{ID: p.Items[0].ID, Publication: "draft"})
	callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("imported")})
	published := create(t, s, input("published"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: published.ID, Revision: published.RevisionID}))
	result := publish(t, s, ap, "durable-publication")
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE revisions SET body=json_set(body,'$.updated_at','2000-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(context.Background(), `UPDATE publication_receipts SET created_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.retain(context.Background(), "fixture", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(writer, "revision_get", Args{ID: r.ID, Revision: r.RevisionID}); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, s, "import_plans") != 1 || rowCount(t, s, "import_receipts") != 1 || rowCount(t, s, "publication_receipts") != 1 {
		t.Fatal("durable evidence pruned")
	}
	replay := publish(t, s, ap, "durable-publication")
	if !replay.Replayed || len(replay.Records) != len(result.Records) {
		t.Fatal(replay)
	}
}

func TestImportedRelationsRetainIndependentReviewerBoundary(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	request := cyclicBatch("consumes")
	p := importPlan(t, s, request)
	if p.Counts.Rejected != 0 {
		t.Fatal(p)
	}
	if _, err := s.Call(writer, "import_commit", commitArgs(p, "import")); err != nil {
		t.Fatal(err)
	}
	r := callRecord(t, s, writer, "record_get", Args{ID: p.Items[0].ID, Publication: "draft"})
	edge := r.Edges[0]
	if edge.ProposedBy != "writer" || edge.Hash == "" || edge.Hash != p.Candidates[0].Edges[0].Hash {
		t.Fatal("import changed or omitted proposal binding")
	}
	args := Args{ID: r.ID, Version: r.VersionToken, EdgeID: edge.ID, Hash: edge.Hash, Decision: "reviewed", Reason: "Independent fixture review"}
	s.reviewers["writer"] = "ground"
	_, err := s.Call(caller("writer", "registry.review"), "relation_decide", args)
	code(t, err, "forbidden")
	reviewed := callRecord(t, s, ground, "relation_decide", args)
	if reviewed.Edges[0].ReviewedBy != "ground" {
		t.Fatal(reviewed)
	}
}

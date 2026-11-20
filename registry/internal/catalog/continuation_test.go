package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
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
	// Compare the actual renderer rather than treating version admission as export proof.
	_, raw, err := renderExport(r)
	if err != nil || !strings.Contains(string(raw), "schema: project/2") || !strings.Contains(string(raw), "type: service") {
		t.Fatal(string(raw), err)
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

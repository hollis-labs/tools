package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	dbmigrate "github.com/hollis-labs/libs/core/store/migrate"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tools/registry/internal/catalog"
)

// This is generated legacy data, never the live catalog or rehearsal cohort.
// The converter and conditional record_migrate operation run on a verified
// backup only. No fixture revision is rewritten to fabricate a schema version.
func TestProject2MigrationOnVerifiedCopy(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	writer := libauth.WithPrincipal(ctx, libauth.Principal{ID: "writer", Verified: true, Scopes: []string{"registry.read", "registry.write"}})
	reviewer := libauth.WithPrincipal(ctx, libauth.Principal{ID: "ground", Verified: true, Scopes: []string{"registry.read", "registry.review"}})
	open := func(path string) *catalog.Service {
		cfg := store.Config{Driver: store.DriverSQLite, SchemaMode: store.SchemaAuto, SQLite: store.SQLiteConfig{Path: path}}
		db, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := dbmigrate.Ensure(ctx, cfg, db, dbmigrate.Source{FS: catalog.Migrations, Root: "migrations"}); err != nil {
			t.Fatal(err)
		}
		return catalog.New(db, map[string]string{"writer": "registry"}, map[string]string{"ground": "ground"})
	}
	source := open(filepath.Join(dir, "generated-legacy.db"))
	call := func(s *catalog.Service, c context.Context, op string, a catalog.Args) any {
		t.Helper()
		v, err := s.Call(c, op, a)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return v
	}
	assertCode := func(s *catalog.Service, op string, a catalog.Args, want string) {
		t.Helper()
		_, err := s.Call(writer, op, a)
		var ce *catalog.Error
		if !errors.As(err, &ce) || ce.Code != want {
			t.Fatalf("%s want %s: %v", op, want, err)
		}
	}
	inputs := map[string]*catalog.Input{}
	old := map[string]catalog.Record{}
	initial := map[string]catalog.Record{}
	for _, typ := range []string{"service", "lib", "framework", "monorepo"} {
		payload := strings.Replace(completeDraft, `"id":"example"`, `"id":"`+typ+`"`, 1)
		payload = strings.Replace(payload, `"type":"service"`, `"type":"`+typ+`"`, 1)
		if typ != "service" {
			payload = strings.Replace(payload, `"class":2`, `"class":"lib"`, 1)
			payload = strings.Replace(payload, `"type":"cli"`, `"type":"lib"`, 1)
		}
		in := &catalog.Input{Schema: "project/1", Key: typ, Summary: "preserve summary", Source: catalog.Source{URI: "fixture:generated", Revision: "source-rev"}, Payload: json.RawMessage(payload), Metadata: map[string]any{"note": "preserve envelope"}}
		if typ == "service" {
			var p map[string]any
			if err := json.Unmarshal(in.Payload, &p); err != nil {
				t.Fatal(err)
			}
			p["edges"] = []any{map[string]any{"type": "consumes", "to": "project:lib"}}
			in.Payload, _ = json.Marshal(p)
		}
		// Create the dependency first below; keep all source fixtures independent.
		inputs[typ] = in
	}
	for _, typ := range []string{"lib", "framework", "monorepo", "service"} {
		r := call(source, writer, "record_create", catalog.Args{Publication: "draft", Record: inputs[typ]}).(catalog.Record)
		initial[typ] = r
		old[typ] = r
	}
	// Retain a real immutable edit and an existing published project/1 snapshot.
	lib := old["lib"]
	ap := call(source, writer, "publication_approval_prepare", catalog.Args{Candidates: []catalog.Candidate{{ID: lib.ID, Revision: lib.RevisionID}}}).(catalog.Approval)
	ap = call(source, reviewer, "publication_approval_decide", catalog.Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "approved"}).(catalog.Approval)
	call(source, writer, "publication_publish", catalog.Args{Candidates: ap.Candidates, ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, IdempotencyKey: "fixture-publication"})
	inputs["service"].Summary = "updated summary"
	old["service"] = call(source, writer, "record_update", catalog.Args{Publication: "draft", ID: old["service"].ID, Version: old["service"].VersionToken, Record: inputs["service"]}).(catalog.Record)
	copyPath := filepath.Join(dir, "migration-copy.db")
	if _, err := source.DB.Backup(ctx, copyPath, store.BackupOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(dir, "generated-legacy.db")
	// Close/checkpoint the source so its checksum is a durable no-mutation proof.
	if err := source.DB.Close(); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.Sum256(sourceBytes)
	target := open(copyPath)
	discovered := call(target, writer, "kinds_list", catalog.Args{}).(map[string]any)["schemas"].([]string)
	found := false
	for _, schema := range discovered {
		if schema == "project/2" {
			found = true
		}
	}
	if !found {
		t.Fatal("project/2 missing from discovery")
	}
	schemaDoc := call(target, writer, "schema_get", catalog.Args{Schema: "project/2"})
	schemaJSON, err := json.Marshal(schemaDoc)
	if err != nil || !bytes.Contains(schemaJSON, []byte("project/2")) {
		t.Fatal("project/2 schema discovery", err)
	}

	snapshot := func(table string) map[string]string {
		t.Helper()
		rows, err := target.DB.QueryContext(ctx, "SELECT token,body FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var id string
			var body []byte
			if err := rows.Scan(&id, &body); err != nil {
				t.Fatal(err)
			}
			out[id] = string(body)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	history := snapshot("revisions")
	var publishedBefore []byte
	if err := target.DB.QueryRowContext(ctx, "SELECT body FROM publication_events WHERE id=?", lib.ID).Scan(&publishedBefore); err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	sourceJSON := map[string][]byte{}
	for _, typ := range []string{"lib", "framework", "monorepo", "service"} {
		envelope := catalog.Args{Publication: "draft", ID: old[typ].ID, Version: old[typ].VersionToken, Record: inputs[typ], Reason: "Explicit generated-copy project/2 conversion"}
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, typ+".json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		sourceJSON[path] = append([]byte(nil), raw...)
	}
	output := filepath.Join(dir, "converted")
	if err := migrate(output, paths); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"lib", "framework", "monorepo", "service"} {
		raw, err := os.ReadFile(filepath.Join(output, typ+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var args catalog.Args
		if err := catalog.Decode(raw, &args); err != nil {
			t.Fatal(err)
		}
		if args.ID != old[typ].ID || args.Version != old[typ].VersionToken || args.Record.Source != inputs[typ].Source || args.Record.Summary != inputs[typ].Summary || !reflect.DeepEqual(args.Record.Metadata, inputs[typ].Metadata) {
			t.Fatal("converter changed envelope fields", typ)
		}
		var p map[string]any
		if err := json.Unmarshal(args.Record.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["type"] != typ || p["disposition"] != "continue" || args.Record.Schema != "project/2" {
			t.Fatal("wrong destination", typ)
		}
		assertCode(target, "record_update", args, "validation_error")
		stale := args
		stale.Version = "stale-token"
		assertCode(target, "record_migrate", stale, "version_conflict")
		noReason := args
		noReason.Reason = ""
		assertCode(target, "record_migrate", noReason, "validation_error")
		// A missing dependency fails after preparation, inside the transaction.
		invalid := args
		copyInput := *args.Record
		invalid.Record = &copyInput
		invalid.Record.Payload = bytes.ReplaceAll(args.Record.Payload, []byte("project:lib"), []byte("project:missing"))
		if typ == "service" {
			assertCode(target, "record_migrate", invalid, "reference_unresolved")
		}
		changed := args
		copyInput = *args.Record
		changed.Record = &copyInput
		changed.Record.Payload = bytes.Replace(args.Record.Payload, []byte(`"id": "`+typ+`"`), []byte(`"id": "changed"`), 1)
		assertCode(target, "record_migrate", changed, "validation_error")
		got := call(target, writer, "record_migrate", args).(catalog.Record)
		if got.ID != old[typ].ID || got.CreatedAt != old[typ].CreatedAt || got.Schema != "project/2" || got.Publication != "draft" || got.RevisionID == old[typ].RevisionID || got.VersionToken == old[typ].VersionToken {
			t.Fatal("identity/draft/token invariants", typ)
		}
		if len(got.Edges) != len(old[typ].Edges) {
			t.Fatal("edges lost")
		}
		for i, e := range got.Edges {
			if !reflect.DeepEqual(e.Relation, old[typ].Edges[i].Relation) {
				t.Fatal("edge semantics changed")
			}
		}
		assertCode(target, "record_migrate", args, "version_conflict")
		args.Version = got.VersionToken
		assertCode(target, "record_migrate", args, "validation_error")
		v := call(target, writer, "migration_list", catalog.Args{ID: got.ID}).(map[string]any)
		logs := v["migrations"].([]catalog.KindMigration)
		if len(logs) != 1 {
			t.Fatal("failed writes/reruns leaked audit", logs)
		}
		m := logs[0]
		if m.RecordID != got.ID || m.FromSchema != "project/1" || m.ToSchema != "project/2" || m.FromRevision != old[typ].RevisionID || m.ToRevision != got.RevisionID || m.Actor != "writer" || m.Reason != args.Reason || m.MigratedAt == "" {
			t.Fatal("audit linkage", m)
		}
		if typ == "service" {
			// Relation mutations on project/2 validate against that record's version.
			call(target, writer, "relation_remove", catalog.Args{ID: got.ID, Version: got.VersionToken, EdgeID: got.Edges[0].ID})
		}
	}
	after := snapshot("revisions")
	if len(after) != len(history)+5 {
		t.Fatalf("unexpected revisions: %d -> %d", len(history), len(after))
	}
	for token, body := range history {
		if after[token] != body {
			t.Fatal("immutable history changed", token)
		}
	}
	var publishedAfter []byte
	if err := target.DB.QueryRowContext(ctx, "SELECT body FROM publication_events WHERE id=?", lib.ID).Scan(&publishedAfter); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(publishedBefore, publishedAfter) {
		t.Fatal("published snapshot changed")
	}
	published := call(target, writer, "record_get", catalog.Args{ID: lib.ID, Publication: "published"}).(catalog.Record)
	if published.Schema != "project/1" || published.RevisionID != initial["lib"].RevisionID {
		t.Fatal("published project/1 pointer changed")
	}
	var integrity string
	if err := target.DB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("copy integrity", integrity, err)
	}
	unchanged, err := os.ReadFile(sourcePath)
	if err != nil || sha256.Sum256(unchanged) != sourceHash {
		t.Fatal("source database changed")
	}
	t.Logf("GENERATED copy only: baseline bytes=%d source sha256=%x; 4 canonical IDs, 5 retained revisions, 4 linked audits, 1 preserved publication; conditional failures rolled back; integrity=%s", len(before), sourceHash, integrity)
	// All source JSON files remain byte-for-byte unchanged by offline conversion.
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, sourceJSON[path]) {
			t.Fatal(fmt.Sprint("source fixture changed: ", path, err))
		}
	}
}

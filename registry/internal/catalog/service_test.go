package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/kinds"
)

func openTest(t *testing.T, path string) *Service {
	t.Helper()
	cfg := store.Config{Driver: store.DriverSQLite, SchemaMode: store.SchemaAuto, SQLite: store.SQLiteConfig{Path: path}}
	db, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := migrate.Ensure(context.Background(), cfg, db, migrate.Source{FS: Migrations, Root: "migrations"}); checkErr != nil {
		t.Fatal(checkErr)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, map[string]string{"writer": "registry", "other": "external"}, map[string]string{"reviewer": "elevated", "ground": "ground"})
}
func caller(id string, scopes ...string) context.Context {
	return libauth.WithPrincipal(context.Background(), libauth.Principal{ID: id, Verified: true, Scopes: scopes})
}

var writer = caller("writer", "registry.read", "registry.write")
var reviewer = caller("reviewer", "registry.read", "registry.review")

func input(id string) *Input {
	return &Input{Schema: kinds.ProjectSchema, Key: id, Payload: json.RawMessage(`{"schema":"project/1","id":"` + id + `","name":"` + id + `","class":2,"lifecycle":"active","visibility":"private","owner":"user:test","interfaces":[{"type":"cli","auth":"none"}]}`)}
}
func create(t *testing.T, s *Service, in *Input) Record {
	t.Helper()
	x, err := s.Call(writer, "record_create", Args{Record: in})
	if err != nil {
		t.Fatal(err)
	}
	return x.(Record)
}

//nolint:revive // Test helpers take testing.T first.
func callRecord(t *testing.T, s *Service, ctx context.Context, op string, a Args) Record {
	t.Helper()
	x, err := s.Call(ctx, op, a)
	if err != nil {
		t.Fatal(err)
	}
	return x.(Record)
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s got %v", want, err)
	}
}
func TestPersistenceConflictsAndAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	s := openTest(t, path)
	a := Args{Record: input("alpha"), IdempotencyKey: "request-1"}
	r := callRecord(t, s, writer, "record_create", a)
	replay := callRecord(t, s, writer, "record_create", a)
	if replay.VersionToken != r.VersionToken {
		t.Fatal("receipt not stable")
	}
	a.Record = input("beta")
	_, err := s.Call(writer, "record_create", a)
	code(t, err, "version_conflict")
	_, err = s.Call(caller("other", "registry.write"), "record_create", Args{Record: input("alpha")})
	code(t, err, "version_conflict")
	_, err = s.Call(caller("other", "registry.write"), "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("alpha")})
	code(t, err, "forbidden")
	_, err = s.Call(caller("reader", "registry.read"), "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("alpha")})
	code(t, err, "forbidden")
	r2 := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("alpha")})
	_, err = s.Call(writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("alpha")})
	code(t, err, "version_conflict")
	if checkErr := s.DB.Close(); checkErr != nil {
		t.Fatal(checkErr)
	}
	s = openTest(t, path)
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.VersionToken != r2.VersionToken || got.VerifiedBy != "writer" {
		t.Fatal("persistence/attestation lost")
	}
	archived := callRecord(t, s, writer, "record_archive", Args{ID: r.ID, Version: got.VersionToken})
	tomb := callRecord(t, s, writer, "record_delete", Args{ID: r.ID, Version: archived.VersionToken})
	if tomb.Lifecycle != "deleted" {
		t.Fatal("not tombstoned")
	}
	_, err = s.Call(writer, "record_get", Args{ID: r.ID})
	code(t, err, "deleted")
	_, err = s.Call(writer, "record_create", Args{Record: input("alpha")})
	code(t, err, "version_conflict")
	old, err := s.Call(writer, "revision_get", Args{ID: r.ID, Revision: r.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	var historic Record
	if checkErr := json.Unmarshal(old.(json.RawMessage), &historic); checkErr != nil {
		t.Fatal(checkErr)
	}
	if historic.Lifecycle != "active" {
		t.Fatal("history changed")
	}
}
func TestSnapshotUsesImmutableRevisionsAcrossPages(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	c := create(t, s, input("c"))
	first, err := s.Call(writer, "snapshot_get", Args{Query: Query{Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	page := first.(Page)
	if !page.HasMore || page.Records[0].ID != a.ID {
		t.Fatal(page)
	}
	in := input("b")
	in.Summary = "after boundary"
	callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b.VersionToken, Record: in})
	archived := callRecord(t, s, writer, "record_archive", Args{ID: c.ID, Version: c.VersionToken})
	callRecord(t, s, writer, "record_delete", Args{ID: c.ID, Version: archived.VersionToken})
	create(t, s, input("d"))
	second, err := s.Call(writer, "snapshot_get", Args{Query: Query{Limit: 1, Cursor: page.Cursor}})
	if err != nil {
		t.Fatal(err)
	}
	p2 := second.(Page)
	if p2.Records[0].VersionToken != b.VersionToken || p2.Records[0].Summary != "" || p2.Boundary != page.Boundary {
		t.Fatalf("updated row leaked into snapshot: %+v", p2)
	}
	third, err := s.Call(writer, "snapshot_get", Args{Query: Query{Limit: 1, Cursor: p2.Cursor}})
	if err != nil {
		t.Fatal(err)
	}
	p3 := third.(Page)
	if p3.Records[0].VersionToken != c.VersionToken || p3.Records[0].Lifecycle != "active" || p3.HasMore {
		t.Fatal("archive/delete/create changed snapshot")
	}
	_, err = s.Call(writer, "snapshot_get", Args{Query: Query{Limit: 1, Cursor: page.Cursor, Kind: "tool"}})
	code(t, err, "validation_error")
	_, err = s.Call(caller("other", "registry.read"), "snapshot_get", Args{Query: Query{Limit: 1, Cursor: page.Cursor}})
	code(t, err, "validation_error")
}
func toolInput(name, description, alias string) *Input {
	t := kinds.Tool{Schema: kinds.ToolSchema, ID: "tool:mcp/example/" + name, Source: "mcp", Origin: "example", Lifecycle: "active", Definition: kinds.Definition{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`), Description: json.RawMessage(`"` + description + `"`), Annotations: json.RawMessage(`{"title":"Display"}`)}}
	if alias != "" {
		t.Aliases = []kinds.Alias{{System: "test", Alias: alias}}
	}
	raw, _ := json.Marshal(t)
	return &Input{Schema: kinds.ToolSchema, Key: name, Payload: raw}
}
func TestToolReviewInvalidationAndAliasRollback(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	r := create(t, s, toolInput("..", "before", "lookup"))
	if r.ID != "tool:mcp/example/.." || r.Review.State != "unreviewed" {
		t.Fatal(r)
	}
	cls := &kinds.Classification{SideEffect: []string{"read"}}
	_, err := s.Call(writer, "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Classification: cls, Decision: "reviewed", Approval: &kinds.Reference{Authority: "approval", Kind: "decision", ID: "review/1"}, Reason: "inspected"})
	code(t, err, "forbidden")
	approved := callRecord(t, s, reviewer, "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Classification: cls, Decision: "reviewed", Approval: &kinds.Reference{Authority: "approval", Kind: "decision", ID: "review/1"}, Reason: "inspected"})
	if approved.Review.ReviewedBy != "reviewer" || approved.Review.Effective == nil {
		t.Fatal("missing authenticated approval")
	}
	changed := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: approved.VersionToken, Record: toolInput("..", "after", "lookup")})
	if changed.Review.Effective != nil || changed.Review.State != "unreviewed" || changed.Review.DefinitionHash == r.Review.DefinitionHash {
		t.Fatal("stale approval carried forward")
	}
	_, err = s.Call(reviewer, "review_decide", Args{ID: r.ID, Version: changed.VersionToken, Hash: r.Review.DefinitionHash, Classification: cls, Decision: "reviewed", Approval: &kinds.Reference{Authority: "approval", Kind: "decision", ID: "review/1"}, Reason: "stale"})
	code(t, err, "version_conflict")
	_, err = s.Call(writer, "record_create", Args{Record: toolInput("_lookup", "other", "lookup")})
	code(t, err, "alias_conflict")
	_, err = s.Call(writer, "record_get", Args{ID: "tool:mcp/example/_lookup"})
	code(t, err, "not_found")
	got := callRecord(t, s, writer, "tool_resolve", Args{AliasSystem: "test", Alias: "lookup"})
	if got.ID != r.ID {
		t.Fatal("alias changed on rollback")
	}
}
func TestRelationsAndProjectPayloadAreAtomic(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	r := create(t, s, input("a"))
	relation := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: r.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: "project:missing"}, Source: "authored", Status: "proposed"}
	a := Args{ID: r.ID, Version: r.VersionToken, Relation: relation}
	_, err := s.Call(writer, "relation_create", a)
	code(t, err, "reference_unresolved")
	a.AllowUnresolved = true
	with := callRecord(t, s, writer, "relation_create", a)
	p, err := kinds.ParseProjectJSON(with.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Edges) != 1 || p.Edges[0].To != relation.To.ID {
		t.Fatal("second graph diverged")
	}
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.Edges[0].TargetState != "unresolved" {
		t.Fatal("unresolved hidden")
	}
	target := create(t, s, input("missing"))
	archived := callRecord(t, s, writer, "record_archive", Args{ID: target.ID, Version: target.VersionToken})
	callRecord(t, s, writer, "record_delete", Args{ID: target.ID, Version: archived.VersionToken})
	got = callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.Edges[0].TargetState != "deleted" {
		t.Fatal("tombstone target hidden")
	}
	removed := callRecord(t, s, writer, "relation_remove", Args{ID: r.ID, Version: got.VersionToken, EdgeID: got.Edges[0].ID})
	p, err = kinds.ParseProjectJSON(removed.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Edges) != 0 || len(removed.Edges) != 0 {
		t.Fatal("edge not removed atomically")
	}
}

func TestAllContentKindsPersistBodiesAndOpaqueIDs(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	cases := map[string]string{
		"agent/1":       `"charter":"Read carefully\nReturn evidence","roles":["reviewer"],"launch_profile":{"reference":{"authority":"tether","kind":"launch_profile","id":"unavailable/profile"},"revision":"r1"}`,
		"agency/1":      `"framework":"citadel","charter":"An organization","cells":[{"name":"reviewer","definition":{"charter":"Read","roles":["reviewer"]}}]`,
		"skill/1":       `"instructions":"# Inspect\nKeep this body"`,
		"document/1":    `"subtype":"engineering_bible","format":"markdown","body":"# Rules\nKeep exact authored body.\n"`,
		"workstream/1":  `"status":"active","workflow_id":"native/workflow"`,
		"correlation/1": `"links":{"task":{"authority":"torque","kind":"work_item","id":"task/opaque"}},"source_ids":{"correlation_id":"c1","in_reply_to":"c2","causation_id":"c3"}`,
	}
	for schema, tail := range cases {
		name := `,"name":"Name"`
		if schema == "workstream/1" {
			name = ""
		}
		raw := json.RawMessage(`{"schema":"` + schema + `","id":"native/../_id"` + name + `,` + tail + `}`)
		r := create(t, s, &Input{Schema: schema, Key: schema, Payload: raw})
		got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
		if string(got.Payload) != string(raw) {
			t.Fatalf("payload changed: %s", got.Payload)
		}
		if schema == "workstream/1" && got.Name != got.ID {
			t.Fatal("display fallback missing")
		}
	}
}

func TestConcurrentReplacementHasOneWinner(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	r := create(t, s, input("race"))
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := s.Call(writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: input("race")})
			results <- err
		}()
	}
	a, b := <-results, <-results
	if a == nil && b == nil || a != nil && b != nil {
		t.Fatalf("one writer should win: %v %v", a, b)
	}
	if a != nil {
		code(t, a, "version_conflict")
	} else {
		code(t, b, "version_conflict")
	}
}

func TestGroundReviewNeedsExistingElevationForRelaxedHints(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	r := create(t, s, toolInput("lookup", "untrusted", ""))
	no := false
	cls := &kinds.Classification{SideEffect: []string{"read"}, Idempotent: &no, ApprovalTier: "ground", Egress: "none", SecretAccess: "none"}
	ground := caller("ground", "registry.review")
	pending := callRecord(t, s, ground, "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Decision: "reviewed", Classification: cls, Reason: "looks safe"})
	if pending.Review.Effective != nil || pending.Review.QueueState != "needs_human" || pending.Review.ReviewedBy != "" {
		t.Fatal("untrusted hints granted ground approval")
	}
	_, err := s.Call(reviewer, "review_decide", Args{ID: r.ID, Version: pending.VersionToken, Hash: r.Review.DefinitionHash, Decision: "reviewed", Classification: cls, Reason: "elevated"})
	code(t, err, "validation_error")
	in := toolInput("safe", "declared read", "")
	var tool kinds.Tool
	if checkErr := json.Unmarshal(in.Payload, &tool); checkErr != nil {
		t.Fatal(checkErr)
	}
	tool.Definition.Annotations = json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false,"openWorldHint":false}`)
	in.Payload, _ = json.Marshal(tool)
	safe := create(t, s, in)
	approved := callRecord(t, s, ground, "review_decide", Args{ID: safe.ID, Version: safe.VersionToken, Hash: safe.Review.DefinitionHash, Decision: "reviewed", Classification: cls, Reason: "confirmed declaration"})
	if approved.Review.State != "reviewed" || approved.Review.Effective == nil {
		t.Fatal("valid ground review denied")
	}
}
func TestGenericWritesCannotSupplyServiceAuthority(t *testing.T) {
	for _, raw := range []string{`{"authority":"foreign"}`, `{"record":{"schema":"project/1","key":"a","payload":{},"last_verified":"today"}}`, `{"reviewed_by":"owner"}`, `{"record":{"effective":{"approval_tier":"ground"}}}`} {
		var a Args
		if err := Decode([]byte(raw), &a); err == nil {
			t.Errorf("accepted spoof %s", raw)
		}
	}
}

func TestIncomingRelationsDeriveInverseWithoutRewritingStoredDirection(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "registry.db"))
	source := create(t, s, input("source"))
	target := create(t, s, input("target"))
	rel := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: source.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}
	updated := callRecord(t, s, writer, "relation_create", Args{ID: source.ID, Version: source.VersionToken, Relation: rel})
	out, err := s.Call(writer, "relation_list", Args{ID: target.ID, Direction: "incoming"})
	if err != nil {
		t.Fatal(err)
	}
	edges := out.(map[string]any)["edges"].([]Edge)
	if len(edges) != 1 || edges[0].Inverse != "consumed_by" || edges[0].Relation.From.ID != source.ID || edges[0].SourceRevision != updated.RevisionID {
		t.Fatalf("inverse: %+v", edges)
	}
}

func TestGroundCannotEraseSameHashAuthoritativeDecision(t *testing.T) {
	for _, state := range []string{"reviewed", "denied", "quarantined"} {
		for _, proposal := range []bool{false, true} {
			t.Run(state+map[bool]string{false: "-direct", true: "-proposal"}[proposal], func(t *testing.T) {
				s := openTest(t, filepath.Join(t.TempDir(), "db"))
				in := toolInput("prior", "declared-read", "")
				var tool kinds.Tool
				if err := json.Unmarshal(in.Payload, &tool); err != nil {
					t.Fatal(err)
				}
				tool.Definition.Annotations = json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false,"openWorldHint":false}`)
				in.Payload, _ = json.Marshal(tool)
				r := create(t, s, in)
				no := false
				high := &kinds.Classification{SideEffect: []string{"destructive"}, Idempotent: &no, ApprovalTier: "elevated", Egress: "external", SecretAccess: "reads"}
				low := &kinds.Classification{SideEffect: []string{"read"}, Idempotent: &no, ApprovalTier: "ground", Egress: "none", SecretAccess: "none"}
				r = callRecord(t, s, reviewer, "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Decision: state, Classification: high, Reason: "restrictive evidence", Approval: &kinds.Reference{Authority: "approval", Kind: "decision", ID: "elevated/1"}})
				if proposal {
					r = callRecord(t, s, writer, "tool_propose", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Classification: low, Reason: "new proposal"})
				}
				r = callRecord(t, s, caller("ground", "registry.review"), "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Decision: "reviewed", Classification: low, Reason: "declared read"})
				if r.Review.Effective != nil || r.Review.QueueState != "needs_human" || r.Review.Approval != nil || r.Review.ReviewedBy != "" {
					t.Fatalf("same-hash decision bypass: %+v", r.Review)
				}
				// Existing elevated authority can replace it, with newly bound evidence.
				r = callRecord(t, s, reviewer, "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Decision: "reviewed", Classification: low, Reason: "new evidence", Approval: &kinds.Reference{Authority: "approval", Kind: "decision", ID: "elevated/2"}})
				if r.Review.State != "reviewed" || r.Review.Approval == nil || r.Review.Approval.ID != "elevated/2" {
					t.Fatal("elevation path lost")
				}
			})
		}
	}
}
func TestGroundConditionalRulesNeedElevation(t *testing.T) {
	for _, conditional := range []string{`"input_dependent":true`, `"rule_ref":"policy/rule"`, `"arg_rules":"policy/args"`, `"flag_patterns":[{"pattern":"--delete","side_effect":["destructive"],"reason":"deletes"}]`} {
		s := openTest(t, filepath.Join(t.TempDir(), "db"))
		in := toolInput("flags", "conditional", "")
		var tool kinds.Tool
		if err := json.Unmarshal(in.Payload, &tool); err != nil {
			t.Fatal(err)
		}
		tool.Definition.Annotations = json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false,"openWorldHint":false}`)
		in.Payload, _ = json.Marshal(tool)
		r := create(t, s, in)
		var cls kinds.Classification
		if err := json.Unmarshal([]byte(`{"side_effect":["read"],"idempotent":false,"approval_tier":"ground","egress":"none","secret_access":"none",`+conditional+`}`), &cls); err != nil {
			t.Fatal(err)
		}
		r = callRecord(t, s, caller("ground", "registry.review"), "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Decision: "reviewed", Classification: &cls, Reason: "read default"})
		if r.Review.Effective != nil || r.Review.QueueState != "needs_human" {
			t.Fatalf("conditional ground approval: %+v", r.Review)
		}
	}
}
func TestIncomingAndBothResolveArchivedTarget(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("from"))
	b := create(t, s, input("to"))
	rel := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: a.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: b.ID}, Source: "authored", Status: "proposed"}
	callRecord(t, s, writer, "relation_create", Args{ID: a.ID, Version: a.VersionToken, Relation: rel})
	callRecord(t, s, writer, "record_archive", Args{ID: b.ID, Version: b.VersionToken})
	for _, direction := range []string{"incoming", "both"} {
		out, err := s.Call(writer, "relation_list", Args{ID: b.ID, Direction: direction})
		if err != nil {
			t.Fatal(err)
		}
		edges := out.(map[string]any)["edges"].([]Edge)
		if len(edges) != 1 || edges[0].TargetState != "archived" || edges[0].CheckedAt == "" || edges[0].Relation.From.ID != a.ID {
			t.Fatalf("%s resolution: %+v", direction, edges)
		}
	}
}

func TestEnvelopeMetadataAdmissionRejectsWithoutRowsOrReceipts(t *testing.T) {
	for _, n := range []string{"9007199254740993", "1e-400", "1e400"} {
		t.Run(n, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "db"))
			in := input("metadata")
			in.Metadata = map[string]any{"nested": []any{json.Number(n)}}
			_, err := s.Call(writer, "record_create", Args{Record: in, IdempotencyKey: "metadata-request"})
			code(t, err, "validation_error")
			for _, table := range []string{"records", "revisions", "receipts"} {
				var count int
				var query string
				switch table {
				case "records":
					query = "SELECT COUNT(*) FROM records"
				case "revisions":
					query = "SELECT COUNT(*) FROM revisions"
				case "receipts":
					query = "SELECT COUNT(*) FROM receipts"
				}
				if checkErr := s.DB.QueryRowContext(context.Background(), query).Scan(&count); checkErr != nil {
					t.Fatal(checkErr)
				}
				if count != 0 {
					t.Fatalf("rejected write left %d %s", count, table)
				}
			}
			// Reusing the rejected creation key for admitted data must succeed: no receipt.
			in.Metadata = map[string]any{"number": json.Number("9007199254740991"), "exact": "9007199254740993"}
			r := callRecord(t, s, writer, "record_create", Args{Record: in, IdempotencyKey: "metadata-request"})
			got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
			if got.Metadata["number"] != float64(9007199254740991) || got.Metadata["exact"] != "9007199254740993" {
				t.Fatalf("admitted metadata changed: %+v", got.Metadata)
			}
			// Invalid replacement must leave the old revision current and readable.
			in.Metadata = map[string]any{"number": json.Number(n)}
			_, err = s.Call(writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: in})
			code(t, err, "validation_error")
			unchanged := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
			if unchanged.VersionToken != r.VersionToken {
				t.Fatal("invalid update advanced revision")
			}
		})
	}
}

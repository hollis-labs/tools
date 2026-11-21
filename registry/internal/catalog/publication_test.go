package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tools/registry/contracts"
)

const sensitiveSettingName = "password"

var ground = caller("ground", "registry.read", "registry.review")

func preparePublication(t *testing.T, s *Service, cs ...Candidate) Approval {
	t.Helper()
	v, err := s.Call(writer, "publication_approval_prepare", Args{Candidates: cs})
	if err != nil {
		t.Fatal(err)
	}
	return v.(Approval)
}
func approvePublication(t *testing.T, s *Service, ap Approval) Approval {
	t.Helper()
	v, err := s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	return v.(Approval)
}
func publicationArgs(ap Approval, key string) Args {
	return Args{Candidates: ap.Candidates, ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, IdempotencyKey: key}
}
func publish(t *testing.T, s *Service, ap Approval, key string) PublicationResult {
	t.Helper()
	v, err := s.Call(writer, "publication_publish", publicationArgs(ap, key))
	if err != nil {
		t.Fatal(err)
	}
	return v.(PublicationResult)
}
func TestPublicationDraftEditReadAndReplay(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("alpha"))
	_, err := s.Call(writer, "record_get", Args{ID: r.ID})
	code(t, err, "not_found")
	page, err := s.Call(writer, "snapshot_get", Args{})
	if err != nil || len(page.(Page).Records) != 0 {
		t.Fatalf("draft leaked: %v %v", page, err)
	}
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	first := publish(t, s, ap, "first")
	next := input("alpha")
	next.Summary = "Draft changed"
	r2 := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: next})
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.RevisionID != r.RevisionID || got.Publication != "published" {
		t.Fatal("published revision overwritten")
	}
	draft := callRecord(t, s, writer, "record_get", Args{ID: r.ID, Publication: "draft"})
	if draft.RevisionID != r2.RevisionID || draft.Publication != "draft" {
		t.Fatal("draft not visible")
	}
	ap2 := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r2.ID, Revision: r2.RevisionID, ExpectedPublishedRevision: r.RevisionID}))
	publish(t, s, ap2, "second")
	replay := publish(t, s, ap, "first")
	if !replay.Replayed || replay.ID != first.ID || replay.CurrentAvailability[r.ID] != "superseded" {
		t.Fatalf("bad replay: %+v", replay)
	}
	got = callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.RevisionID != r2.RevisionID {
		t.Fatal("replay rewound pointer")
	}
	_, err = s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "withdrawn"})
	if err != nil {
		t.Fatal(err)
	}
	if !publish(t, s, ap, "first").Replayed {
		t.Fatal("withdrawal destroyed historical replay")
	}
	_, err = s.Call(writer, "publication_publish", publicationArgs(ap, "new-key"))
	code(t, err, "version_conflict")
	changed := publicationArgs(ap, "first")
	changed.Hash = "changed"
	_, err = s.Call(writer, "publication_publish", changed)
	code(t, err, "version_conflict")
	callRecord(t, s, writer, "record_archive", Args{ID: r2.ID, Version: r2.VersionToken})
	if publish(t, s, ap, "first").CurrentAvailability[r.ID] != "archived" {
		t.Fatal("archive availability missing")
	}
	var n int
	if err = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM publication_events`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("replay emitted events: %d %v", n, err)
	}
}
func TestPublicationCASAtomicityAndConcurrentReplay(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}, Candidate{ID: b.ID, Revision: b.RevisionID}))
	callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b.VersionToken, Record: input("b")})
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, "batch"))
	code(t, err, "version_conflict")
	_, err = s.Call(writer, "record_get", Args{ID: a.ID})
	code(t, err, "not_found")
	one := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}))
	var wg sync.WaitGroup
	results := make(chan PublicationResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.Call(writer, "publication_publish", publicationArgs(one, "concurrent"))
			errs <- e
			if e == nil {
				results <- v.(PublicationResult)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	x, y := <-results, <-results
	if x.ID != y.ID || x.Replayed == y.Replayed {
		t.Fatal("concurrent key committed twice")
	}
	// Another approved request cannot replace the newly published pointer with an empty expected pointer.
	_, err = s.Call(writer, "publication_publish", publicationArgs(one, "another"))
	code(t, err, "version_conflict")
}
func TestPublicationEdgesArePinnedAndDraftGraphIsIsolated(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	target := approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b.RevisionID}))
	publish(t, s, target, "b")
	rel := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: a.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: b.ID}, Source: "authored", Status: "proposed"}
	a = callRecord(t, s, writer, "relation_create", Args{ID: a.ID, Version: a.VersionToken, Relation: rel})
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}))
	b2 := callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b.VersionToken, Record: input("b")})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b2.RevisionID, ExpectedPublishedRevision: b.RevisionID})), "b2")
	result := publish(t, s, ap, "a")
	if result.Records[0].TargetPins[a.Edges[0].ID] != b.RevisionID {
		t.Fatal("target silently repinned")
	}
	a2 := callRecord(t, s, writer, "relation_remove", Args{ID: a.ID, Version: a.VersionToken, EdgeID: a.Edges[0].ID})
	got := callRecord(t, s, writer, "record_get", Args{ID: a.ID})
	if len(got.Edges) != 1 {
		t.Fatal("draft edge removal leaked")
	}
	changed := preparePublication(t, s, Candidate{ID: a.ID, Revision: a2.RevisionID, ExpectedPublishedRevision: a.RevisionID})
	if changed.ManifestDigest == ap.ManifestDigest {
		t.Fatal("edge edit did not invalidate subject")
	}
	incoming, err := s.Call(writer, "relation_list", Args{ID: b.ID, Direction: "incoming"})
	if err != nil || len(incoming.(map[string]any)["edges"].([]Edge)) != 1 {
		t.Fatalf("published incoming edge lost: %v %v", incoming, err)
	}
	callRecord(t, s, writer, "record_archive", Args{ID: b2.ID, Version: b2.VersionToken})
	// Fresh approval on the old pinned dependency cannot bypass target archive.
	_, err = s.Call(writer, "publication_approval_prepare", Args{Candidates: []Candidate{{ID: a.ID, Revision: a.RevisionID}}})
	code(t, err, "version_conflict")
}
func TestPublicationElevatedRefusalEvenForOwnerAndForgedEvidence(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	s.reviewers["owner"] = "owner"
	raw := json.RawMessage(`{"schema":"agent/1","id":"a","name":"A","charter":"Read","roles":["reader"]}`)
	r := create(t, s, &Input{Schema: "agent/1", Key: "a", Payload: raw})
	ap := preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})
	for _, principal := range []context.Context{ground, reviewer, caller("owner", "registry.read", "registry.review")} {
		_, err := s.Call(principal, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "approved", Approval: &kinds.Reference{Authority: "human", Kind: "approval", ID: "fabricated"}})
		code(t, err, "forbidden")
	}
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, "blocked"))
	code(t, err, "forbidden")
}
func TestPublicationPolicyAndWithdrawalOrdering(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	s.reviewers["owner"] = "owner"
	owner := caller("owner", "registry.read", "registry.review")
	r := create(t, s, input("a"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	p, err := s.Call(owner, "publication_policy_get", Args{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(owner, "publication_policy_set", Args{Version: p.(PublicationPolicy).Revision, Roles: map[string]string{"owner": "owner", "ground": "ground"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(writer, "publication_publish", publicationArgs(ap, "policy"))
	code(t, err, "forbidden")
	ap = approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	_, err = s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "withdrawn"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(writer, "publication_publish", publicationArgs(ap, "withdrawn"))
	code(t, err, "version_conflict")
}

func TestPublicationSnapshotRetainsPublishedBoundary(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}, Candidate{ID: b.ID, Revision: b.RevisionID})), "batch")
	first, err := s.Call(writer, "snapshot_get", Args{Query: Query{Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	page := first.(Page)
	b2 := callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b.VersionToken, Record: input("b")})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b2.ID, Revision: b2.RevisionID, ExpectedPublishedRevision: b.RevisionID})), "new-b")
	callRecord(t, s, writer, "record_archive", Args{ID: b2.ID, Version: b2.VersionToken})
	second, err := s.Call(writer, "snapshot_get", Args{Query: Query{Limit: 1, Cursor: page.Cursor}})
	if err != nil {
		t.Fatal(err)
	}
	old := second.(Page)
	if len(old.Records) != 1 || old.Records[0].RevisionID != b.RevisionID || old.Records[0].Lifecycle != "active" {
		t.Fatalf("snapshot moved: %+v", old)
	}
	_, err = s.Call(writer, "snapshot_get", Args{Query: Query{Publication: "draft", Limit: 1, Cursor: page.Cursor}})
	code(t, err, "validation_error")
}
func TestPublicationDenialExpiryAndReadScope(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("a"))
	ap := preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})
	_, err := s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: "wrong", Decision: "approved"})
	code(t, err, "version_conflict")
	denied, err := s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(writer, "publication_publish", publicationArgs(denied.(Approval), "deny"))
	code(t, err, "forbidden")
	good := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	publish(t, s, good, "committed")
	_, err = s.Call(caller("writer", "registry.write"), "publication_publish", publicationArgs(good, "committed"))
	code(t, err, "forbidden")
	// Simulate a persisted expired receipt without introducing clock sleeps.
	good.ExpiresAt = "2000-01-01T00:00:00Z"
	raw, _ := json.Marshal(good)
	if _, err = s.DB.ExecContext(context.Background(), `UPDATE publication_approvals SET body=? WHERE id=?`, raw, good.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Call(writer, "publication_publish", publicationArgs(good, "expired"))
	code(t, err, "forbidden")
	if !publish(t, s, good, "committed").Replayed {
		t.Fatal("expiry broke committed replay")
	}
}
func TestPublicationWithdrawalRaceIsSerialized(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("a"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	ch := make(chan error, 1)
	go func() {
		_, err := s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "withdrawn"})
		ch <- err
	}()
	v, err := s.Call(writer, "publication_publish", publicationArgs(ap, "race"))
	if er := <-ch; er != nil {
		t.Fatal(er)
	}
	if err == nil {
		if !publish(t, s, ap, "race").Replayed || v.(PublicationResult).ID == "" {
			t.Fatal("committed race lost receipt")
		}
	} else {
		code(t, err, "version_conflict")
		_, er := s.Call(writer, "record_get", Args{ID: r.ID})
		code(t, er, "not_found")
	}
}

func TestPublicationBehaviorForExistingKinds(t *testing.T) {
	cases := map[string]string{
		"agent/1":       `"charter":"Read","roles":["reader"]`,
		"agency/1":      `"framework":"citadel","charter":"Organization","cells":[{"name":"reviewer","definition":{"charter":"Read","roles":["reviewer"]}}]`,
		"skill/1":       `"instructions":"Read carefully"`,
		"document/1":    `"subtype":"procedure","format":"markdown","body":"Instructions"`,
		"workstream/1":  `"status":"active","workflow_id":"native/workflow"`,
		"correlation/1": `"links":{"task":{"authority":"torque","kind":"work_item","id":"opaque"}},"source_ids":{"correlation_id":"c1"}`,
	}
	for schema, tail := range cases {
		t.Run(schema, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "db"))
			name := `,"name":"Name"`
			if schema == "workstream/1" {
				name = ""
			}
			in := &Input{Schema: schema, Key: "kind", Payload: json.RawMessage(`{"schema":"` + schema + `","id":"kind"` + name + `,` + tail + `}`)}
			r := create(t, s, in)
			_, err := s.Call(writer, "record_get", Args{ID: r.ID})
			code(t, err, "not_found")
			ap := preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})
			if schema == "agent/1" || schema == "agency/1" {
				_, err = s.Call(ground, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "approved"})
				code(t, err, "forbidden")
				_, err = s.Call(writer, "publication_publish", publicationArgs(ap, "denied"))
				code(t, err, "forbidden")
			} else {
				ap = approvePublication(t, s, ap)
				publish(t, s, ap, "publish")
			}
			in.Summary = "Edited draft"
			r2 := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: in})
			if r2.Publication != "draft" {
				t.Fatal("edit not draft")
			}
			if ap.Elevated {
				_, err = s.Call(writer, "record_get", Args{ID: r.ID})
				code(t, err, "not_found")
			} else {
				got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
				if got.RevisionID != r.RevisionID {
					t.Fatal("draft replaced publication")
				}
				publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r2.ID, Revision: r2.RevisionID, ExpectedPublishedRevision: r.RevisionID})), "edit")
			}
		})
	}
}
func readOnlyToolInput(name, alias string) *Input {
	in := toolInput(name, "Read only", alias)
	var tool kinds.Tool
	_ = json.Unmarshal(in.Payload, &tool)
	tool.Definition.Annotations = json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false}`)
	in.Payload, _ = json.Marshal(tool)
	return in
}
func classifyGround(t *testing.T, s *Service, r Record) Record {
	t.Helper()
	yes := true
	c := &kinds.Classification{SideEffect: []string{"read"}, Idempotent: &yes, Egress: "none", SecretAccess: "none", ApprovalTier: "ground"}
	return callRecord(t, s, ground, "review_decide", Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Classification: c, Decision: "reviewed", Reason: "Read-only fixture"})
}
func TestPublicationToolClassificationSeparateFromEdgeApproval(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, readOnlyToolInput("read", "original"))
	unreviewed := preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})
	if !unreviewed.Elevated {
		t.Fatal("unreviewed tool considered ground")
	}
	r = classifyGround(t, s, r)
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	publish(t, s, ap, "tool")
	target := create(t, s, input("project"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: target.ID, Revision: target.RevisionID})), "target")
	rel := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "tool", ID: r.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}
	edge := callRecord(t, s, writer, "relation_create", Args{ID: r.ID, Version: r.VersionToken, Relation: rel})
	if edge.Review.DefinitionHash != r.Review.DefinitionHash || edge.Review.State != "reviewed" {
		t.Fatal("edge edit erased TR-1 review")
	}
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, "reuse-after-edge"))
	code(t, err, "version_conflict")
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if len(got.Edges) != 0 {
		t.Fatal("edge draft leaked")
	}
	next := approvePublication(t, s, preparePublication(t, s, Candidate{ID: edge.ID, Revision: edge.RevisionID, ExpectedPublishedRevision: r.RevisionID}))
	if next.ManifestDigest == ap.ManifestDigest {
		t.Fatal("edge-only hash unchanged")
	}
	publish(t, s, next, "edges")
	edited := callRecord(t, s, writer, "record_update", Args{ID: edge.ID, Version: edge.VersionToken, Record: readOnlyToolInput("read", "changed")})
	resolved := callRecord(t, s, writer, "tool_resolve", Args{AliasSystem: "test", Alias: "original"})
	if resolved.RevisionID != edge.RevisionID {
		t.Fatal("draft alias changed resolution")
	}
	_, err = s.Call(writer, "tool_resolve", Args{AliasSystem: "test", Alias: "changed"})
	code(t, err, "not_found")
	if edited.Review.State != "reviewed" {
		t.Fatal("alias-only edit changed classification")
	}
}

func TestPublicationObservationDoesNotChangeSubject(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("a"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	verified := callRecord(t, s, writer, "record_verify", Args{ID: r.ID, Version: r.VersionToken})
	if verified.RevisionID != r.RevisionID || verified.LastVerified == r.LastVerified {
		t.Fatal("verification must change only observation")
	}
	check := preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})
	if check.ManifestDigest != ap.ManifestDigest {
		t.Fatal("observation changed authored digest")
	}
	publish(t, s, ap, "after-verification")
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.LastVerified != verified.LastVerified {
		t.Fatal("current verification missing")
	}

}

func TestPublicationAliasOwnershipSurvivesDraftRemoval(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := classifyGround(t, s, create(t, s, readOnlyToolInput("a", "shared")))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "a")
	a2 := callRecord(t, s, writer, "record_update", Args{ID: a.ID, Version: a.VersionToken, Record: readOnlyToolInput("a", "")})
	b := classifyGround(t, s, create(t, s, readOnlyToolInput("b", "shared")))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b.RevisionID}))
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, "collision"))
	code(t, err, "alias_conflict")
	got := callRecord(t, s, writer, "tool_resolve", Args{AliasSystem: "test", Alias: "shared"})
	if got.ID != a.ID {
		t.Fatal("draft stole published alias")
	}
	batch := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a2.ID, Revision: a2.RevisionID, ExpectedPublishedRevision: a.RevisionID}, Candidate{ID: b.ID, Revision: b.RevisionID}))
	publish(t, s, batch, "transfer")
	got = callRecord(t, s, writer, "tool_resolve", Args{AliasSystem: "test", Alias: "shared"})
	if got.ID != b.ID {
		t.Fatal("atomic alias transfer failed")
	}
}
func TestPublicationIncomingPinsAndLifecycleIgnoreDrafts(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b.RevisionID})), "target")
	rel := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: a.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: b.ID}, Source: "authored", Status: "proposed"}
	a = callRecord(t, s, writer, "relation_create", Args{ID: a.ID, Version: a.VersionToken, Relation: rel})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID})), "source")
	b2 := callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b.VersionToken, Record: input("b")})
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b2.RevisionID, ExpectedPublishedRevision: b.RevisionID})), "target2")
	archivedInput := input("b")
	archivedInput.Summary = "Unpublished target edit"
	b3 := callRecord(t, s, writer, "record_update", Args{ID: b.ID, Version: b2.VersionToken, Record: archivedInput})
	for _, args := range []Args{{ID: a.ID}, {ID: b.ID, Direction: "incoming"}, {ID: b.ID, Direction: "both"}} {
		v, err := s.Call(writer, "relation_list", args)
		if err != nil {
			t.Fatal(err)
		}
		edges := v.(map[string]any)["edges"].([]Edge)
		if len(edges) != 1 || edges[0].TargetRevision != b.RevisionID || edges[0].TargetState != "resolved" {
			t.Fatalf("published pin/state changed: %+v", edges)
		}
	}
	// Prepare while publication is active, then archive before commit.
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID, ExpectedPublishedRevision: a.RevisionID}))
	callRecord(t, s, writer, "record_archive", Args{ID: b.ID, Version: b3.VersionToken})
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, "archived-dependency"))
	code(t, err, "reference_unavailable")
}
func TestPublicationReceiptsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s := openTest(t, path)
	r := create(t, s, input("a"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	first := publish(t, s, ap, "durable")
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path)
	replay := publish(t, s, ap, "durable")
	if !replay.Replayed || replay.ID != first.ID {
		t.Fatal("receipt lost on reopen")
	}
}

// Admission hashes include the complete schema bundle, including tool-package/1.
func TestPublicationGoldenVectors(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	target := create(t, s, input("target"))
	r, err := prepare(input("golden"), "registry")
	if err != nil {
		t.Fatal(err)
	}
	r.RevisionID = "revision-1"
	cases := []struct {
		name  string
		edges []Edge
		want  string
	}{
		{"empty", nil, "sha256:fb8f7348ee890af5592ea8449edb7d53aa988fe7cc94d40d729422fea7dfd81d"},
		{"edge", []Edge{{ID: "edge-1", SourceRevision: "revision-1", Relation: kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: r.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}}}, "sha256:b682e86945dafda7cd037dbb8ccbfcbcde7afa2b5b317dce5432259bac19250e"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.Edges = tc.edges
			sub, err := subjectFor(context.Background(), s.DB, r, map[string]string{target.ID: "target-revision-1"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := canonicalDigest(sub)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				raw, _ := json.Marshal(sub)
				t.Fatalf("golden %s got %s\n%s", tc.name, got, raw)
			}
			// JSON property order is immaterial; authored semantic changes are not.
			original := r.Payload
			var payload map[string]json.RawMessage
			if err = json.Unmarshal(original, &payload); err != nil {
				t.Fatal(err)
			}
			r.Payload, _ = json.Marshal(payload)
			reordered, err := subjectFor(context.Background(), s.DB, r, map[string]string{target.ID: "target-revision-1"})
			if err != nil {
				t.Fatal(err)
			}
			h, _ := canonicalDigest(reordered)
			if h != got {
				t.Fatal("object order changed digest")
			}
			r.LastVerified = "later"
			r.VerifiedBy = "observer"
			r.PublishedAt = "later"
			r.Review = &Review{State: "observed"}
			observed, err := subjectFor(context.Background(), s.DB, r, map[string]string{target.ID: "target-revision-1"})
			if err != nil {
				t.Fatal(err)
			}
			h, _ = canonicalDigest(observed)
			if h != got {
				t.Fatal("operational observation changed digest")
			}
			r.Metadata = map[string]any{"authored": "change"}
			changed, err := subjectFor(context.Background(), s.DB, r, map[string]string{target.ID: "target-revision-1"})
			if err != nil {
				t.Fatal(err)
			}
			h, _ = canonicalDigest(changed)
			if h == got {
				t.Fatal("metadata omitted from digest")
			}
			r.Metadata = nil
			r.Payload = original
		})
	}
}
func TestPublicationDigestRejectsUnsafeNumbersAndDuplicateEdges(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{"n":9007199254740993}`), json.RawMessage(`{"n":1e-400}`), json.RawMessage(`{"n":1e400}`), json.RawMessage(`{"x":1,"x":2}`)} {
		if _, err := canonicalDigest(raw); err == nil {
			t.Fatalf("unsafe canonical input admitted: %s", raw)
		}
	}
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("a"))
	e := Edge{ID: "duplicate", Relation: kinds.Relation{To: kinds.Reference{Authority: "registry", Kind: "project", ID: r.ID}}}
	r.Edges = []Edge{e, e}
	_, err := subjectFor(context.Background(), s.DB, r, map[string]string{r.ID: r.RevisionID})
	code(t, err, "validation_error")
}

func newKindInput(schema string) *Input {
	tails := map[string]string{
		"service/1":        `"owner":"example-owner","operator":"example-operator","host":"example-host","cerberus_resource_id":"catalog","deploy_method":"cerberus","description":"Catalog","project":{"authority":"registry","kind":"project","id":"project:parent"},"interfaces":[{"type":"mcp","name":"catalog"}]`,
		"config/1":         `"scope":"project","target":{"authority":"registry","kind":"project","id":"project:parent"},"settings_schema":{"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false},"settings":{"limit":5},"secret_refs":{"API_KEY":{"authority":"vault","kind":"secret","id":"catalog/api-key"}}`,
		"knowledge/1":      `"format":"markdown","body":"# Guide","provenance":[{"authority":"author","kind":"document","id":"guide"}]`,
		"mcp_server/1":     `"transport":"stdio","source":{"reference":{"authority":"tether","kind":"mcp_server","id":"native"},"revision":"v1"}`,
		"launch_profile/1": `"source":{"reference":{"authority":"tether","kind":"launch_profile","id":"native"},"revision":"v1"}`,
	}
	return &Input{Schema: schema, Key: "example", Payload: json.RawMessage(`{"schema":"` + schema + `","id":"example","name":"Example",` + tails[schema] + `}`)}
}
func TestPublicationBehaviorForNewKinds(t *testing.T) {
	for _, schema := range []string{"service/1", "config/1", "knowledge/1", "mcp_server/1", "launch_profile/1"} {
		t.Run(schema, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "db"))
			parent := create(t, s, input("parent"))
			publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: parent.ID, Revision: parent.RevisionID})), "parent")
			in := newKindInput(schema)
			r := create(t, s, in)
			if r.Publication != "draft" {
				t.Fatal("creation not draft")
			}
			_, err := s.Call(writer, "record_get", Args{ID: r.ID})
			code(t, err, "not_found")
			ap := preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID})
			if ap.Elevated {
				_, err = s.Call(reviewer, "publication_approval_decide", Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "approved"})
				code(t, err, "forbidden")
				_, err = s.Call(writer, "publication_publish", publicationArgs(ap, "blocked"))
				code(t, err, "forbidden")
			} else {
				ap = approvePublication(t, s, ap)
				publish(t, s, ap, "first")
			}
			in.Summary = "edited"
			r2 := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: in})
			if r2.RevisionID == r.RevisionID {
				t.Fatal("edit reused revision")
			}
			if ap.Elevated {
				_, err = s.Call(writer, "record_get", Args{ID: r.ID})
				code(t, err, "not_found")
			} else {
				got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
				if got.RevisionID != r.RevisionID {
					t.Fatal("draft leaked")
				}
				publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r2.ID, Revision: r2.RevisionID, ExpectedPublishedRevision: r.RevisionID})), "edited")
			}
			in.Metadata = map[string]any{sensitiveSettingName: "not-a-reference"}
			_, err = s.Call(writer, "record_update", Args{ID: r2.ID, Version: r2.VersionToken, Record: in})
			code(t, err, "validation_error")
			got := callRecord(t, s, writer, "record_get", Args{ID: r2.ID, Publication: "draft"})
			if got.RevisionID != r2.RevisionID {
				t.Fatal("secret refusal changed draft")
			}
		})
	}
}
func TestPublicationSecretRefusalEnvelopeAndErrorRedaction(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	in := input("secret-refusal")
	in.Metadata = map[string]any{sensitiveSettingName: "secret-placeholder"}
	_, err := s.Call(writer, "record_create", Args{Record: in, IdempotencyKey: "refusal"})
	code(t, err, "validation_error")
	if strings.Contains(err.Error(), "secret-placeholder") {
		t.Fatal("error echoed rejected content")
	}
	var count int
	for _, table := range []string{"records", "revisions", "receipts"} {
		if err = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("refusal persisted %s: %d %v", table, count, err)
		}
	}
	in.Metadata = map[string]any{sensitiveSettingName: map[string]any{"authority": "vault", "kind": "secret", "id": "db/password"}}
	create(t, s, in)
}

func TestPublicationBatchCyclesUseSharedPolicy(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second kinds.EdgeType
		allowed       bool
	}{{"descriptive", kinds.Consumes, kinds.Consumes, true}, {"mixed", kinds.Consumes, kinds.PartOf, false}, {"hierarchy", kinds.PartOf, kinds.PartOf, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "db"))
			a := create(t, s, input("a"))
			b := create(t, s, input("b"))
			edge := func(from, to Record, relation kinds.EdgeType) Record {
				return callRecord(t, s, writer, "relation_create", Args{ID: from.ID, Version: from.VersionToken, Relation: &kinds.Relation{Schema: kinds.RelationSchema, Type: relation, From: kinds.Reference{Authority: "registry", Kind: "project", ID: from.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: to.ID}, Source: "authored", Status: "proposed"}})
			}
			a = edge(a, b, tc.first)
			b = edge(b, a, tc.second)
			ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}, Candidate{ID: b.ID, Revision: b.RevisionID}))
			v, err := s.Call(writer, "publication_publish", publicationArgs(ap, "cycle"))
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
				if len(v.(PublicationResult).Records) != 2 {
					t.Fatal("partial cycle batch")
				}
			} else {
				code(t, err, "validation_error")
				_, err = s.Call(writer, "record_get", Args{ID: a.ID})
				code(t, err, "not_found")
			}
		})
	}
}
func TestPublicationTypedDependenciesReplaceStaleGeneratedEdges(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	p := create(t, s, input("parent"))
	other := create(t, s, input("other"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: p.ID, Revision: p.RevisionID}, Candidate{ID: other.ID, Revision: other.RevisionID})), "parents")
	in := newKindInput("service/1")
	r := create(t, s, in)
	if len(r.Edges) != 1 || r.Edges[0].Relation.Type != kinds.PartOf {
		t.Fatal("service project missing typed edge")
	}
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	first := publish(t, s, ap, "service")
	if first.Records[0].TargetPins[r.Edges[0].ID] != p.RevisionID {
		t.Fatal("embedded project not pinned")
	}
	_, err := s.Call(writer, "relation_remove", Args{ID: r.ID, Version: r.VersionToken, EdgeID: r.Edges[0].ID})
	code(t, err, "validation_error")
	in.Payload = json.RawMessage(strings.Replace(string(in.Payload), "project:parent", "project:other", 1))
	r2 := callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: in})
	if len(r2.Edges) != 1 || r2.Edges[0].Relation.To.ID != other.ID {
		t.Fatal("stale embedded target retained")
	}
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID})
	if got.Edges[0].Relation.To.ID != p.ID {
		t.Fatal("draft embedded dependency leaked")
	}
	next := publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: r2.ID, Revision: r2.RevisionID, ExpectedPublishedRevision: r.RevisionID})), "edited-service")
	if next.Records[0].TargetPins[r2.Edges[0].ID] != other.RevisionID {
		t.Fatal("new dependency not pinned")
	}
}
func TestPublicationSubjectEdgeOrderAndTargetPins(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	r := create(t, s, input("source"))
	for i, target := range []Record{a, b} {
		r.Edges = append(r.Edges, Edge{ID: fmt.Sprintf("edge-%d", i), SourceRevision: r.RevisionID, Relation: kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: r.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}})
	}
	pins := map[string]string{a.ID: a.RevisionID, b.ID: b.RevisionID}
	sub, err := subjectFor(context.Background(), s.DB, r, pins)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := canonicalDigest(sub)
	r.Edges[0], r.Edges[1] = r.Edges[1], r.Edges[0]
	ordered, err := subjectFor(context.Background(), s.DB, r, pins)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := canonicalDigest(ordered)
	if other != h {
		t.Fatal("input edge order changed subject")
	}
	pins[a.ID] = "different-revision"
	changed, err := subjectFor(context.Background(), s.DB, r, pins)
	if err != nil {
		t.Fatal(err)
	}
	other, _ = canonicalDigest(changed)
	if other == h {
		t.Fatal("target revision omitted")
	}
	weight := 0.5
	r.Edges[0].Relation.Weight = &weight
	changed, err = subjectFor(context.Background(), s.DB, r, pins)
	if err != nil {
		t.Fatal(err)
	}
	other, _ = canonicalDigest(changed)
	if other == h {
		t.Fatal("authored attributes omitted")
	}
}

func TestPublicationRejectsArchivedTransitiveDependency(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	c := create(t, s, input("c"))
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: c.ID, Revision: c.RevisionID})), "c")
	link := func(source, target Record) Record {
		return callRecord(t, s, writer, "relation_create", Args{ID: source.ID, Version: source.VersionToken, Relation: &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: source.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: target.ID}, Source: "authored", Status: "proposed"}})
	}
	b = link(b, c)
	publish(t, s, approvePublication(t, s, preparePublication(t, s, Candidate{ID: b.ID, Revision: b.RevisionID})), "b")
	a = link(a, b)
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}))
	callRecord(t, s, writer, "record_archive", Args{ID: c.ID, Version: c.VersionToken})
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, "a"))
	code(t, err, "reference_unavailable")
	_, err = s.Call(writer, "record_get", Args{ID: a.ID})
	code(t, err, "not_found")
}

func TestPublicationReceiptKeysCannotStoreCredentials(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("a"))
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: r.ID, Revision: r.RevisionID}))
	// Construct a synthetic detector fixture, never a real credential.
	key := strings.Join([]string{"Bearer", strings.Repeat("a", 40)}, " ")
	_, err := s.Call(writer, "publication_publish", publicationArgs(ap, key))
	code(t, err, "validation_error")
	if strings.Contains(err.Error(), key) {
		t.Fatal("credential-like key echoed")
	}
	var count int
	if err = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM publication_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("credential-like receipt key persisted")
	}
}

func TestReviewApprovalReferenceCannotPersistCredentials(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, toolInput("unsafe", "Definition", ""))
	synthetic := strings.Join([]string{"Bearer", strings.Repeat("b", 40)}, " ")
	args := Args{ID: r.ID, Version: r.VersionToken, Hash: r.Review.DefinitionHash, Classification: &kinds.Classification{SideEffect: []string{"read"}}, Decision: "reviewed", Reason: "Inspection", Approval: &kinds.Reference{Authority: "issuer", Kind: "approval", ID: "decision", URI: synthetic}}
	_, err := s.Call(reviewer, "review_decide", args)
	code(t, err, "validation_error")
	if strings.Contains(err.Error(), synthetic) {
		t.Fatal("approval error leaked value")
	}
	raw, _ := json.Marshal(args)
	var decoded Args
	err = Decode(raw, &decoded)
	code(t, err, "validation_error")
	got := callRecord(t, s, writer, "record_get", Args{ID: r.ID, Publication: "draft"})
	if got.RevisionID != r.RevisionID || got.Review.Approval != nil {
		t.Fatal("rejected approval changed record")
	}
}

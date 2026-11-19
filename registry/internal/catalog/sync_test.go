package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
)

func importPlan(t *testing.T, s *Service, request ImportRequest) ImportPlan {
	t.Helper()
	v, e := s.Call(writer, "import_prepare", Args{Import: &request, IdempotencyKey: newID()})
	if e != nil {
		t.Fatal(e)
	}
	return v.(ImportPlan)
}
func commitArgs(p ImportPlan, key string) Args {
	return Args{PlanID: p.ID, Hash: p.Digest, SourceDigest: p.SourceDigest, IdempotencyKey: key}
}
func batch(cp, expected string, ids ...string) ImportRequest {
	r := ImportRequest{Connector: "fixture", SourceAuthority: "fixture", MappingVersion: "fixture/1", Checkpoint: cp, ExpectedCheckpoint: expected}
	for _, id := range ids {
		r.Entries = append(r.Entries, ImportEntry{SourceKey: id, SourceRevision: cp, ExpectedSourceRevision: expected, Record: input(id)})
	}
	return r
}
func TestImportAtomicReplayDriftAndRollback(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	p := importPlan(t, s, batch("one", "", "a", "b"))
	if p.Counts.Created != 2 {
		t.Fatal(p)
	}
	v, e := s.Call(writer, "import_commit", commitArgs(p, "first"))
	if e != nil {
		t.Fatal(e)
	}
	if v.(ImportResult).Counts.Created != 2 {
		t.Fatal(v)
	}
	v, e = s.Call(writer, "import_commit", commitArgs(p, "first"))
	if e != nil || !v.(ImportResult).Replayed {
		t.Fatal(v, e)
	}
	bad := commitArgs(p, "first")
	bad.SourceDigest = "changed"
	_, e = s.Call(writer, "import_commit", bad)
	code(t, e, "version_conflict")
	p = importPlan(t, s, batch("two", "one", "a", "b"))
	if p.Counts.Unchanged != 2 {
		t.Fatal(p)
	}
	a := callRecord(t, s, writer, "record_get", Args{ID: "project:a", Publication: "draft"})
	changed := input("a")
	changed.Summary = "Registry edit"
	callRecord(t, s, writer, "record_update", Args{ID: a.ID, Version: a.VersionToken, Record: changed})
	_, e = s.Call(writer, "import_commit", commitArgs(p, "stale"))
	code(t, e, "version_conflict")
	r := batch("two", "one", "a", "b")
	r.Entries[0].Record.Summary = "Source edit"
	p = importPlan(t, s, r)
	if p.Counts.Rejected != 1 || p.Items[0].Reason != "both_sides_changed" {
		t.Fatal(p)
	}
	_, e = s.Call(writer, "import_commit", commitArgs(p, "conflict"))
	code(t, e, "validation_error")
	cp, e := sourceCheckpoint(context.Background(), s.DB, "registry", "fixture")
	if e != nil || cp != "one" {
		t.Fatal(cp, e)
	}
	// Failure late in save rolls back the earlier record, source rows and checkpoint.
	r = batch("bad", "one", "c", "d")
	r.Entries[1].Record.Key = "c"
	p = importPlan(t, s, r)
	_, e = s.Call(writer, "import_commit", commitArgs(p, "rollback"))
	code(t, e, "version_conflict")
	_, e = s.Call(writer, "record_get", Args{ID: "project:c", Publication: "draft"})
	code(t, e, "not_found")
	var n int
	if e = s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM import_sources WHERE source_key='c'`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestImportIdentityAuthorityAndRejections(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := batch("one", "", "a")
	r.Entries = append(r.Entries, r.Entries[0])
	p := importPlan(t, s, r)
	if p.Counts.Rejected != 1 {
		t.Fatal(p)
	}
	_, e := s.Call(writer, "import_commit", commitArgs(p, "reject"))
	code(t, e, "validation_error")
	p = importPlan(t, s, batch("one", "", "a"))
	_, e = s.Call(caller("other", "registry.write"), "import_commit", commitArgs(p, "steal"))
	code(t, e, "not_found")
	_, e = s.Call(caller("reader", "registry.read"), "import_prepare", Args{Import: &r})
	code(t, e, "forbidden")
	_, e = s.Call(writer, "import_commit", Args{PlanID: p.ID, Hash: p.Digest, SourceDigest: "drift", IdempotencyKey: "source-drift"})
	code(t, e, "version_conflict")
	if _, e = s.Call(writer, "import_commit", commitArgs(p, "good")); e != nil {
		t.Fatal(e)
	}
	r = batch("two", "one", "a")
	r.Entries[0].SourceKey = "new-key"
	p = importPlan(t, s, r)
	if p.Counts.Rejected != 1 {
		t.Fatal("stable identity stolen", p)
	}
}
func TestChangesPublishedOnlyPagingAndIdleResume(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("a"))
	b := create(t, s, input("b"))
	v, e := s.Call(writer, "changes_list", Args{Query: Query{Limit: 1}})
	if e != nil {
		t.Fatal(e)
	}
	idle := v.(Changes)
	if len(idle.Changes) != 0 || idle.Cursor == "" {
		t.Fatal(idle)
	}
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: a.ID, Revision: a.RevisionID}, Candidate{ID: b.ID, Revision: b.RevisionID}))
	publish(t, s, ap, "pub")
	v, e = s.Call(writer, "changes_list", Args{Query: Query{Cursor: idle.Cursor, Limit: 1}})
	if e != nil {
		t.Fatal(e)
	}
	first := v.(Changes)
	if len(first.Changes) != 1 || !first.HasMore {
		t.Fatal(first)
	}
	v, e = s.Call(writer, "changes_list", Args{Query: Query{Cursor: first.Cursor, Limit: 1}})
	if e != nil {
		t.Fatal(e)
	}
	last := v.(Changes)
	if len(last.Changes) != 1 || last.HasMore || last.Changes[0].Sequence <= first.Changes[0].Sequence {
		t.Fatal(last)
	}
	_, e = s.Call(reviewer, "changes_list", Args{Query: Query{Cursor: last.Cursor}})
	code(t, e, "validation_error")
	callRecord(t, s, writer, "record_archive", Args{ID: a.ID, Version: a.VersionToken})
	v, e = s.Call(writer, "changes_list", Args{Query: Query{Cursor: last.Cursor}})
	if e != nil {
		t.Fatal(e)
	}
	if v.(Changes).Changes[0].Record.Lifecycle != "archived" {
		t.Fatal(v)
	}
}

type fixtureConnector struct {
	request ImportRequest
	reads   int
	mutate  bool
}

func (c *fixtureConnector) ID() string { return "fixture" }
func (c *fixtureConnector) Snapshot(_ context.Context, _ string) (ImportRequest, error) {
	c.reads++
	r := c.request
	if c.mutate && c.reads > 1 {
		r.Checkpoint = "changed"
	}
	return r, nil
}
func TestTemporaryConnectorRerunAndSourceConflict(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	c := &fixtureConnector{request: batch("one", "", "a")}
	r, e := RunConnector(writer, s.Call, c)
	if e != nil || r.Counts.Created != 1 {
		t.Fatal(r, e)
	}
	r, e = RunConnector(writer, s.Call, c)
	if e != nil || r.Counts.Unchanged != 1 {
		t.Fatal(r, e)
	}
	c = &fixtureConnector{request: batch("two", "one", "b"), mutate: true}
	_, e = RunConnector(writer, s.Call, c)
	code(t, e, "version_conflict")
	_, e = s.Call(writer, "record_get", Args{ID: "project:b", Publication: "draft"})
	code(t, e, "not_found")
}
func TestImportForwardReferencesAndConcurrentReplay(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	request := batch("forward", "", "a", "b")
	request.Entries[0].Record.Payload = json.RawMessage(`{"schema":"project/1","id":"a","name":"a","class":2,"lifecycle":"active","visibility":"public","owner":"user:test","interfaces":[{"type":"cli","auth":"none"}],"edges":[{"type":"consumes","to":"project:b"}]}`)
	p := importPlan(t, s, request)
	results := make(chan ImportResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.Call(writer, "import_commit", commitArgs(p, "concurrent"))
			errs <- e
			if e == nil {
				results <- v.(ImportResult)
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
	a, b := <-results, <-results
	if a.PlanID != b.PlanID || a.Replayed == b.Replayed || a.Counts.Created != 2 {
		t.Fatal(a, b)
	}
	var n int
	if e := s.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM revisions`).Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	// Invalid final graph causes no partial creation or checkpoint advancement.
	request = batch("missing", "forward", "c")
	request.Entries[0].Record.Payload = json.RawMessage(`{"schema":"project/1","id":"c","name":"c","class":2,"lifecycle":"active","visibility":"public","owner":"user:test","interfaces":[{"type":"cli","auth":"none"}],"edges":[{"type":"consumes","to":"project:missing"}]}`)
	p = importPlan(t, s, request)
	_, e := s.Call(writer, "import_commit", commitArgs(p, "missing"))
	code(t, e, "validation_error")
	_, e = s.Call(writer, "record_get", Args{ID: "project:c", Publication: "draft"})
	code(t, e, "not_found")
	checkpoint, e := sourceCheckpoint(context.Background(), s.DB, "registry", "fixture")
	if e != nil || checkpoint != "forward" {
		t.Fatal(checkpoint, e)
	}
}

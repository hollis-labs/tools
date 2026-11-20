package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/store"
	kinds "github.com/hollis-labs/tools/registry/contracts"
)

var dispatcher = caller("writer", "registry.read", "registry.write", "registry.workstream.ensure")
var launcher = caller("writer", "registry.read", "registry.write", "registry.correlation.register")

func ensured(t *testing.T, s *Service, ctx context.Context, op string, a Args) MintResult {
	t.Helper()
	v, e := s.Call(ctx, op, a)
	if e != nil {
		t.Fatal(e)
	}
	return v.(MintResult)
}
func ensureArgs(key, retry string) Args { return Args{Key: key, IdempotencyKey: retry} }
func launchArgs(session, run, retry string) Args {
	return Args{LaunchFacts: &LaunchFacts{URN: "agent:test", Session: session, RunID: run}, IdempotencyKey: retry}
}
func TestMintConcurrentReplayAndDistinctRetryKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s := openTest(t, path)
	other := openTest(t, path)
	const n = 16
	var wg sync.WaitGroup
	results := make(chan MintResult, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := s
			if i%2 == 1 {
				db = other
			}
			retry := fmt.Sprintf("r%d", i/2)
			v, e := db.Call(dispatcher, "workstream_ensure", ensureArgs("same", retry))
			if e != nil {
				errs <- e
				return
			}
			results <- v.(MintResult)
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	close(results)
	id := ""
	for v := range results {
		if id == "" {
			id = v.ID
		}
		if v.ID != id {
			t.Fatal("concurrent duplicate identity")
		}
	}
	if len(id) != len("workstream:")+26 || !strings.HasPrefix(id, "workstream:") {
		t.Fatal(id)
	}
	a := ensureArgs("same", "lost-response")
	one := ensured(t, s, dispatcher, "workstream_ensure", a)
	two := ensured(t, other, dispatcher, "workstream_ensure", a)
	if one != two || one.ID != id {
		t.Fatal("lost-response receipt changed")
	}
	a.Name = "changed"
	_, e := s.Call(dispatcher, "workstream_ensure", a)
	code(t, e, "version_conflict")
	_, e = s.Call(dispatcher, "workstream_ensure", Args{Key: "same", Name: "changed", IdempotencyKey: "fresh"})
	code(t, e, "version_conflict")
}
func TestMintIsolationAndReceiptExpiry(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	base := ensured(t, s, dispatcher, "workstream_ensure", ensureArgs("same", "same"))
	other := ensured(t, s, caller("other", "registry.write", "registry.workstream.ensure"), "workstream_ensure", ensureArgs("same", "same"))
	project := ensureArgs("same", "project")
	project.ProjectURNs = []string{"project:one"}
	p := ensured(t, s, dispatcher, "workstream_ensure", project)
	key := ensured(t, s, dispatcher, "workstream_ensure", ensureArgs("different", "key"))
	ids := map[string]bool{base.ID: true, other.ID: true, p.ID: true, key.ID: true}
	if len(ids) != 4 {
		t.Fatal("authority/project/key isolation failed")
	}
	s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	replay := ensured(t, s, dispatcher, "workstream_ensure", ensureArgs("same", "same"))
	if replay.ID != base.ID || replay.Created {
		t.Fatal("receipt expiry lost durable mapping")
	}
}
func TestMintClosedDeletedPermanently(t *testing.T) {
	for _, mode := range []string{"closed", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "db"))
			a := ensureArgs("permanent", "original")
			v := ensured(t, s, dispatcher, "workstream_ensure", a)
			r := callRecord(t, s, writer, "record_get", Args{ID: v.ID, Publication: "draft"})
			if mode == "closed" {
				var w kinds.Workstream
				_ = json.Unmarshal(r.Payload, &w)
				w.Status = "closed"
				raw, _ := json.Marshal(w)
				r = callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: &Input{Schema: r.Schema, Key: r.Key, Payload: raw}})
				// Even an explicit generic status rewrite cannot reopen the mint mapping.
				w.Status = "active"
				raw, _ = json.Marshal(w)
				_ = callRecord(t, s, writer, "record_update", Args{ID: r.ID, Version: r.VersionToken, Record: &Input{Schema: r.Schema, Key: r.Key, Payload: raw}})
			} else {
				r = callRecord(t, s, writer, "record_archive", Args{ID: r.ID, Version: r.VersionToken})
				_ = callRecord(t, s, writer, "record_delete", Args{ID: r.ID, Version: r.VersionToken})
			}
			for _, retry := range []string{"original", "new"} {
				a.IdempotencyKey = retry
				_, e := s.Call(dispatcher, "workstream_ensure", a)
				code(t, e, "workstream_closed")
			}
			s.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
			_, e := s.Call(dispatcher, "workstream_ensure", a)
			code(t, e, "workstream_closed")
		})
	}
}
func TestMintRollbackAndRetry(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	_, e := s.DB.ExecContext(context.Background(), `CREATE TRIGGER reject_mint_receipt BEFORE INSERT ON mint_receipts BEGIN SELECT RAISE(ABORT,'test receipt failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	a := ensureArgs("rollback", "retry")
	_, e = s.Call(dispatcher, "workstream_ensure", a)
	if e == nil {
		t.Fatal("injected failure ignored")
	}
	for _, table := range []string{"records", "revisions", "minted_identities", "mint_receipts"} {
		var count int
		if e = s.DB.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); e != nil || count != 0 {
			t.Fatalf("%s partial commit %d %v", table, count, e)
		}
	}
	_, e = s.DB.ExecContext(context.Background(), `DROP TRIGGER reject_mint_receipt`)
	if e != nil {
		t.Fatal(e)
	}
	v := ensured(t, s, dispatcher, "workstream_ensure", a)
	if !v.Created {
		t.Fatal("rolled back identity survived")
	}
	if ensured(t, s, dispatcher, "workstream_ensure", a) != v {
		t.Fatal("retry receipt changed")
	}
}
func TestMintAuthorizationAndInjection(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	for _, ctx := range []context.Context{writer, caller("reader", "registry.workstream.ensure", "registry.write"), caller("writer", "registry.workstream.ensure"), launcher, context.Background()} {
		_, e := s.Call(ctx, "workstream_ensure", ensureArgs("k", "r"))
		code(t, e, "forbidden")
	}
	a := ensureArgs("k", "r")
	a.ID = "workstream:injected"
	_, e := s.Call(dispatcher, "workstream_ensure", a)
	code(t, e, "validation_error")
	for _, op := range []string{"workstream_ensure", "correlation_register"} {
		for _, body := range []string{`{"key":"k","id":"x"}`, `{"key":"k","authority":"spoof"}`, `{"urn":"a","session":"s","run_id":"r","authority":"spoof"}`, `{"urn":"a","session":"s","run_id":"r","id":"x"}`} {
			var got Args
			if e := decodeMint([]byte(body), op, &got); e == nil {
				t.Fatalf("%s accepted %s", op, body)
			}
		}
	}
	h := Handler(s.Call)
	for _, tc := range []struct{ path, body string }{{"/v1/workstreams/ensure", `{"key":"http"}`}, {"/v1/correlations/register", `{"urn":"a","session":"s","run_id":"r"}`}} {
		req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		req = req.WithContext(dispatcher)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "Idempotency-Key") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/v1/workstreams/ensure", strings.NewReader(`{"key":"http"}`))
	req.Header.Set("Idempotency-Key", "http-key")
	req = req.WithContext(dispatcher)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
func TestCorrelationUniquenessAndConditionalEnrichment(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := launchArgs("session", "run", "retry")
	v := ensured(t, s, launcher, "correlation_register", a)
	if ensured(t, s, launcher, "correlation_register", a) != v {
		t.Fatal("replay changed")
	}
	a.IdempotencyKey = "distinct"
	if ensured(t, s, launcher, "correlation_register", a).ID != v.ID {
		t.Fatal("duplicate launch")
	}
	ids := map[string]bool{v.ID: true}
	for _, tc := range []struct {
		ctx context.Context
		a   Args
	}{{launcher, launchArgs("session2", "run", "r2")}, {launcher, launchArgs("session", "run2", "r3")}, {caller("other", "registry.write", "registry.correlation.register"), launchArgs("session", "run", "retry")}} {
		ids[ensured(t, s, tc.ctx, "correlation_register", tc.a).ID] = true
	}
	if len(ids) != 4 {
		t.Fatal("correlation authority/session/run isolation failed")
	}
	r := callRecord(t, s, writer, "record_get", Args{ID: v.ID, Publication: "draft"})
	var c kinds.Correlation
	_ = json.Unmarshal(r.Payload, &c)
	c.Metadata["provider_session_id"] = "provider-late"
	c.Metadata["manifest_digest"] = "digest-late"
	raw, _ := json.Marshal(c)
	update := Args{ID: r.ID, Version: r.VersionToken, Record: &Input{Schema: r.Schema, Key: r.Key, Payload: raw}}
	next := callRecord(t, s, writer, "record_update", update)
	if next.ID != v.ID || next.VersionToken == v.VersionToken {
		t.Fatal("enrichment changed identity or not conditional")
	}
	_, e := s.Call(writer, "record_update", update)
	code(t, e, "version_conflict")
	a.IdempotencyKey = "enriched"
	a.ProviderSessionID = "provider-late"
	a.ManifestDigest = "digest-late"
	if ensured(t, s, launcher, "correlation_register", a).ID != v.ID {
		t.Fatal("enriched launch duplicated")
	}
	c.Metadata["session"] = "different"
	raw, _ = json.Marshal(c)
	update.Version = next.VersionToken
	update.Record.Payload = raw
	_, e = s.Call(writer, "record_update", update)
	code(t, e, "version_conflict")
	a.IdempotencyKey = "retry"
	a.URN = "changed"
	_, e = s.Call(launcher, "correlation_register", a)
	code(t, e, "version_conflict")
}
func TestCorrelationConcurrentAndRollback(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	var wg sync.WaitGroup
	out := make(chan MintResult, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, e := s.Call(launcher, "correlation_register", launchArgs("s", "r", fmt.Sprint(i)))
			if e != nil {
				errs <- e
			} else {
				out <- v.(MintResult)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	close(out)
	id := ""
	for v := range out {
		if id == "" {
			id = v.ID
		}
		if id != v.ID {
			t.Fatal("duplicate correlation")
		}
	}
	if !strings.HasPrefix(id, "correlation:") || len(id) != 38 {
		t.Fatal(id)
	}
	// The same transaction primitive is used for both verbs; a failed receipt
	// must roll back correlation revisions as well as the uniqueness row.
	e := s.DB.WriteTx(context.Background(), func(ctx context.Context, tx store.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TRIGGER reject_receipt BEFORE INSERT ON mint_receipts BEGIN SELECT RAISE(ABORT,'test'); END`)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Call(launcher, "correlation_register", launchArgs("rollback", "run", "rollback"))
	if e == nil {
		t.Fatal("failure ignored")
	}
	var count int
	e = s.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM minted_identities WHERE domain='rollback'`).Scan(&count)
	if e != nil || count != 0 {
		t.Fatal("partial mapping", count, e)
	}
}

func TestCorrelationAllLaunchFactsFitSchemaOne(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := launchArgs("session", "run", "all")
	a.TaskID = "task:one"
	a.WorkstreamID = "workstream:one"
	a.TokenJTI = "opaque-jti"
	a.ManifestDigest = strings.Repeat("a", 64)
	a.ExportReceiptID = "receipt:one"
	a.ActivitySHA256 = strings.Repeat("b", 64)
	a.ProviderSessionID = "provider:one"
	a.Traceparent = "00-00000000000000000000000000000001-0000000000000001-01"
	for name, value := range factsMap(a.LaunchFacts) {
		raw, _ := json.Marshal(map[string]any{"schema": "correlation/1", "metadata": map[string]any{name: value}})
		if err := kinds.ValidateNoSecretsJSON(raw); err != nil {
			t.Errorf("field %s refused by shared admission: %v", name, err)
		}
	}
	v := ensured(t, s, launcher, "correlation_register", a)
	r := callRecord(t, s, caller("agency-reader", "registry.read"), "record_get", Args{ID: v.ID, Publication: "draft"})
	if r.Schema != "correlation/1" {
		t.Fatal(r.Schema)
	}
	if e := kinds.ValidateJSON("correlation/1", r.Payload); e != nil {
		t.Fatal(e)
	}
	var c kinds.Correlation
	_ = json.Unmarshal(r.Payload, &c)
	if len(c.Metadata) != len(launchFields) || c.Trace.Traceparent != a.Traceparent || c.Links["workstream"].ID != a.WorkstreamID {
		t.Fatal("launch fields lost")
	}
}

func TestMintRegistryTransportAdapter(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	h := Handler(s.Call)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := dispatcher
		if strings.Contains(r.URL.Path, "correlations") {
			ctx = launcher
		}
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	client := Client{URL: server.URL}
	for _, tc := range []struct {
		op string
		a  Args
	}{{"workstream_ensure", ensureArgs("transport", "one")}, {"correlation_register", launchArgs("transport", "run", "two")}} {
		v, e := client.Call(context.Background(), tc.op, tc.a)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(v)
		var result MintResult
		if e = json.Unmarshal(raw, &result); e != nil || result.ID == "" {
			t.Fatal(string(raw), e)
		}
	}
}

func TestMintLostHTTPResponseRetry(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	h := Handler(s.Call)
	var once sync.Once
	var original MintResult
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lost := false
		once.Do(func() { lost = true })
		if lost {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r.WithContext(dispatcher))
			if rec.Code != 200 {
				t.Error(rec.Body.String())
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &original)
			panic(http.ErrAbortHandler)
		}
		h.ServeHTTP(w, r.WithContext(dispatcher))
	}))
	defer server.Close()
	client := Client{URL: server.URL}
	a := ensureArgs("lost-http", "stable-retry")
	_, err := client.Call(context.Background(), "workstream_ensure", a)
	code(t, err, "unavailable")
	out, err := client.Call(context.Background(), "workstream_ensure", a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var retry MintResult
	_ = json.Unmarshal(raw, &retry)
	if original.ID == "" || retry != original || !retry.Created || rowCount(t, s, "minted_identities") != 1 {
		t.Fatalf("lost response did not replay original result: %#v %#v", original, retry)
	}
}

func TestMintProjectDomainsOverlapConverge(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := ensureArgs("same-key", "multi")
	a.ProjectURNs = []string{"project:two", "project:one"}
	original := ensured(t, s, dispatcher, "workstream_ensure", a)
	a.ProjectURNs = []string{"project:one", "project:two"}
	if ensured(t, s, dispatcher, "workstream_ensure", a) != original {
		t.Fatal("project order changed replay")
	}
	for _, project := range a.ProjectURNs {
		one := ensureArgs("same-key", project)
		one.ProjectURNs = []string{project}
		v := ensured(t, s, dispatcher, "workstream_ensure", one)
		if v.ID != original.ID || v.Created {
			t.Fatal("per-project domain minted duplicate identity")
		}
	}
	separate := ensureArgs("same-key", "three")
	separate.ProjectURNs = []string{"project:three"}
	v := ensured(t, s, dispatcher, "workstream_ensure", separate)
	if v.ID == original.ID {
		t.Fatal("unrelated project not isolated")
	}
	overlap := ensureArgs("same-key", "overlap")
	overlap.ProjectURNs = []string{"project:two", "project:three"}
	_, err := s.Call(dispatcher, "workstream_ensure", overlap)
	code(t, err, "version_conflict")
	overlap.ProjectURNs = []string{"project:one", "project:new"}
	_, err = s.Call(dispatcher, "workstream_ensure", overlap)
	code(t, err, "version_conflict")
	record := callRecord(t, s, writer, "record_get", Args{ID: original.ID, Publication: "draft"})
	var w kinds.Workstream
	_ = json.Unmarshal(record.Payload, &w)
	if len(w.ProjectURNs) != 2 {
		t.Fatal("ensure changed authored projects")
	}
	record = callRecord(t, s, writer, "record_archive", Args{ID: record.ID, Version: record.VersionToken})
	for _, project := range w.ProjectURNs {
		a := ensureArgs("same-key", "closed-"+project)
		a.ProjectURNs = []string{project}
		_, err = s.Call(dispatcher, "workstream_ensure", a)
		code(t, err, "workstream_closed")
	}
}

func TestMintFixedRetentionIndependentOfMaintenance(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	start := time.Now().UTC().Add(-24 * time.Hour)
	s.now = func() time.Time { return start }
	a := ensureArgs("retention", "fixed-thirty-days")
	original := ensured(t, s, dispatcher, "workstream_ensure", a)
	// Give ordinary configurable retention a receipt it must remove. Mint
	// receipts remain in a separate table with their own fixed 30-day deadline.
	_, err := s.Call(writer, "record_create", Args{Record: input("ordinary"), IdempotencyKey: "ordinary"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(context.Background(), `UPDATE receipts SET created_at='2000-01-01T00:00:00Z'`)
	if err != nil {
		t.Fatal(err)
	}
	s.Maintenance.ReceiptTTL = time.Nanosecond
	s.reviewers["reviewer"] = "owner"
	_, err = s.Call(reviewer, "retention_run", Args{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if rowCount(t, s, "receipts") != 0 || rowCount(t, s, "mint_receipts") != 1 || rowCount(t, s, "minted_identities") != 1 {
		t.Fatal("configurable retention shortened mint retention or lost permanent mapping")
	}
	if ensured(t, s, dispatcher, "workstream_ensure", a) != original {
		t.Fatal("maintenance changed unexpired original receipt")
	}
	s.now = func() time.Time { return start.Add(31 * 24 * time.Hour) }
	expired := ensured(t, s, dispatcher, "workstream_ensure", a)
	if expired.ID != original.ID || expired.Created || rowCount(t, s, "minted_identities") != 1 {
		t.Fatal("receipt expiry reminted or lost permanent mapping")
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/redact"

	"github.com/hollis-labs/tools/conduit/internal/blob"
	"github.com/hollis-labs/tools/conduit/internal/runbook"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

// every request on a fresh connection
var noKeepAlive = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

// callers are the verified principals a test can act as; the header X-Test-Principal names one. The test identity stands
// where the identity module and the auth guard stand in the server: it verifies the caller and refuses a route whose
// scope the caller lacks (the table is the one RegisterScopes fills). The real guard is exercised by the binary test.
var callers = map[string]libauth.Principal{
	"agent":    {Verified: true, ID: "key:agent", Kind: "service", Scopes: []string{service.ScopeRun, service.ScopeRead}},
	"oper":     {Verified: true, ID: "user:oper", Kind: "user", Scopes: []string{service.ScopeApprove, service.ScopeRead}},
	"keyoper":  {Verified: true, ID: "key:keyoper", Kind: "service", Scopes: []string{service.ScopeApprove, service.ScopeRead}},
	"reader":   {Verified: true, ID: "key:reader", Kind: "service", Scopes: []string{service.ScopeRead, service.ScopeReadAll}},
	"own":      {Verified: true, ID: "key:own", Kind: "service", Scopes: []string{service.ScopeRead}},
	"runner":   {Verified: true, ID: "key:runner", Kind: "service", Scopes: []string{service.ScopeRun, service.ScopeRead}},
	"other":    {Verified: true, ID: "key:other", Kind: "service", Scopes: []string{service.ScopeRun, service.ScopeRead}},
	"admin":    {Verified: true, ID: "key:root", Kind: "service", Scopes: []string{service.ScopeAdmin, service.ScopeRun, service.ScopeRead}},
	"selfboth": {Verified: true, ID: "user:both", Kind: "user", Scopes: []string{service.ScopeRun, service.ScopeApprove, service.ScopeRead}},
}

func testIdentity(table *libauth.ScopeTable, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := callers[r.Header.Get("X-Test-Principal")]
		if !ok {
			writeErr(w, 401, "unauthenticated", "a verified caller is required")
			return
		}
		if scope, known := table.ForRoute(r.Method, r.URL.Path); !known || !p.HasScope(scope) {
			writeErr(w, 403, "forbidden", "scope required")
			return
		}
		next.ServeHTTP(w, r.WithContext(libauth.WithPrincipal(r.Context(), p)))
	})
}

type env struct {
	t    *testing.T
	svc  *service.Service
	srv  *Server
	ts   *httptest.Server
	root string
}

func setup(t *testing.T, opts ...service.Option) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bl, _ := blob.NewLocal(filepath.Join(t.TempDir(), "b"))
	return setupWith(t, service.New(st, append([]service.Option{service.WithBlobs(bl)}, opts...)...))
}

// setupWith builds an env around an existing service (shared store).
func setupWith(t *testing.T, svc *service.Service) *env {
	t.Helper()
	root := t.TempDir()
	srv, err := New(svc, Config{AllowDirs: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { srv.Run(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	table := libauth.NewScopeTable()
	srv.RegisterScopes(table)
	ts := httptest.NewServer(testIdentity(table, srv.Handler()))
	t.Cleanup(ts.Close)
	return &env{t: t, svc: svc, srv: srv, ts: ts, root: root}
}

// do sends a request as the named caller ("" sends no identity).
func (e *env) do(method, path, who string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			rd = bytes.NewReader(j)
		}
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	if who != "" {
		req.Header.Set("X-Test-Principal", who)
	}
	resp, err := noKeepAlive.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func errCode(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func TestEveryRouteHasAScopeAndNeedsACaller(t *testing.T) {
	e := setup(t)
	srv, _ := New(e.svc, Config{AllowDirs: []string{e.root}})
	table := libauth.NewScopeTable()
	srv.RegisterScopes(table)
	for _, rt := range srv.routes() {
		path := strings.NewReplacer("{id}", "x").Replace(rt.Pattern)
		if scope, known := table.ForRoute(rt.Method, path); !known || scope == "" {
			t.Errorf("%s %s has no scope", rt.Method, rt.Pattern)
		}
		if code, m := e.do(rt.Method, path, "", nil); code != 401 || errCode(m) != "unauthenticated" {
			t.Errorf("%s %s without a caller: %d %v", rt.Method, rt.Pattern, code, m)
		}
	}
	// the handler's own lock: a request that reaches it without a verified principal is refused
	req := httptest.NewRequest("GET", "/api/runs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("handler without a principal: %d", rec.Code)
	}
}

func TestScopesAreEnforced(t *testing.T) {
	e := setup(t)
	if code, m := e.do("POST", "/api/exec", "own", map[string]any{"command": "echo hi"}); code != 403 || errCode(m) != "forbidden" {
		t.Errorf("read-only caller running: %d %v", code, m)
	}
	if code, _ := e.do("GET", "/api/approvals", "agent", nil); code != 403 {
		t.Errorf("agent listing approvals: %d", code)
	}
	if code, _ := e.do("POST", "/api/runs/x/approve", "agent", nil); code != 403 {
		t.Errorf("agent approving: %d", code)
	}
	if code, _ := e.do("GET", "/api/runs", "own", nil); code != 200 {
		t.Errorf("reader listing: %d", code)
	}
}

func TestExecAllowedRunsAndIsAttributed(t *testing.T) {
	e := setup(t)
	code, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "echo hello && pwd"})
	if code != 200 || m["state"] != "done" || !strings.HasPrefix(m["stdout"].(string), "hello\n") || m["principal"] != "key:agent" {
		t.Fatalf("%d %v", code, m)
	}
	realRoot, _ := filepath.EvalSymlinks(e.root)
	if !strings.Contains(m["stdout"].(string), realRoot) {
		t.Errorf("default dir should be the first allowed root: %v", m["stdout"])
	}
}

func TestAskIs202ThenAPersonApprovesAndItRuns(t *testing.T) {
	e := setup(t)
	marker := filepath.Join(e.root, "ran")
	code, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "touch " + marker})
	if code != 202 || m["state"] != "pending" {
		t.Fatalf("%d %v", code, m)
	}
	id := m["id"].(string)
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ran while pending")
	}
	if code, p := e.do("GET", "/api/approvals", "oper", nil); code != 200 || len(p["pending"].([]any)) != 1 {
		t.Fatalf("%d %v", code, p)
	}
	if code, m := e.do("POST", "/api/runs/"+id+"/approve", "oper", nil); code != 200 || m["state"] != "approved" || m["approved_by"] != "user:oper" {
		t.Fatalf("%d %v", code, m)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, m := e.do("GET", "/api/runs/"+id, "reader", nil); m["state"] == "done" {
			if _, err := os.Stat(marker); err != nil {
				t.Error("approved run did not execute")
			}
			if code, _ := e.do("POST", "/api/runs/"+id+"/approve", "oper", nil); code != 409 {
				t.Errorf("second approve: %d", code)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("approved run never finished")
}

// An API key is never a person: it may hold the approve scope and still cannot approve or reject.
func TestAPIKeyWithApproveScopeCannotApprove(t *testing.T) {
	e := setup(t)
	_, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "touch " + filepath.Join(e.root, "x")})
	id := m["id"].(string)
	for _, what := range []string{"approve", "reject"} {
		if code, r := e.do("POST", "/api/runs/"+id+"/"+what, "keyoper", nil); code != 403 || errCode(r) != "not_a_person" {
			t.Errorf("%s by a key: %d %v", what, code, r)
		}
	}
	if _, r := e.do("GET", "/api/runs/"+id, "reader", nil); r["state"] != "pending" {
		t.Errorf("run left pending: %v", r["state"])
	}
}

func TestSelfApprovalRefusedEvenForAPerson(t *testing.T) {
	e := setup(t)
	_, m := e.do("POST", "/api/exec", "selfboth", map[string]any{"command": "touch " + filepath.Join(e.root, "x")})
	if code, r := e.do("POST", "/api/runs/"+m["id"].(string)+"/approve", "selfboth", nil); code != 403 || errCode(r) != "self_approval" {
		t.Fatalf("%d %v", code, r)
	}
}

func TestRejectAndDeny(t *testing.T) {
	e := setup(t)
	_, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "touch " + filepath.Join(e.root, "x")})
	if code, r := e.do("POST", "/api/runs/"+m["id"].(string)+"/reject", "oper", nil); code != 200 || r["state"] != "denied" {
		t.Fatalf("%d %v", code, r)
	}
	if code, r := e.do("POST", "/api/exec", "agent", map[string]any{"command": "rm -rf ~"}); code != 403 || r["state"] != "denied" {
		t.Fatalf("%d %v", code, r)
	}
}

func TestDirectoryAllowList(t *testing.T) {
	e := setup(t)
	sub := filepath.Join(e.root, "sub")
	_ = os.Mkdir(sub, 0o750)
	if code, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "pwd", "dir": sub}); code != 200 || !strings.Contains(m["stdout"].(string), "sub") {
		t.Fatalf("%d %v", code, m)
	}
	// symlink inside the root pointing out of it must not be a way out
	link := filepath.Join(e.root, "escape")
	_ = os.Symlink("/etc", link)
	for _, d := range []string{"/etc", "/", link, e.root + "/../..", "relative/dir", filepath.Join(e.root, "missing")} {
		if code, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "pwd", "dir": d}); code != 400 {
			t.Errorf("dir %q accepted: %d %v", d, code, m)
		}
	}
}

func TestRequestHygiene(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("POST", "/api/exec", "agent", `{"command":"ls","bogus":1}`); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
	if code, _ := e.do("POST", "/api/exec", "agent", `not json`); code != 400 {
		t.Errorf("bad json: %d", code)
	}
	if code, _ := e.do("POST", "/api/exec", "agent", map[string]any{}); code != 400 {
		t.Errorf("neither command nor runbook: %d", code)
	}
	if code, _ := e.do("POST", "/api/exec", "agent", map[string]any{"command": strings.Repeat("a", 2<<20)}); code != 413 {
		t.Errorf("oversized body: %d", code)
	}
	if code, _ := e.do("GET", "/api/runs/zzzz", "reader", nil); code != 404 {
		t.Errorf("missing run: %d", code)
	}
	if code, _ := e.do("POST", "/api/exec", "agent", map[string]any{"command": "ls", "sandbox": "fortress"}); code == 200 {
		t.Errorf("unknown sandbox accepted")
	}
}

func TestRateLimitIsPerPrincipal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := service.New(st)
	srv, _ := New(svc, Config{AllowDirs: []string{t.TempDir()}, RatePerMinute: 3})
	table := libauth.NewScopeTable()
	srv.RegisterScopes(table)
	ts := httptest.NewServer(testIdentity(table, srv.Handler()))
	t.Cleanup(ts.Close)
	e := &env{t: t, svc: svc, ts: ts}
	last := 0
	for i := 0; i < 5; i++ {
		last, _ = e.do("GET", "/api/runbooks", "agent", nil)
	}
	if last != 429 {
		t.Errorf("expected throttling after the limit, got %d", last)
	}
	if code, _ := e.do("GET", "/api/runbooks", "reader", nil); code != 200 {
		t.Errorf("another principal was throttled: %d", code)
	}
}

func TestOutputPagingAndCancel(t *testing.T) {
	e := setup(t)
	_, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "seq 1 2000", "max_bytes": 10})
	if m["output_truncated"] != true {
		t.Fatalf("%v", m)
	}
	_, pg := e.do("GET", "/api/runs/"+m["id"].(string)+"/output?offset=10&max_bytes=5", "reader", nil)
	if pg["returned"].(float64) != 5 || pg["more"] != true {
		t.Fatalf("%v", pg)
	}
	_, slow := e.do("POST", "/api/exec", "agent", map[string]any{"command": "sleep 30", "wait_sec": 0})
	id := slow["id"].(string)
	time.Sleep(200 * time.Millisecond)
	if code, _ := e.do("POST", "/api/runs/"+id+"/cancel", "own", nil); code != 403 {
		t.Errorf("reader (no run scope) cancel: %d", code)
	}
	// a caller that HAS conduit.run but did not submit the run must still be refused
	if code, m := e.do("POST", "/api/runs/"+id+"/cancel", "other", nil); code != 403 || errCode(m) != "forbidden" {
		t.Errorf("non-owner with conduit.run cancel: %d %v", code, m)
	}
	_, other2 := e.do("POST", "/api/exec", "agent", map[string]any{"command": "sleep 30", "wait_sec": 0})
	time.Sleep(200 * time.Millisecond)
	if code, _ := e.do("POST", "/api/runs/"+other2["id"].(string)+"/cancel", "admin", nil); code != 200 {
		t.Errorf("admin cancel of another's run: %d", code)
	}
	if code, _ := e.do("POST", "/api/runs/"+id+"/cancel", "agent", nil); code != 200 {
		t.Errorf("owner cancel: %d", code)
	}
}

func TestRunbooksOverHTTP(t *testing.T) {
	set, _ := runbook.NewSet(&runbook.Runbook{Name: "say", Argv: []string{"echo", "{{m}}"}, Verdict: "allow", Params: map[string]runbook.Param{"m": {Required: true}}})
	e := setup(t, service.WithRunbooks(set))
	if code, m := e.do("GET", "/api/runbooks", "reader", nil); code != 200 || len(m["runbooks"].([]any)) != 1 {
		t.Fatalf("%d %v", code, m)
	}
	code, m := e.do("POST", "/api/exec", "agent", map[string]any{"runbook": "say", "params": map[string]string{"m": "a; id"}})
	if code != 200 || m["stdout"] != "a; id\n" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _ := e.do("POST", "/api/exec", "agent", map[string]any{"runbook": "say", "params": map[string]string{}}); code != 400 {
		t.Errorf("missing required param: %d", code)
	}
}

// A runbook that fixes its own directory runs there, not in the default root.
func TestRunbookDirIsHonouredOverHTTP(t *testing.T) {
	fixed := t.TempDir()
	realFixed, _ := filepath.EvalSymlinks(fixed)
	set, _ := runbook.NewSet(
		&runbook.Runbook{Name: "where", Argv: []string{"pwd"}, Verdict: "allow", Dir: fixed},
		&runbook.Runbook{Name: "here", Argv: []string{"pwd"}, Verdict: "allow"},
	)
	e := setup(t, service.WithRunbooks(set))
	_, m := e.do("POST", "/api/exec", "agent", map[string]any{"runbook": "where"})
	if !strings.Contains(m["stdout"].(string), realFixed) {
		t.Errorf("runbook dir ignored: %v", m["stdout"])
	}
	realRoot, _ := filepath.EvalSymlinks(e.root)
	_, m = e.do("POST", "/api/exec", "agent", map[string]any{"runbook": "here"})
	if !strings.Contains(m["stdout"].(string), realRoot) {
		t.Errorf("runbook without dir should use the default root: %v", m["stdout"])
	}
	// a caller-supplied dir is still validated for a runbook
	if code, _ := e.do("POST", "/api/exec", "agent", map[string]any{"runbook": "here", "dir": "/etc"}); code != 400 {
		t.Errorf("caller dir outside roots accepted for a runbook: %d", code)
	}
}

// getPanics wraps a real blob store but panics on read, so a run can be recorded
// normally and the panic happens inside an HTTP handler.
type getPanics struct{ blob.Store }

func (getPanics) Get(context.Context, string) ([]byte, error) {
	panic("blob read bug, token=sk-abcdefghijklmnopqrstuvwxyz")
}

type collect struct {
	mu   sync.Mutex
	recs []panics.Record
}

func (c *collect) Report(r panics.Record) { c.mu.Lock(); c.recs = append(c.recs, r); c.mu.Unlock() }
func (c *collect) n() int                 { c.mu.Lock(); defer c.mu.Unlock(); return len(c.recs) }

func TestHandlerPanicBecomesA500WithARefAndTheServerSurvives(t *testing.T) {
	rep := &collect{}
	panics.SetDefault(panics.Config{Reporter: rep, Redact: redact.Default().Text})
	t.Cleanup(func() { panics.SetDefault(panics.Config{Reporter: nopReporter{}}) })
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blobReal, _ := blob.NewLocal(filepath.Join(t.TempDir(), "b"))
	e := setupWith(t, service.New(st, service.WithBlobs(blobReal)))
	_, m := e.do("POST", "/api/exec", "agent", map[string]any{"command": "echo hi", "max_bytes": 1})
	id := m["id"].(string)

	e2 := setupWith(t, service.New(st, service.WithBlobs(getPanics{blobReal})))
	code, body := e2.do("GET", "/api/runs/"+id+"/output", "reader", nil)
	if code != 500 || errCode(body) != "internal" {
		t.Fatalf("%d %v", code, body)
	}
	msg := body["error"].(map[string]any)["message"].(string)
	ref, _ := body["error"].(map[string]any)["ref"].(string)
	if !strings.HasPrefix(ref, "pnc_") || !strings.Contains(msg, ref) || strings.Contains(msg, "sk-") || strings.Contains(msg, "blob read bug") {
		t.Fatalf("response must carry the ref and no panic text: %v", body)
	}
	if rep.n() == 0 {
		t.Error("the panic was not reported")
	}
	// the very next request is served normally
	if code, _ := e2.do("GET", "/api/runs", "reader", nil); code != 200 {
		t.Errorf("authenticated route broken after a panic: %d", code)
	}
}

type nopReporter struct{}

func (nopReporter) Report(panics.Record) {}

func TestReadScopesOwnVersusAll(t *testing.T) {
	e := setup(t)
	_, mine := e.do("POST", "/api/exec", "runner", map[string]any{"command": "echo mine"})
	_, theirs := e.do("POST", "/api/exec", "agent", map[string]any{"command": "echo theirs"})

	// the runner sees its own run, and only that
	if code, m := e.do("GET", "/api/runs/"+mine["id"].(string), "runner", nil); code != 200 || m["id"] != mine["id"] {
		t.Fatalf("own run: %d %v", code, m)
	}
	_, list := e.do("GET", "/api/runs", "runner", nil)
	runs := list["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["id"] != mine["id"] {
		t.Fatalf("list leaked other principals' runs: %v", list)
	}
	// another principal's run is 404, not 403: its existence is not revealed
	id := theirs["id"].(string)
	for _, path := range []string{"/api/runs/" + id, "/api/runs/" + id + "/output"} {
		if code, m := e.do("GET", path, "runner", nil); code != 404 {
			t.Errorf("%s: %d %v", path, code, m)
		}
	}
	// a caller with only conduit.read and no runs of its own sees nothing
	if _, l := e.do("GET", "/api/runs", "own", nil); len(l["runs"].([]any)) != 0 {
		t.Errorf("%v", l)
	}
	// conduit.read.all sees everything
	if _, l := e.do("GET", "/api/runs", "reader", nil); len(l["runs"].([]any)) != 2 {
		t.Errorf("conduit.read.all should see all: %v", l)
	}
	if code, _ := e.do("GET", "/api/runs/"+id+"/output", "reader", nil); code != 200 {
		t.Errorf("read.all output: %d", code)
	}
	// but read alone cannot run
	if code, _ := e.do("POST", "/api/exec", "own", map[string]any{"command": "echo x"}); code != 403 {
		t.Errorf("read-only caller ran a command: %d", code)
	}
}

// The old /v1 paths and unknown /api paths answer a JSON 404 from the route set itself.
func TestOldAndUnknownPathsAreJSON404(t *testing.T) {
	e := setup(t)
	mux := http.NewServeMux()
	mux.Handle("/api/", e.srv.Handler())
	mux.Handle("/v1/", NotFound())
	h := httptest.NewServer(mux)
	defer h.Close()
	for _, path := range []string{"/v1/runs", "/v1/exec", "/v1/check", "/api/nope", "/api/runs/x/nope"} {
		resp, err := noKeepAlive.Get(h.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		ct := resp.Header.Get("Content-Type")
		_ = resp.Body.Close()
		if resp.StatusCode != 404 || errCode(m) != "not_found" || !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: %d %v %s", path, resp.StatusCode, m, ct)
		}
	}
}

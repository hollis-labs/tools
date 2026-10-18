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
	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/tools/conduit/internal/blob"
	"github.com/hollis-labs/tools/conduit/internal/runbook"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

// every request on a fresh connection, so per-address logic cannot hide behind a reused port
var noKeepAlive = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

type env struct {
	t      *testing.T
	svc    *service.Service
	ts     *httptest.Server
	root   string
	agent  string // exec.run + exec.read
	oper   string // exec.approve + exec.read
	reader string // exec.read
}

func setup(t *testing.T, opts ...service.Option) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bl, _ := blob.NewLocal(filepath.Join(t.TempDir(), "b"))
	svc := service.New(st, append([]service.Option{service.WithBlobs(bl)}, opts...)...)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h, err := New(ctx, svc, Config{AllowDirs: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	mk := func(name string, scopes ...string) string {
		sec, _, err := svc.CreateToken(name, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return sec
	}
	return &env{t: t, svc: svc, ts: ts, root: root,
		agent: mk("agent", service.ScopeRun, service.ScopeRead), oper: mk("oper", service.ScopeApprove, service.ScopeRead),
		reader: mk("reader", service.ScopeRead)}
}

func (e *env) do(method, path, token string, body any) (int, map[string]any) {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func TestHealthzNeedsNoAuthEverythingElseDoes(t *testing.T) {
	e := setup(t)
	if code, m := e.do("GET", "/healthz", "", nil); code != 200 || m["ok"] != true {
		t.Fatalf("%d %v", code, m)
	}
	for _, p := range []string{"/v1/runs", "/v1/runbooks", "/v1/approvals"} {
		if code, m := e.do("GET", p, "", nil); code != 401 || errCode(m) != "unauthenticated" {
			t.Errorf("%s without token: %d %v", p, code, m)
		}
		if code, _ := e.do("GET", p, "cdt_bogus", nil); code != 401 {
			t.Errorf("%s bad token: %d", p, code)
		}
	}
}

func TestScopesAreEnforced(t *testing.T) {
	e := setup(t)
	if code, m := e.do("POST", "/v1/exec", e.reader, map[string]any{"command": "echo hi"}); code != 403 || errCode(m) != "forbidden" {
		t.Errorf("reader running: %d %v", code, m)
	}
	if code, _ := e.do("GET", "/v1/approvals", e.agent, nil); code != 403 {
		t.Errorf("agent listing approvals: %d", code)
	}
	if code, _ := e.do("POST", "/v1/runs/x/approve", e.agent, nil); code != 403 {
		t.Errorf("agent approving: %d", code)
	}
	if code, _ := e.do("GET", "/v1/runs", e.reader, nil); code != 200 {
		t.Errorf("reader listing: %d", code)
	}
}

func TestHostScopedTokenOnlyWorksForThatHost(t *testing.T) {
	e := setup(t)
	here, _, _ := e.svc.CreateToken("here", []string{"exec.run:" + e.svc.Host(), "exec.read:" + e.svc.Host()})
	elsewhere, _, _ := e.svc.CreateToken("elsewhere", []string{"exec.run:some-other-host"})
	if code, _ := e.do("POST", "/v1/exec", here, map[string]any{"command": "echo hi"}); code != 200 {
		t.Errorf("own host: %d", code)
	}
	if code, _ := e.do("POST", "/v1/exec", elsewhere, map[string]any{"command": "echo hi"}); code != 403 {
		t.Errorf("other host: %d", code)
	}
}

func TestExecAllowedRunsAndIsAttributed(t *testing.T) {
	e := setup(t)
	code, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "echo hello && pwd"})
	if code != 200 || m["state"] != "done" || !strings.HasPrefix(m["stdout"].(string), "hello\n") || m["principal"] != "token:agent" {
		t.Fatalf("%d %v", code, m)
	}
	realRoot, _ := filepath.EvalSymlinks(e.root)
	if !strings.Contains(m["stdout"].(string), realRoot) {
		t.Errorf("default dir should be the first allowed root: %v", m["stdout"])
	}
}

func TestAskIs202ThenOperatorApprovesAndItRuns(t *testing.T) {
	e := setup(t)
	marker := filepath.Join(e.root, "ran")
	code, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "touch " + marker})
	if code != 202 || m["state"] != "pending" {
		t.Fatalf("%d %v", code, m)
	}
	id := m["id"].(string)
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ran while pending")
	}
	if code, p := e.do("GET", "/v1/approvals", e.oper, nil); code != 200 || len(p["pending"].([]any)) != 1 {
		t.Fatalf("%d %v", code, p)
	}
	if code, m := e.do("POST", "/v1/runs/"+id+"/approve", e.oper, nil); code != 200 || m["state"] != "approved" || m["approved_by"] != "token:oper" {
		t.Fatalf("%d %v", code, m)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, m := e.do("GET", "/v1/runs/"+id, e.reader, nil); m["state"] == "done" {
			if _, err := os.Stat(marker); err != nil {
				t.Error("approved run did not execute")
			}
			if code, _ := e.do("POST", "/v1/runs/"+id+"/approve", e.oper, nil); code != 409 {
				t.Errorf("second approve: %d", code)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("approved run never finished")
}

func TestSelfApprovalRefusedEvenWithBothScopes(t *testing.T) {
	e := setup(t)
	both, _, _ := e.svc.CreateToken("both", []string{service.ScopeRun, service.ScopeApprove})
	_, m := e.do("POST", "/v1/exec", both, map[string]any{"command": "touch " + filepath.Join(e.root, "x")})
	if code, r := e.do("POST", "/v1/runs/"+m["id"].(string)+"/approve", both, nil); code != 403 || errCode(r) != "self_approval" {
		t.Fatalf("%d %v", code, r)
	}
}

func TestRejectAndDeny(t *testing.T) {
	e := setup(t)
	_, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "touch " + filepath.Join(e.root, "x")})
	if code, r := e.do("POST", "/v1/runs/"+m["id"].(string)+"/reject", e.oper, nil); code != 200 || r["state"] != "denied" {
		t.Fatalf("%d %v", code, r)
	}
	if code, r := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "rm -rf ~"}); code != 403 || r["state"] != "denied" {
		t.Fatalf("%d %v", code, r)
	}
}

func TestDirectoryAllowList(t *testing.T) {
	e := setup(t)
	sub := filepath.Join(e.root, "sub")
	_ = os.Mkdir(sub, 0o750)
	if code, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "pwd", "dir": sub}); code != 200 || !strings.Contains(m["stdout"].(string), "sub") {
		t.Fatalf("%d %v", code, m)
	}
	// symlink inside the root pointing out of it must not be a way out
	link := filepath.Join(e.root, "escape")
	_ = os.Symlink("/etc", link)
	for _, d := range []string{"/etc", "/", link, e.root + "/../..", "relative/dir", filepath.Join(e.root, "missing")} {
		if code, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "pwd", "dir": d}); code != 400 {
			t.Errorf("dir %q accepted: %d %v", d, code, m)
		}
	}
}

func TestRequestHygiene(t *testing.T) {
	e := setup(t)
	if code, _ := e.do("POST", "/v1/exec", e.agent, `{"command":"ls","bogus":1}`); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
	if code, _ := e.do("POST", "/v1/exec", e.agent, `not json`); code != 400 {
		t.Errorf("bad json: %d", code)
	}
	if code, _ := e.do("POST", "/v1/exec", e.agent, map[string]any{}); code != 400 {
		t.Errorf("neither command nor runbook: %d", code)
	}
	if code, _ := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": strings.Repeat("a", 2<<20)}); code != 413 {
		t.Errorf("oversized body: %d", code)
	}
	if code, _ := e.do("GET", "/v1/runs/zzzz", e.reader, nil); code != 404 {
		t.Errorf("missing run: %d", code)
	}
	if code, _ := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "ls", "sandbox": "fortress"}); code == 200 {
		t.Errorf("unknown sandbox accepted")
	}
}

func TestFailedAuthThrottle(t *testing.T) {
	e := setup(t)
	last := 0
	for i := 0; i < 14; i++ {
		last, _ = e.do("GET", "/v1/runs", "cdt_wrong", nil)
	}
	if last != 429 {
		t.Errorf("expected throttling after repeated failures, got %d", last)
	}
	// the throttle holds off guessers, not legitimate clients on the same address
	if code, _ := e.do("GET", "/v1/runs", e.reader, nil); code != 200 {
		t.Errorf("valid token locked out by bad guesses from the same address: %d", code)
	}
}

func TestOutputPagingAndCancel(t *testing.T) {
	e := setup(t)
	_, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "seq 1 2000", "max_bytes": 10})
	if m["output_truncated"] != true {
		t.Fatalf("%v", m)
	}
	_, pg := e.do("GET", "/v1/runs/"+m["id"].(string)+"/output?offset=10&max_bytes=5", e.reader, nil)
	if pg["returned"].(float64) != 5 || pg["more"] != true {
		t.Fatalf("%v", pg)
	}
	_, slow := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "sleep 30", "wait_sec": 0})
	id := slow["id"].(string)
	time.Sleep(200 * time.Millisecond)
	if code, _ := e.do("POST", "/v1/runs/"+id+"/cancel", e.reader, nil); code != 403 {
		t.Errorf("reader (no run scope) cancel: %d", code)
	}
	// a token that HAS exec.run but did not submit the run must still be refused
	other, _, _ := e.svc.CreateToken("other", []string{service.ScopeRun, service.ScopeRead})
	if code, m := e.do("POST", "/v1/runs/"+id+"/cancel", other, nil); code != 403 || errCode(m) != "forbidden" {
		t.Errorf("non-owner with exec.run cancel: %d %v", code, m)
	}
	admin, _, _ := e.svc.CreateToken("root", []string{service.ScopeAdmin})
	_, other2 := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "sleep 30", "wait_sec": 0})
	time.Sleep(200 * time.Millisecond)
	if code, _ := e.do("POST", "/v1/runs/"+other2["id"].(string)+"/cancel", admin, nil); code != 200 {
		t.Errorf("admin cancel of another's run: %d", code)
	}
	if code, _ := e.do("POST", "/v1/runs/"+id+"/cancel", e.agent, nil); code != 200 {
		t.Errorf("owner cancel: %d", code)
	}
}

func TestRunbooksOverHTTP(t *testing.T) {
	set, _ := runbook.NewSet(&runbook.Runbook{Name: "say", Argv: []string{"echo", "{{m}}"}, Verdict: "allow", Params: map[string]runbook.Param{"m": {Required: true}}})
	e := setup(t, service.WithRunbooks(set))
	if code, m := e.do("GET", "/v1/runbooks", e.reader, nil); code != 200 || len(m["runbooks"].([]any)) != 1 {
		t.Fatalf("%d %v", code, m)
	}
	code, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"runbook": "say", "params": map[string]string{"m": "a; id"}})
	if code != 200 || m["stdout"] != "a; id\n" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _ := e.do("POST", "/v1/exec", e.agent, map[string]any{"runbook": "say", "params": map[string]string{}}); code != 400 {
		t.Errorf("missing required param: %d", code)
	}
}

func TestListenRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.5:0", ":0"} {
		if ln, err := (Listen{Addr: addr}).Bind(); err == nil {
			_ = ln.Close()
			t.Errorf("%s bound without -unsafe-bind", addr)
		}
	}
	ln, err := (Listen{Addr: "127.0.0.1:0"}).Bind()
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	sock := filepath.Join(t.TempDir(), "c.sock")
	ln, err = (Listen{Unix: sock}).Bind()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if fi, _ := os.Stat(sock); fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v", fi.Mode().Perm())
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
	_, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"runbook": "where"})
	if !strings.Contains(m["stdout"].(string), realFixed) {
		t.Errorf("runbook dir ignored: %v", m["stdout"])
	}
	realRoot, _ := filepath.EvalSymlinks(e.root)
	_, m = e.do("POST", "/v1/exec", e.agent, map[string]any{"runbook": "here"})
	if !strings.Contains(m["stdout"].(string), realRoot) {
		t.Errorf("runbook without dir should use the default root: %v", m["stdout"])
	}
	// a caller-supplied dir is still validated for a runbook
	if code, _ := e.do("POST", "/v1/exec", e.agent, map[string]any{"runbook": "here", "dir": "/etc"}); code != 400 {
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
	svc := service.New(st, service.WithBlobs(blobReal))
	e := setupWith(t, svc, "a-")
	_, m := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "echo hi", "max_bytes": 1})
	id := m["id"].(string)

	svcPanicky := service.New(st, service.WithBlobs(getPanics{blobReal}))
	e2 := setupWith(t, svcPanicky, "b-")
	code, body := e2.do("GET", "/v1/runs/"+id+"/output", e2.reader, nil)
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
	if code, _ := e2.do("GET", "/healthz", "", nil); code != 200 {
		t.Errorf("server did not survive: %d", code)
	}
	if code, _ := e2.do("GET", "/v1/runs", e2.reader, nil); code != 200 {
		t.Errorf("authenticated route broken after a panic: %d", code)
	}
}

type nopReporter struct{}

func (nopReporter) Report(panics.Record) {}

// setupWith builds an env around an existing service (shared store).
func setupWith(t *testing.T, svc *service.Service, prefix string) *env {
	t.Helper()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h, err := New(ctx, svc, Config{AllowDirs: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	mk := func(name string, scopes ...string) string {
		sec, _, err := svc.CreateToken(prefix+name, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return sec
	}
	return &env{t: t, svc: svc, ts: ts, root: root,
		agent: mk("agent", service.ScopeRun, service.ScopeRead), oper: mk("oper", service.ScopeApprove, service.ScopeRead), reader: mk("reader", service.ScopeRead)}
}

func TestReadScopesOwnVersusAll(t *testing.T) {
	e := setup(t)
	own, _, _ := e.svc.CreateToken("own-only", []string{service.ScopeReadOwn})
	runner, _, _ := e.svc.CreateToken("runner", []string{service.ScopeRun}) // exec.run implies reading its own runs
	_, mine := e.do("POST", "/v1/exec", runner, map[string]any{"command": "echo mine"})
	_, theirs := e.do("POST", "/v1/exec", e.agent, map[string]any{"command": "echo theirs"})

	// the runner sees its own run, and only that
	if code, m := e.do("GET", "/v1/runs/"+mine["id"].(string), runner, nil); code != 200 || m["id"] != mine["id"] {
		t.Fatalf("own run: %d %v", code, m)
	}
	_, list := e.do("GET", "/v1/runs", runner, nil)
	runs := list["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["id"] != mine["id"] {
		t.Fatalf("list leaked other principals' runs: %v", list)
	}
	// another principal's run is 404, not 403: its existence is not revealed
	id := theirs["id"].(string)
	for _, path := range []string{"/v1/runs/" + id, "/v1/runs/" + id + "/output"} {
		if code, m := e.do("GET", path, runner, nil); code != 404 {
			t.Errorf("%s: %d %v", path, code, m)
		}
	}
	// a token with only read.own and no runs of its own sees nothing
	if _, l := e.do("GET", "/v1/runs", own, nil); len(l["runs"].([]any)) != 0 {
		t.Errorf("%v", l)
	}
	// exec.read still sees everything
	if _, l := e.do("GET", "/v1/runs", e.reader, nil); len(l["runs"].([]any)) != 2 {
		t.Errorf("exec.read should see all: %v", l)
	}
	if code, _ := e.do("GET", "/v1/runs/"+id+"/output", e.reader, nil); code != 200 {
		t.Errorf("exec.read output: %d", code)
	}
	// but read.own alone cannot run or approve
	if code, _ := e.do("POST", "/v1/exec", own, map[string]any{"command": "echo x"}); code != 403 {
		t.Errorf("read.own ran a command: %d", code)
	}
}

func TestReadOwnScopeSyntax(t *testing.T) {
	e := setup(t)
	if _, _, err := e.svc.CreateToken("ok1", []string{"exec.read.own"}); err != nil {
		t.Error(err)
	}
	if _, _, err := e.svc.CreateToken("ok2", []string{"exec.read.own:" + e.svc.Host()}); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"exec.read.owner", "exec.read.own.x", "exec.reads.own", "exec.read.own exec.admin"} {
		if _, _, err := e.svc.CreateToken("bad", []string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

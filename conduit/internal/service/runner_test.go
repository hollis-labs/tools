package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/runbook"
	"github.com/hollis-labs/conduit/internal/store"
)

func agent(ctx context.Context) context.Context { return As(ctx, "token:agent") }
func human(ctx context.Context) context.Context { return As(ctx, "token:human") }

func waitState(t *testing.T, s *Service, id string, want ...string) *store.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if r.State == w {
				return r
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, _ := s.Get(id)
	t.Fatalf("run %s stuck in %s, wanted %v", id, r.State, want)
	return nil
}

func TestSubmitLeavesAskPendingAndDoesNotExecute(t *testing.T) {
	s := newSvc(t)
	run, err := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if err != nil || run.State != store.StatePending || run.Principal != "token:agent" || len(run.Attempts) != 0 {
		t.Fatalf("%v %+v", err, run)
	}
	if _, err := s.Execute(context.Background(), run.ID, ExecOpts{}); !errors.Is(err, ErrNotRunnable) {
		t.Errorf("executing a pending run: %v", err)
	}
	pend, _ := s.Pending()
	if len(pend) != 1 || pend[0].ID != run.ID {
		t.Errorf("pending list: %+v", pend)
	}
}

func TestApproveThenExecute(t *testing.T) {
	s := newSvc(t)
	marker := filepath.Join(t.TempDir(), "ran")
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch " + marker})
	ap, err := s.Approve(human(context.Background()), run.ID, "api")
	if err != nil || ap.State != store.StateApproved || ap.ApprovedBy != "token:human" || ap.Approval != "api" {
		t.Fatalf("%v %+v", err, ap)
	}
	done, err := s.Execute(context.Background(), run.ID, ExecOpts{})
	if err != nil || done.State != store.StateDone {
		t.Fatalf("%v %+v", err, done)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("approved run did not execute")
	}
	if p, _ := s.Pending(); len(p) != 0 {
		t.Errorf("still pending: %+v", p)
	}
}

func TestAgentCannotApproveItsOwnRun(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if _, err := s.Approve(agent(context.Background()), run.ID, "api"); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("got %v", err)
	}
	if r, _ := s.Get(run.ID); r.State != store.StatePending {
		t.Errorf("state %s", r.State)
	}
}

func TestApprovalExpires(t *testing.T) {
	s := newSvc(t, WithApprovalTTL(50*time.Millisecond))
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	time.Sleep(120 * time.Millisecond)
	if _, err := s.Approve(human(context.Background()), run.ID, "api"); !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v", err)
	}
	if r, _ := s.Get(run.ID); r.State != store.StateExpired {
		t.Errorf("state %s", r.State)
	}
}

// A rule added after submission still bites at approval time.
func TestPolicyIsRedecidedAtApproval(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	s.policy = &policy.Policy{Rules: []policy.Rule{{ID: "no-touch", Bin: "touch", Verdict: policy.Deny}}}
	if _, err := s.Approve(human(context.Background()), run.ID, "api"); !errors.Is(err, ErrDenied) {
		t.Fatalf("got %v", err)
	}
	if r, _ := s.Get(run.ID); r.State != store.StateDenied {
		t.Errorf("state %s", r.State)
	}
}

func TestRejectAndDoubleApprove(t *testing.T) {
	s := newSvc(t)
	r1, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if rej, err := s.Reject(human(context.Background()), r1.ID); err != nil || rej.State != store.StateDenied || rej.Approval != "rejected:token:human" {
		t.Fatalf("%v %+v", err, rej)
	}
	if _, err := s.Approve(human(context.Background()), r1.ID, "api"); !errors.Is(err, ErrNotPending) {
		t.Errorf("approve after reject: %v", err)
	}
}

func TestExecuteRunsAtMostOnceUnderRace(t *testing.T) {
	s := newSvc(t)
	counter := filepath.Join(t.TempDir(), "count")
	run, _ := s.Submit(context.Background(), RunRequest{Command: "echo x >> " + counter, Shadow: true})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Execute(context.Background(), run.ID, ExecOpts{}); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	b, _ := os.ReadFile(counter) //nolint:gosec // test file in a temp dir
	if ok != 1 || string(b) != "x\n" {
		t.Fatalf("executed %d times, file %q", ok, b)
	}
}

func TestStartAsyncAndCancelRunning(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(context.Background(), RunRequest{Command: "sleep 30", Shadow: true})
	if err := s.Start(run.ID, ExecOpts{}); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, run.ID, store.StateRunning)
	time.Sleep(100 * time.Millisecond)
	if _, err := s.Cancel(context.Background(), run.ID, true); err != nil {
		t.Fatal(err)
	}
	r := waitState(t, s, run.ID, store.StateCancelled, store.StateFailed)
	if r.State != store.StateCancelled {
		t.Errorf("state %s", r.State)
	}
	if len(r.Attempts) != 1 || r.Attempts[0].Signal == "" {
		t.Errorf("attempt should record the kill: %+v", r.Attempts)
	}
	if _, err := s.Cancel(context.Background(), run.ID, true); err == nil {
		t.Error("canceling a finished run should fail")
	}
}

func TestCancelPendingWithdrawsIt(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	c, err := s.Cancel(agent(context.Background()), run.ID, false)
	if err != nil || c.State != store.StateCancelled {
		t.Fatalf("%v %+v", err, c)
	}
	if _, err := s.Approve(human(context.Background()), run.ID, "api"); !errors.Is(err, ErrNotPending) {
		t.Errorf("approving a canceled run: %v", err)
	}
}

func mustSet(t *testing.T, rbs ...*runbook.Runbook) *runbook.Set {
	t.Helper()
	set, err := runbook.NewSet(rbs...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestRunbookVerdictAppliesButDenyStands(t *testing.T) {
	set := mustSet(t,
		&runbook.Runbook{Name: "stamp", Argv: []string{"touch", "{{f}}"}, Verdict: "allow", Params: map[string]runbook.Param{"f": {Required: true, Pattern: `[A-Za-z0-9/._-]+`}}},
		&runbook.Runbook{Name: "wipe", Argv: []string{"rm", "-rf", "{{p}}"}, Verdict: "allow", Params: map[string]runbook.Param{"p": {Required: true, Pattern: `[a-z0-9/._~-]+`}}},
	)
	s := newSvc(t, WithRunbooks(set))
	dir := t.TempDir()

	// touch asks by default; the reviewed runbook allows it
	run, err := s.SubmitRunbook(agent(context.Background()), "stamp", map[string]string{"f": filepath.Join(dir, "a")}, RunRequest{})
	if err != nil || run.State != store.StateApproved || run.Parent != "runbook:stamp" {
		t.Fatalf("%v %+v", err, run)
	}
	// but a hard deny is not overridable by a runbook
	run, err = s.SubmitRunbook(agent(context.Background()), "wipe", map[string]string{"p": "~"}, RunRequest{})
	if !errors.Is(err, ErrDenied) || run.State != store.StateDenied {
		t.Fatalf("%v %+v", err, run)
	}
	if _, err := s.SubmitRunbook(context.Background(), "nope", nil, RunRequest{}); !errors.Is(err, ErrNoRunbook) {
		t.Errorf("unknown runbook: %v", err)
	}
	if _, err := s.SubmitRunbook(context.Background(), "stamp", map[string]string{"f": "x; rm -rf ~"}, RunRequest{}); !errors.Is(err, runbook.ErrParam) {
		t.Errorf("injection: %v", err)
	}
}

func TestRunbookAskStaysPending(t *testing.T) {
	set := mustSet(t, &runbook.Runbook{Name: "ping-host", Argv: []string{"echo", "{{h}}"}, Params: map[string]runbook.Param{"h": {Required: true}}})
	s := newSvc(t, WithRunbooks(set))
	run, err := s.SubmitRunbook(agent(context.Background()), "ping-host", map[string]string{"h": "w1"}, RunRequest{})
	if err != nil || run.State != store.StatePending {
		t.Fatalf("%v %+v", err, run)
	}
}

func TestTokensScopesAndRevocation(t *testing.T) {
	s := newSvc(t)
	secret, tok, err := s.CreateToken("ci", []string{ScopeRun, "exec.read:worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.Hash == secret || len(secret) < 40 {
		t.Errorf("secret handling: %q", secret)
	}
	p, err := s.Authenticate(secret)
	if err != nil || p.Name != "ci" || p.ID() != "token:ci" {
		t.Fatalf("%v %+v", err, p)
	}
	if !p.Can(ScopeRun, "worker-1") || !p.Can(ScopeRead, "worker-1") || p.Can(ScopeRead, "worker-2") || p.Can(ScopeApprove, "worker-1") {
		t.Errorf("scope check wrong for %+v", p.Scopes)
	}
	if _, err := s.Authenticate("cdt_wrong"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("bad secret: %v", err)
	}
	if _, err := s.Authenticate(""); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("empty: %v", err)
	}
	if ok, err := s.RevokeToken("ci"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if _, err := s.Authenticate(secret); !errors.Is(err, ErrUnauthenticated) {
		t.Error("revoked token still authenticates")
	}
	admin := &Principal{Scopes: []string{ScopeAdmin}}
	if !admin.Can(ScopeApprove, "x") {
		t.Error("admin covers everything")
	}
	for _, bad := range [][]string{nil, {"exec.root"}, {"read"}} {
		if _, _, err := s.CreateToken("x", bad); err == nil {
			t.Errorf("accepted scopes %v", bad)
		}
	}
}

// Approval is not a bearer token for later: the run is re-checked when it executes.
func TestExecuteRechecksPolicyAfterApproval(t *testing.T) {
	s := newSvc(t)
	marker := filepath.Join(t.TempDir(), "ran")
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch " + marker})
	if _, err := s.Approve(human(context.Background()), run.ID, "api"); err != nil {
		t.Fatal(err)
	}
	s.policy = &policy.Policy{Rules: []policy.Rule{{ID: "no-touch", Bin: "touch", Verdict: policy.Deny}}}
	r, err := s.Execute(context.Background(), run.ID, ExecOpts{})
	if !errors.Is(err, ErrDenied) || r.State != store.StateDenied {
		t.Fatalf("%v %+v", err, r)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("a run denied after approval still executed")
	}
}

func TestStaleHumanApprovalExpiresBeforeExecuting(t *testing.T) {
	s := newSvc(t, WithApprovalTTL(150*time.Millisecond))
	marker := filepath.Join(t.TempDir(), "ran")
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch " + marker})
	if _, err := s.Approve(human(context.Background()), run.ID, "api"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := s.Execute(context.Background(), run.ID, ExecOpts{}); !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v", err)
	}
	if r, _ := s.Get(run.ID); r.State != store.StateExpired {
		t.Errorf("state %s", r.State)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("stale approval executed")
	}
	// an allowed command is auto-approved: that approval does not go stale
	ok, _ := s.Submit(context.Background(), RunRequest{Command: "echo fine"})
	time.Sleep(250 * time.Millisecond)
	if r, err := s.Execute(context.Background(), ok.ID, ExecOpts{}); err != nil || r.State != store.StateDone {
		t.Fatalf("auto-approved run: %v %+v", err, r)
	}
}

func TestCancelRequiresOwnerOrAdmin(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if _, err := s.Cancel(As(context.Background(), "mcp:other"), run.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner: %v", err)
	}
	if r, _ := s.Get(run.ID); r.State != store.StatePending {
		t.Errorf("state %s", r.State)
	}
	if _, err := s.Cancel(As(context.Background(), "mcp:other"), run.ID, true); err != nil {
		t.Errorf("admin: %v", err)
	}
}

func TestSettleNeverOverwritesAnotherProcessesOutcome(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if _, err := s.Approve(human(context.Background()), run.ID, "api"); err != nil {
		t.Fatal(err)
	}
	s.settle(run.ID, store.StatePending, store.StateExpired) // a stale view: it is approved now
	if r, _ := s.Get(run.ID); r.State != store.StateApproved {
		t.Fatalf("state overwritten to %s", r.State)
	}
}

func TestPerPrincipalActiveCapAndTimeoutBounds(t *testing.T) {
	s := newSvc(t, WithMaxActive(2))
	for i := 0; i < 2; i++ {
		if _, err := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("third pending run: %v", err)
	}
	if _, err := s.Submit(As(context.Background(), "token:other"), RunRequest{Command: "touch /tmp/never"}); err != nil {
		t.Errorf("another principal is not affected: %v", err)
	}
	for _, d := range []time.Duration{-time.Second, 2 * time.Hour} {
		if _, err := s.Submit(As(context.Background(), "token:third"), RunRequest{Command: "echo", Timeout: d}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("timeout %v: %v", d, err)
		}
	}
}

func TestScopeStringsCannotSmuggleScopes(t *testing.T) {
	s := newSvc(t)
	for _, bad := range []string{"exec.read:h exec.admin", "exec.read exec.admin", "exec.read:", "exec.read:a b", "exec.readx", "EXEC.READ", "exec.read\nexec.admin"} {
		if _, _, err := s.CreateToken("x", []string{bad}); err == nil {
			t.Errorf("accepted scope %q", bad)
		}
	}
	for i, good := range []string{"exec.read", "exec.run:worker-1", "exec.approve:*", "exec.admin"} {
		if _, _, err := s.CreateToken(fmt.Sprintf("good%d", i), []string{good}); err != nil {
			t.Errorf("rejected scope %q: %v", good, err)
		}
	}
}

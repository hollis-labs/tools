package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tools/conduit/internal/policy"
	"github.com/hollis-labs/tools/conduit/internal/runbook"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

func agent(ctx context.Context) context.Context { return As(ctx, "key:agent") }

// human is a signed-in person: the only kind of context that can approve.
func human(ctx context.Context) context.Context { return AsPerson(ctx, "user:human") }

// submitApproved is how an approved run exists now: an agent submits, a person approves. A run the policy allows is
// returned as approved already.
func submitApproved(s *Service, rq RunRequest) (*store.Run, error) {
	run, err := s.Submit(agent(context.Background()), rq)
	if err != nil || run.State != store.StatePending {
		return run, err
	}
	return s.Approve(human(context.Background()), run.ID)
}

// runApproved is submitApproved followed by the execution.
func runApproved(s *Service, rq RunRequest) (*store.Run, error) {
	run, err := submitApproved(s, rq)
	if err != nil {
		return run, err
	}
	return s.Execute(agent(context.Background()), run.ID, ExecOpts{})
}

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
	if err != nil || run.State != store.StatePending || run.Principal != "key:agent" || len(run.Attempts) != 0 {
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
	ap, err := s.Approve(human(context.Background()), run.ID)
	if err != nil || ap.State != store.StateApproved || ap.ApprovedBy != "user:human" || ap.Approval != "person" {
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
	run, _ := s.Submit(human(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if _, err := s.Approve(human(context.Background()), run.ID); !errors.Is(err, ErrSelfApproval) {
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
	if _, err := s.Approve(human(context.Background()), run.ID); !errors.Is(err, ErrExpired) {
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
	if _, err := s.Approve(human(context.Background()), run.ID); !errors.Is(err, ErrDenied) {
		t.Fatalf("got %v", err)
	}
	if r, _ := s.Get(run.ID); r.State != store.StateDenied {
		t.Errorf("state %s", r.State)
	}
}

func TestRejectAndDoubleApprove(t *testing.T) {
	s := newSvc(t)
	r1, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if rej, err := s.Reject(human(context.Background()), r1.ID); err != nil || rej.State != store.StateDenied || rej.Approval != "rejected:user:human" {
		t.Fatalf("%v %+v", err, rej)
	}
	if _, err := s.Approve(human(context.Background()), r1.ID); !errors.Is(err, ErrNotPending) {
		t.Errorf("approve after reject: %v", err)
	}
}

func TestExecuteRunsAtMostOnceUnderRace(t *testing.T) {
	s := newSvc(t)
	counter := filepath.Join(t.TempDir(), "count")
	run, _ := submitApproved(s, RunRequest{Command: "echo x >> " + counter})
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
	run, _ := submitApproved(s, RunRequest{Command: "sleep 30"})
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
	if _, err := s.Approve(human(context.Background()), run.ID); !errors.Is(err, ErrNotPending) {
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

// Approval is not a bearer token for later: the run is re-checked when it executes.
func TestExecuteRechecksPolicyAfterApproval(t *testing.T) {
	s := newSvc(t)
	marker := filepath.Join(t.TempDir(), "ran")
	run, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch " + marker})
	if _, err := s.Approve(human(context.Background()), run.ID); err != nil {
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
	if _, err := s.Approve(human(context.Background()), run.ID); err != nil {
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
	if _, err := s.Approve(human(context.Background()), run.ID); err != nil {
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
	if _, err := s.Submit(As(context.Background(), "key:other"), RunRequest{Command: "touch /tmp/never"}); err != nil {
		t.Errorf("another principal is not affected: %v", err)
	}
	for _, d := range []time.Duration{-time.Second, 2 * time.Hour} {
		if _, err := s.Submit(As(context.Background(), "key:third"), RunRequest{Command: "echo", Timeout: d}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("timeout %v: %v", d, err)
		}
	}
}

func TestRunbookTimeoutIsACeilingNotADefault(t *testing.T) {
	set := mustSet(t, &runbook.Runbook{Name: "bounded", Argv: []string{"echo", "x"}, Verdict: "allow", Timeout: 20 * time.Second})
	s := newSvc(t, WithRunbooks(set))
	eff := func(callerSec int) time.Duration {
		v, err := s.ExecAndWait(context.Background(), ExecRequest{Runbook: "bounded", TimeoutSec: callerSec})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := s.Get(v.ID)
		var ep execParams
		_ = json.Unmarshal([]byte(r.Request), &ep)
		return ep.Timeout
	}
	if got := eff(3600); got != 20*time.Second {
		t.Errorf("a caller lengthened the runbook's timeout: %v", got)
	}
	if got := eff(5); got != 5*time.Second {
		t.Errorf("a caller should be able to shorten it: %v", got)
	}
	if got := eff(0); got != 20*time.Second {
		t.Errorf("no caller timeout should use the runbook's: %v", got)
	}
}

func TestRemoteRequestsAlwaysGetATimeout(t *testing.T) {
	s := newSvc(t)
	v, err := s.ExecAndWait(context.Background(), ExecRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get(v.ID)
	var ep execParams
	_ = json.Unmarshal([]byte(r.Request), &ep)
	if ep.Timeout != DefaultRemoteTimeout {
		t.Errorf("timeout %v, want the %v default", ep.Timeout, DefaultRemoteTimeout)
	}
}

// No local-trust path reaches approve: a context that is not a signed-in person (an agent, an API key, a local process,
// any CLI context) is refused, for approve and reject, and the run stays pending.
func TestLocalTrustPrincipalCannotApproveOrReject(t *testing.T) {
	s := newSvc(t)
	run, err := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	if err != nil || run.State != store.StatePending {
		t.Fatalf("%v %+v", err, run)
	}
	for name, ctx := range map[string]context.Context{
		"agent key":  As(context.Background(), "key:other"),
		"local user": As(context.Background(), "local:operator"),
		"no context": context.Background(),
	} {
		if _, err := s.Approve(ctx, run.ID); !errors.Is(err, ErrNotPerson) {
			t.Errorf("%s approve: %v", name, err)
		}
		if _, err := s.Reject(ctx, run.ID); !errors.Is(err, ErrNotPerson) {
			t.Errorf("%s reject: %v", name, err)
		}
	}
	if r, _ := s.Get(run.ID); r.State != store.StatePending {
		t.Fatalf("run left pending state: %s", r.State)
	}
}

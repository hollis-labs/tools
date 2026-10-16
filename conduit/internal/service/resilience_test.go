package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/conduit/internal/blob"
	"github.com/hollis-labs/conduit/internal/cmdparse"
	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/proc"
	"github.com/hollis-labs/conduit/internal/store"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"
)

type memReporter struct {
	mu   sync.Mutex
	recs []panics.Record
}

func (m *memReporter) Report(r panics.Record) { m.mu.Lock(); m.recs = append(m.recs, r); m.mu.Unlock() }
func (m *memReporter) all() []panics.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]panics.Record(nil), m.recs...)
}

func captured(t *testing.T) *memReporter {
	t.Helper()
	m := &memReporter{}
	panics.SetDefault(panics.Config{Reporter: m, Redact: redact.Default().Text})
	t.Cleanup(func() { panics.SetDefault(panics.Config{Reporter: discard{}, Redact: redact.Default().Text}) })
	return m
}

type discard struct{}

func (discard) Report(panics.Record) {}

// blobs that explode, standing in for any bug in code Execute calls.
type panicBlobs struct{ blob.Store }

func (panicBlobs) Put(context.Context, []byte, blob.Meta) (string, error) {
	panic("blob store bug while holding token=sk-abcdefghijklmnopqrstuvwxyz")
}

func TestPanicDuringExecutionFailsTheRunAndKeepsTheProcessAlive(t *testing.T) {
	rep := captured(t)
	s := newSvc(t)
	s.blobs = panicBlobs{s.blobs}
	run, err := s.Submit(context.Background(), RunRequest{Command: "echo hello", Shadow: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Execute(context.Background(), run.ID, ExecOpts{})
	ref, isPanic := panics.Ref(err)
	if !isPanic {
		t.Fatalf("expected a PanicError, got %v", err)
	}
	if got == nil || got.State != store.StateFailed || got.FailureClass != "internal_error" || got.PanicRef != ref {
		t.Fatalf("the run must be failed with the panic ref, not left running: %+v", got)
	}
	if strings.Contains(got.FailureMessage, "sk-") || strings.Contains(err.Error(), "sk-") {
		t.Error("panic text leaked into the failure record or error")
	}
	recs := rep.all()
	if len(recs) != 1 || recs[0].Attrs["run"] != run.ID || strings.Contains(recs[0].Value, "sk-abcdefghijklmnop") {
		t.Fatalf("panic record: %+v", recs)
	}
	// the service is still usable afterwards
	s.blobs = panicBlobs{}.Store
	s2 := newSvc(t)
	if r, err := s2.Run(context.Background(), RunRequest{Command: "echo ok"}); err != nil || r.State != store.StateDone {
		t.Fatalf("service unusable after a contained panic: %v %+v", err, r)
	}
}

func TestPanicInStartedRunIsContainedAndNeverStaysRunning(t *testing.T) {
	captured(t)
	s := newSvc(t)
	s.blobs = panicBlobs{s.blobs}
	run, _ := s.Submit(context.Background(), RunRequest{Command: "echo hello", Shadow: true})
	if err := s.Start(run.ID, ExecOpts{}); err != nil {
		t.Fatal(err)
	}
	r := waitState(t, s, run.ID, store.StateFailed, store.StateDone)
	if r.State != store.StateFailed || r.FailureClass != "internal_error" {
		t.Fatalf("%+v", r)
	}
}

func TestPolicyPanicDeniesAndNeverAllows(t *testing.T) {
	captured(t)
	s := newSvc(t)
	s.decide = func(*cmdparse.Parsed) policy.Decision { panic("policy bug") }
	_, d := s.Check("echo hi")
	if d.Verdict != policy.Deny || len(d.Reasons) != 1 || d.Reasons[0].Rule != "internal-error" || !strings.Contains(d.Reasons[0].Why, "pnc_") {
		t.Fatalf("%+v", d)
	}
	marker := t.TempDir() + "/ran"
	run, err := s.Submit(context.Background(), RunRequest{Command: "touch " + marker})
	if !errors.Is(err, ErrDenied) || run.State != store.StateDenied {
		t.Fatalf("%v %+v", err, run)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("ran although policy evaluation panicked")
	}
}

func TestHeartbeatAdvancesWhileARunIsRunning(t *testing.T) {
	s := newSvc(t, WithLiveness(50*time.Millisecond, time.Minute))
	run, _ := s.Submit(context.Background(), RunRequest{Command: "sleep 1", Shadow: true})
	if err := s.Start(run.ID, ExecOpts{}); err != nil {
		t.Fatal(err)
	}
	first := waitState(t, s, run.ID, store.StateRunning).HeartbeatAt
	time.Sleep(300 * time.Millisecond)
	r, _ := s.Get(run.ID)
	if !r.HeartbeatAt.After(first) {
		t.Errorf("heartbeat did not advance: %v -> %v", first, r.HeartbeatAt)
	}
	if r.OwnerPID != os.Getpid() || r.OwnerStart == 0 {
		t.Errorf("owner not recorded: %d %d", r.OwnerPID, r.OwnerStart)
	}
	waitState(t, s, run.ID, store.StateDone)
}

func TestProcessGroupIsRecordedForTheReaper(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Submit(context.Background(), RunRequest{Command: "sleep 2", Shadow: true})
	if err := s.Start(run.ID, ExecOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := s.Get(run.ID); r.PGID > 1 && r.PGIDStart > 0 {
			if !proc.Alive(r.PGID, r.PGIDStart) {
				t.Error("recorded group leader is not alive")
			}
			_, _ = s.Cancel(context.Background(), run.ID, true)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("process group never recorded")
}

// --- reaper ---

// orphan fabricates what a crashed owner leaves behind: a run in state running
// whose heartbeat is old, whose owner pid is dead, and whose command still runs.
func orphan(t *testing.T, s *Service, host string, beat time.Time, pgidStartDelta int64) (*store.Run, *exec.Cmd) {
	t.Helper()
	child := exec.Command("sleep", "60")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL); _ = child.Wait() })
	start, _ := proc.StartTime(child.Process.Pid)

	dead := exec.Command("true")
	_ = dead.Run() // a pid that has exited
	r := &store.Run{ID: newID(), Principal: "token:x", Host: host, Cwd: "/tmp", Command: "sleep 60", Parsed: "{}", Effect: "read",
		PolicyVerdict: "allow", PolicyReasons: "[]", PolicyMode: "enforce", State: store.StateRunning, CreatedAt: beat,
		HeartbeatAt: beat, OwnerPID: dead.Process.Pid, OwnerStart: 12345, PGID: child.Process.Pid, PGIDStart: start + pgidStartDelta}
	if err := s.store.InsertRun(r); err != nil {
		t.Fatal(err)
	}
	// InsertRun does not write the liveness columns; set them as the executor would
	setLiveness(t, s, r)
	return r, child
}

func setLiveness(t *testing.T, s *Service, r *store.Run) {
	t.Helper()
	if err := s.store.SetLivenessForTest(r.ID, r.OwnerPID, r.OwnerStart, r.PGID, r.PGIDStart, r.HeartbeatAt); err != nil {
		t.Fatal(err)
	}
}

func groupAlive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }

func TestReapKillsOrphanedGroupAndFailsTheRun(t *testing.T) {
	s := newSvc(t, WithLiveness(time.Second, 30*time.Second))
	r, child := orphan(t, s, s.host, time.Now().Add(-2*time.Minute), 0)
	rep, err := s.Reap(time.Now())
	if err != nil || rep.Orphaned != 1 || rep.Killed != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	_ = child.Wait()
	if groupAlive(r.PGID) {
		t.Error("orphaned process group was left running")
	}
	got, _ := s.Get(r.ID)
	if got.State != store.StateFailed || got.FailureClass != "orphaned" || len(got.Attempts) != 1 || got.Attempts[0].Class != "orphaned" {
		t.Fatalf("%+v", got)
	}
	// idempotent
	if rep, _ := s.Reap(time.Now()); rep.Orphaned != 0 {
		t.Errorf("second reap: %+v", rep)
	}
}

func TestReapNeverSignalsAProcessThatMerelyReusedThePid(t *testing.T) {
	s := newSvc(t)
	r, child := orphan(t, s, s.host, time.Now().Add(-2*time.Minute), +999) // recorded start time differs
	rep, _ := s.Reap(time.Now())
	if rep.Killed != 0 || rep.Orphaned != 1 {
		t.Fatalf("%+v", rep)
	}
	if !groupAlive(r.PGID) {
		t.Fatal("a process whose start time did not match was killed: pid reuse is not guarded")
	}
	_ = child
}

func TestReapLeavesRunsWhoseOwnerIsAliveOrHeartbeatFresh(t *testing.T) {
	s := newSvc(t, WithLiveness(time.Second, 30*time.Second))
	// stale heartbeat but the owner is this very process: leave it alone
	r, _ := orphan(t, s, s.host, time.Now().Add(-2*time.Minute), 0)
	self, _ := proc.StartTime(os.Getpid())
	r.OwnerPID, r.OwnerStart = os.Getpid(), self
	setLiveness(t, s, r)
	fresh, freshChild := orphan(t, s, s.host, time.Now().Add(-2*time.Second), 0)
	other, otherChild := orphan(t, s, "some-other-host", time.Now().Add(-2*time.Minute), 0)

	rep, _ := s.Reap(time.Now())
	if rep.Orphaned != 0 || rep.Skipped != 1 {
		t.Fatalf("%+v", rep)
	}
	for _, id := range []string{r.ID, fresh.ID, other.ID} {
		if got, _ := s.Get(id); got.State != store.StateRunning {
			t.Errorf("run %s was touched: %s", id, got.State)
		}
	}
	if !groupAlive(freshChild.Process.Pid) || !groupAlive(otherChild.Process.Pid) {
		t.Error("a live run's process group was killed")
	}
}

func TestReapExpiresStaleApprovedAndPending(t *testing.T) {
	s := newSvc(t, WithApprovalTTL(50*time.Millisecond))
	pend, _ := s.Submit(agent(context.Background()), RunRequest{Command: "touch /tmp/never"})
	appr, _ := s.Submit(context.Background(), RunRequest{Command: "echo hi"}) // auto-approved, never started
	time.Sleep(120 * time.Millisecond)
	rep, _ := s.Reap(time.Now())
	if rep.Expired != 2 {
		t.Fatalf("%+v", rep)
	}
	for _, id := range []string{pend.ID, appr.ID} {
		if got, _ := s.Get(id); got.State != store.StateExpired {
			t.Errorf("%s: %s", id, got.State)
		}
	}
	if _, err := s.Execute(context.Background(), appr.ID, ExecOpts{}); !errors.Is(err, ErrNotRunnable) {
		t.Errorf("an expired run must not execute: %v", err)
	}
}

func TestShutdownWaitsForExecutionsToRecord(t *testing.T) {
	s := newSvc(t)
	ctx, cancel := context.WithCancel(context.Background())
	run, _ := s.Submit(context.Background(), RunRequest{Command: "sleep 30", Shadow: true})
	go func() { _, _ = s.Execute(ctx, run.ID, ExecOpts{}) }()
	waitState(t, s, run.ID, store.StateRunning)
	time.Sleep(100 * time.Millisecond)
	cancel() // what a stopping daemon does
	s.Shutdown(5 * time.Second)
	if got, _ := s.Get(run.ID); got.State == store.StateRunning {
		t.Fatalf("Shutdown returned before the run was recorded: %+v", got)
	}
}

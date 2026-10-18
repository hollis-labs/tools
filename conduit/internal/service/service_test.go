package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/conduit/internal/blob"
	"github.com/hollis-labs/tools/conduit/internal/policy"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

func newSvc(t *testing.T, opts ...Option) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "conduit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := blob.NewLocal(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	return New(st, append([]Option{WithBlobs(bl)}, opts...)...)
}

func approveAll(context.Context, Preview) (bool, string) { return true, "test" }

func TestAllowedRunIsRecordedWithRecallableOutput(t *testing.T) {
	s := newSvc(t)
	run, err := s.Run(context.Background(), RunRequest{Command: "echo hello; echo oops >&2"})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.StateDone || run.Approval != "auto" || run.PolicyVerdict != "allow" {
		t.Fatalf("%+v", run)
	}
	if len(run.Attempts) != 1 || run.Attempts[0].Class != "ok" {
		t.Fatalf("%+v", run.Attempts)
	}
	out, err := s.Recall(context.Background(), run.ID, "stdout", 0)
	if err != nil || string(out) != "hello\n" {
		t.Fatalf("%q %v", out, err)
	}
	errb, _ := s.Recall(context.Background(), run.ID, "stderr", 0)
	if string(errb) != "oops\n" {
		t.Errorf("stderr %q", errb)
	}
}

func TestAskWithoutApproverIsDeniedAndNothingRuns(t *testing.T) {
	s := newSvc(t)
	marker := filepath.Join(t.TempDir(), "ran")
	run, err := s.Run(context.Background(), RunRequest{Command: "touch " + marker})
	if !errors.Is(err, ErrNotAsked) {
		t.Fatalf("err %v", err)
	}
	if run.State != store.StateDenied || len(run.Attempts) != 0 {
		t.Fatalf("%+v", run)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("command ran without approval")
	}
}

func TestApprovedAskRuns(t *testing.T) {
	s := newSvc(t)
	marker := filepath.Join(t.TempDir(), "ran")
	var seen Preview
	run, err := s.Run(context.Background(), RunRequest{Command: "touch " + marker, Approve: func(_ context.Context, p Preview) (bool, string) {
		seen = p
		return true, "tty"
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatal("approved command did not run")
	}
	if run.Approval != "tty" || run.State != store.StateDone {
		t.Fatalf("%+v", run)
	}
	if seen.Parsed == nil || seen.Decision.Verdict != policy.Ask {
		t.Errorf("approver saw %+v", seen)
	}
}

func TestDenyIsNotOverridableByApprover(t *testing.T) {
	s := newSvc(t)
	called := false
	run, err := s.Run(context.Background(), RunRequest{Command: "rm -rf /", Approve: func(context.Context, Preview) (bool, string) {
		called = true
		return true, "flag"
	}})
	if !errors.Is(err, ErrDenied) || called {
		t.Fatalf("err=%v approverCalled=%v", err, called)
	}
	if run.State != store.StateDenied || len(run.Attempts) != 0 {
		t.Fatalf("%+v", run)
	}
}

func TestShadowModeStillBlocksDeny(t *testing.T) {
	s := newSvc(t)
	marker := filepath.Join(t.TempDir(), "ran")
	run, err := s.Run(context.Background(), RunRequest{Command: "touch " + marker, Shadow: true})
	if err != nil {
		t.Fatal(err)
	}
	if run.PolicyMode != "shadow" || run.PolicyVerdict != "ask" || run.State != store.StateDone {
		t.Fatalf("%+v", run)
	}
	if _, err := s.Run(context.Background(), RunRequest{Command: "rm -rf ~", Shadow: true}); !errors.Is(err, ErrDenied) {
		t.Fatalf("shadow must not run a deny: %v", err)
	}
}

func TestOutputIsRedactedBeforeStorage(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Run(context.Background(), RunRequest{Command: `echo "DB_PASSWORD=hunter2hunter2"`})
	out, _ := s.Recall(context.Background(), run.ID, "stdout", 0)
	if strings.Contains(string(out), "hunter2") || run.Attempts[0].Redactions != 1 {
		t.Fatalf("%q %+v", out, run.Attempts[0])
	}
}

func TestFailureAndTimeoutAreRecorded(t *testing.T) {
	s := newSvc(t)
	run, _ := s.Run(context.Background(), RunRequest{Command: "false"})
	if run.State != store.StateFailed || run.Attempts[0].ExitCode != 1 {
		t.Fatalf("%+v", run)
	}
	run, _ = s.Run(context.Background(), RunRequest{Command: "sleep 30", Timeout: 150 * time.Millisecond, Approve: approveAll})
	if run.State != store.StateFailed || run.Attempts[0].Class != "timeout" {
		t.Fatalf("%+v", run.Attempts)
	}
}

func TestRetryIsLinkedAttemptAndOnlyForReadOnly(t *testing.T) {
	s := newSvc(t)
	flag := filepath.Join(t.TempDir(), "n")
	// fails with a transient error the first time, then succeeds
	cmd := `if [ -e ` + flag + ` ]; then echo ok; else touch ` + flag + `; echo "connection refused" >&2; exit 1; fi`
	run, err := s.Run(context.Background(), RunRequest{Command: cmd, Retries: 1, Approve: approveAll})
	if err != nil {
		t.Fatal(err)
	}
	// touch makes the effect write: must NOT be retried
	if len(run.Attempts) != 1 {
		t.Fatalf("write command was retried: %+v", run.Attempts)
	}

	flag2 := filepath.Join(t.TempDir(), "m")
	_ = os.WriteFile(flag2, nil, 0o600)
	os.Remove(flag2)
	cmd2 := `if cat ` + flag2 + ` 2>/dev/null; then :; else echo "connection refused" >&2; exit 1; fi`
	run, _ = s.Run(context.Background(), RunRequest{Command: cmd2, Retries: 1})
	if len(run.Attempts) != 2 || run.Attempts[1].RetryOf != 1 || run.Attempts[0].Class != "transient" {
		t.Fatalf("read-only transient failure should retry as a linked attempt: %+v", run.Attempts)
	}
}

func TestGetByFragmentAndList(t *testing.T) {
	s := newSvc(t)
	r1, _ := s.Run(context.Background(), RunRequest{Command: "echo a", Session: "s1"})
	_, _ = s.Run(context.Background(), RunRequest{Command: "echo b", Session: "s2"})
	got, err := s.Get(r1.ID[len(r1.ID)-6:])
	if err != nil || got.ID != r1.ID {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := s.Get("zzzz"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want not found, got %v", err)
	}
	runs, _ := s.List("s1", "", 10)
	if len(runs) != 1 || runs[0].Command != "echo a" {
		t.Errorf("%+v", runs)
	}
}

func bwrapUsable(t *testing.T) {
	t.Helper()
	if err := exec.Command("bwrap", "--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib64", "/lib64", "true").Run(); err != nil {
		t.Skipf("bwrap unusable: %v", err)
	}
}

func TestPolicySandboxObligationIsEnforcedAndRequestCannotLowerIt(t *testing.T) {
	bwrapUsable(t)
	ws := t.TempDir()
	pol := &policy.Policy{Rules: []policy.Rule{{ID: "touchers", Bin: "touch", Verdict: policy.Allow, Sandbox: "ro"}}}
	s := newSvc(t, WithPolicy(pol))

	// the request asks for a weaker profile than the obligation; the obligation wins
	run, err := s.Run(context.Background(), RunRequest{Command: "touch f", Dir: ws, Sandbox: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Sandbox != "ro" {
		t.Fatalf("effective sandbox %q, want ro", run.Sandbox)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "f")); statErr == nil {
		t.Error("ro obligation did not stop the write")
	}
	if run.State != store.StateFailed || len(run.Attempts) != 1 || run.Attempts[0].Class == "sandbox_failed" {
		t.Fatalf("expected a normal failure from the confined command, got %+v", run.Attempts)
	}
	if errb, _ := s.Recall(context.Background(), run.ID, "stderr", 0); !strings.Contains(string(errb), "Read-only file system") {
		t.Errorf("the write should have failed with EROFS, stderr %q", errb)
	}
}

func TestRequestedSandboxIsRecorded(t *testing.T) {
	bwrapUsable(t)
	s := newSvc(t)
	run, err := s.Run(context.Background(), RunRequest{Command: "echo hi", Sandbox: "workspace", Dir: t.TempDir()})
	if err != nil || run.Sandbox != "workspace" || run.State != store.StateDone {
		t.Fatalf("%v %+v", err, run)
	}
	if _, err := s.Run(context.Background(), RunRequest{Command: "echo hi", Sandbox: "fortress"}); err == nil {
		t.Error("unknown profile accepted")
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/redact"
	"github.com/hollis-labs/conduit/internal/store"
)

// RunView is the transport-facing summary of a run: state, verdict and, once it
// has executed, the exit status and a bounded slice of the stored output.
type RunView struct {
	ID         string          `json:"id"`
	Short      string          `json:"short_id"`
	State      string          `json:"state"`
	Command    string          `json:"command"`
	Cwd        string          `json:"cwd"`
	Principal  string          `json:"principal"`
	Host       string          `json:"host"`
	Session    string          `json:"session,omitempty"`
	Verdict    string          `json:"verdict"`
	Effect     string          `json:"effect"`
	Sandbox    string          `json:"sandbox,omitempty"`
	Approval   string          `json:"approval,omitempty"`
	ApprovedBy string          `json:"approved_by,omitempty"`
	Reasons    []policy.Reason `json:"reasons,omitempty"`
	Attempts   int             `json:"attempts"`
	ExitCode   *int            `json:"exit_code,omitempty"`
	Class      string          `json:"class,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	StdoutSize int64           `json:"stdout_bytes,omitempty"`
	StderrSize int64           `json:"stderr_bytes,omitempty"`
	Truncated  bool            `json:"output_truncated,omitempty"`
	Redactions int             `json:"redactions,omitempty"`
	// FailureClass/FailureMessage/PanicRef describe a run that failed for a reason
	// other than the command's own exit status (internal_error, orphaned). The
	// message never carries panic text: look the ref up with `conduit panics show`.
	FailureClass   string `json:"failure_class,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
	PanicRef       string `json:"panic_ref,omitempty"`
	// OutputTrust marks stdout and stderr as data produced by the command, not
	// instructions for whoever reads them.
	OutputTrust string `json:"output_trust,omitempty"`
	Hint        string `json:"hint,omitempty"`
}

// ShortID is the unique-looking tail of a run id.
func ShortID(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '-' {
			return id[i+1:]
		}
	}
	return id
}

// View summarizes a run. maxBytes bounds each output stream (0 = no output).
func (s *Service) View(ctx context.Context, r *store.Run, maxBytes int) *RunView {
	v := &RunView{
		ID: r.ID, Short: ShortID(r.ID), State: r.State, Command: redact.Text(r.Command, 8192), Cwd: r.Cwd, Principal: r.Principal,
		Host: r.Host, Session: r.Session, Verdict: r.PolicyVerdict, Effect: r.Effect, Sandbox: r.Sandbox,
		Approval: r.Approval, ApprovedBy: r.ApprovedBy, Attempts: len(r.Attempts),
	}
	_ = json.Unmarshal([]byte(r.PolicyReasons), &v.Reasons)
	for i := range v.Reasons { // command text can hold secrets typed inline; views leave the machine
		v.Reasons[i].Segment = redact.Text(v.Reasons[i].Segment, 2048)
		v.Reasons[i].Why = redact.Text(v.Reasons[i].Why, 2048)
	}
	v.FailureClass, v.FailureMessage, v.PanicRef = r.FailureClass, r.FailureMessage, r.PanicRef
	switch r.State {
	case store.StatePending:
		v.Hint = fmt.Sprintf("awaiting operator approval: `conduit approve %s` (expires %s after submission)", v.Short, s.approvalTTL)
	case store.StateDenied:
		v.Hint = "denied; this command will not run"
	case store.StateExpired:
		v.Hint = "approval window passed; submit again"
	}
	switch r.FailureClass {
	case "internal_error":
		v.Hint = "conduit hit an internal error running this; the operator can look up " + r.PanicRef + " (conduit panics show)"
	case "orphaned":
		v.Hint = "the process that was running this exited before it finished; the command was stopped"
	}
	if n := len(r.Attempts); n > 0 {
		a := r.Attempts[n-1]
		code := a.ExitCode
		v.ExitCode, v.Class = &code, a.Class
		v.DurationMS = a.Ended.Sub(a.Started).Milliseconds()
		v.StdoutSize, v.StderrSize, v.Redactions = a.StdoutBytes, a.StderrBytes, a.Redactions
		v.OutputTrust = "untrusted"
		if maxBytes > 0 {
			var t1, t2 bool
			v.Stdout, t1 = s.readBounded(ctx, a.StdoutRef, maxBytes)
			v.Stderr, t2 = s.readBounded(ctx, a.StderrRef, maxBytes)
			v.Truncated = t1 || t2 || a.Truncated
		}
		if v.Truncated || maxBytes == 0 {
			v.Hint = fmt.Sprintf("full output: `conduit recall %s` or exec_output", v.Short)
		}
	}
	return v
}

func (s *Service) readBounded(ctx context.Context, ref string, limit int) (string, bool) {
	if ref == "" || s.blobs == nil {
		return "", false
	}
	b, err := s.blobs.Get(ctx, ref)
	if err != nil {
		return "", false
	}
	if len(b) > limit {
		return string(b[:limit]), true
	}
	return string(b), false
}

// Wait polls until the run leaves the active states (approved, running) or the
// wait elapses; a pending run is returned as is unless untilApproved is set, in
// which case it keeps waiting for a human.
func (s *Service) Wait(ctx context.Context, idFrag string, d time.Duration, untilApproved bool) (*store.Run, error) {
	deadline := time.Now().Add(d)
	for {
		run, err := s.Get(idFrag)
		if err != nil {
			return nil, err
		}
		switch run.State {
		case store.StateApproved, store.StateRunning:
		case store.StatePending:
			if !untilApproved {
				return run, nil
			}
		default:
			return run, nil
		}
		if time.Now().After(deadline) {
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// StartApproved starts every approved, not yet started run submitted by
// principal. A run approved from another process (the operator's CLI) is
// executed by the process that owns it.
func (s *Service) StartApproved(principal string) (int, error) {
	runs, err := s.store.ListRuns(store.Filter{State: store.StateApproved, Limit: 100})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range runs {
		if r.Principal != principal {
			continue
		}
		if err := s.Start(r.ID, ExecOpts{}); err == nil {
			n++
		}
	}
	return n, nil
}

// ExecRequest is the transport-neutral request to run a command or a runbook.
type ExecRequest struct {
	Command    string            `json:"command"`
	Runbook    string            `json:"runbook"`
	Params     map[string]string `json:"params"`
	Dir        string            `json:"dir"`
	Session    string            `json:"session"`
	Sandbox    string            `json:"sandbox"`
	TimeoutSec int               `json:"timeout_sec"`
	WaitSec    *int              `json:"wait_sec"` // nil = default 30; 0 returns at once
	MaxBytes   int               `json:"max_bytes"`
}

// DefaultRemoteTimeout applies to commands submitted through a transport that
// did not choose a timeout, so a remote caller cannot start an unbounded process.
const DefaultRemoteTimeout = 10 * time.Minute

// Limits for transports.
const (
	DefaultWaitSec  = 30
	MaxWaitSec      = 300
	DefaultMaxBytes = 16 << 10
	MaxMaxBytes     = 1 << 20
)

// ErrBadRequest marks a malformed ExecRequest.
var ErrBadRequest = errors.New("bad request")

// ClampBytes applies the output-size default and ceiling.
func ClampBytes(n int) int {
	switch {
	case n <= 0:
		return DefaultMaxBytes
	case n > MaxMaxBytes:
		return MaxMaxBytes
	}
	return n
}

// WaitDuration applies the wait default and ceiling.
func WaitDuration(sec *int) time.Duration {
	w := DefaultWaitSec
	if sec != nil {
		w = min(max(*sec, 0), MaxWaitSec)
	}
	return time.Duration(w) * time.Second
}

// ExecAndWait is what a remote caller means by "run this": submit under the
// caller's principal (taken from ctx), start it if it was allowed, and wait a
// bounded time for it. A pending or denied run comes back as such, not as an
// error. dir, if set, has already been validated by the transport.
func (s *Service) ExecAndWait(ctx context.Context, a ExecRequest) (*RunView, error) {
	if (a.Command == "") == (a.Runbook == "") {
		return nil, fmt.Errorf("%w: give exactly one of command or runbook", ErrBadRequest)
	}
	timeout := time.Duration(a.TimeoutSec) * time.Second
	if a.TimeoutSec < 0 || timeout > MaxTimeout {
		return nil, fmt.Errorf("%w: timeout_sec must be between 0 and %d", ErrBadRequest, int(MaxTimeout.Seconds()))
	}
	if timeout == 0 {
		timeout = DefaultRemoteTimeout
	}
	rq := RunRequest{Dir: a.Dir, Session: a.Session, Sandbox: a.Sandbox, Timeout: timeout}
	var run *store.Run
	var err error
	if a.Runbook != "" {
		run, err = s.SubmitRunbook(ctx, a.Runbook, a.Params, rq)
	} else {
		rq.Command = a.Command
		run, err = s.Submit(ctx, rq)
	}
	if err != nil && !errors.Is(err, ErrDenied) {
		return nil, err
	}
	if run.State == store.StateApproved {
		// The background starter may claim the run first; that is not an error.
		if err = s.Start(run.ID, ExecOpts{}); err != nil && !errors.Is(err, ErrNotRunnable) {
			return nil, err
		}
		if run, err = s.Wait(ctx, run.ID, WaitDuration(a.WaitSec), false); err != nil {
			return nil, err
		}
	}
	return s.View(ctx, run, ClampBytes(a.MaxBytes)), nil
}

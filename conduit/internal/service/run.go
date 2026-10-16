package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hollis-labs/conduit/internal/blob"
	"github.com/hollis-labs/conduit/internal/cmdparse"
	"github.com/hollis-labs/conduit/internal/cmdrun"
	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/proc"
	"github.com/hollis-labs/conduit/internal/runbook"
	"github.com/hollis-labs/conduit/internal/safe"
	"github.com/hollis-labs/conduit/internal/store"
)

// Errors a transport maps to exit codes or status codes.
var (
	ErrDenied       = errors.New("denied by policy")
	ErrNotAsked     = errors.New("approval required and none given")
	ErrNoBlobs      = errors.New("no output store configured")
	ErrNotPending   = errors.New("run is not awaiting approval")
	ErrNotRunnable  = errors.New("run is not approved to execute")
	ErrExpired      = errors.New("approval window has passed")
	ErrSelfApproval = errors.New("a run cannot be approved by the principal that submitted it")
	ErrNoRunbook    = errors.New("no such runbook")
	ErrTooMany      = errors.New("too many active runs for this principal")
	ErrForbidden    = errors.New("not permitted to act on this run")
)

// Preview is what an approver is shown: the parsed, normalised command and the
// verdict, never only the caller's own description of it.
type Preview struct {
	Command  string
	Parsed   *cmdparse.Parsed
	Decision policy.Decision
}

// Approver decides an Ask. kind records how approval was obtained ("tty",
// "flag"); ok=false refuses. It is never consulted for a Deny.
type Approver func(ctx context.Context, p Preview) (ok bool, kind string)

// RunRequest is one command to run.
type RunRequest struct {
	Command string
	Dir     string
	Session string
	Parent  string
	Timeout time.Duration
	Shadow  bool     // record the verdict but do not require approval; Deny still blocks
	PassEnv []string // credential-looking variables allowed through the scrub
	Retries int      // extra attempts for transient failures of read-only commands
	Sandbox string   // confinement profile; a policy obligation can raise it, never lower it
	// DefaultTimeout applies when, after any runbook has had its say, no timeout
	// is set. Transports for remote callers set it so nothing runs unbounded.
	DefaultTimeout time.Duration
	Approve        Approver // used by Run only; Submit leaves an Ask pending
	Stdout         io.Writer
	Stderr         io.Writer
}

// execParams are the execution settings stored with a run, so an approved run
// executes exactly as it was submitted.
type execParams struct {
	Timeout time.Duration `json:"timeout,omitempty"`
	PassEnv []string      `json:"pass_env,omitempty"`
	Retries int           `json:"retries,omitempty"`
}

// ExecOpts are per-execution options that are not part of the record.
type ExecOpts struct {
	Stdout io.Writer
	Stderr io.Writer
}

type ctxKey int

const principalKey ctxKey = 1

// As returns a context under which runs are attributed to principal.
func As(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

func (s *Service) principalOf(ctx context.Context) string {
	if p, ok := ctx.Value(principalKey).(string); ok && p != "" {
		return p
	}
	return s.principal
}

func newID() string {
	var r [3]byte
	_, _ = rand.Read(r[:])
	return fmt.Sprintf("%011x-%s", time.Now().UnixMilli(), hex.EncodeToString(r[:]))
}

// Parse parses a command without deciding anything.
func (s *Service) Parse(cmd string) *cmdparse.Parsed { return cmdparse.Parse(cmd) }

// Check is a policy dry run: parse and decide, record nothing.
func (s *Service) Check(cmd string) (*cmdparse.Parsed, policy.Decision) {
	p := cmdparse.Parse(cmd)
	var d policy.Decision
	if err := safe.Call("policy.Decide", func() error { d = s.decide(p); return nil }); err != nil {
		// A policy that cannot evaluate must not say yes, and must not say ask
		// either: nobody can review what the policy failed to understand.
		ref, _ := safe.Ref(err)
		return p, policy.Decision{Verdict: policy.Deny, Effect: p.Effect, Reasons: []policy.Reason{{
			Verdict: policy.Deny, Rule: "internal-error", Why: "policy evaluation failed (ref " + ref + ")"}}}
	}
	return p, d
}

// Submit records a run and decides it. The returned run is in one of:
// approved (allowed, or an Ask under shadow mode), pending (an Ask waiting for
// a human) or denied (also reported as ErrDenied). Nothing executes here.
func (s *Service) Submit(ctx context.Context, rq RunRequest) (*store.Run, error) {
	return s.submit(ctx, rq, nil)
}

func (s *Service) submit(ctx context.Context, rq RunRequest, rb *runbook.Runbook) (*store.Run, error) {
	if s.blobs == nil {
		return nil, ErrNoBlobs
	}
	if rq.Sandbox != "" && !cmdrun.ValidSandbox(rq.Sandbox) {
		return nil, fmt.Errorf("unknown sandbox profile %q", rq.Sandbox)
	}
	dir := rq.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}

	parsed, dec := s.Check(rq.Command)
	if rb != nil && dec.Verdict != policy.Deny {
		// A reviewed runbook is the unit of trust: its verdict replaces the
		// generic one, but a policy deny always stands.
		v := policy.Verdict(rb.Verdict)
		dec.Verdict = v
		dec.Reasons = append(dec.Reasons, policy.Reason{Verdict: v, Rule: "runbook:" + rb.Name, Why: "reviewed runbook"})
	}
	sbx := cmdrun.Stricter(dec.Sandbox, rq.Sandbox)
	if rb != nil {
		sbx = cmdrun.Stricter(sbx, rb.Sandbox)
	}
	if sbx == cmdrun.SandboxNone {
		sbx = ""
	}

	principal := s.principalOf(ctx)
	if rq.Timeout == 0 {
		rq.Timeout = rq.DefaultTimeout
	}
	if rq.Timeout < 0 || rq.Timeout > MaxTimeout {
		return nil, fmt.Errorf("%w: timeout must be between 0 and %s", ErrBadRequest, MaxTimeout)
	}
	if n, err := s.store.CountActive(principal); err != nil {
		return nil, err
	} else if n >= s.maxActive {
		return nil, fmt.Errorf("%w (%d active, limit %d)", ErrTooMany, n, s.maxActive)
	}

	pj, _ := json.Marshal(parsed)
	rj, _ := json.Marshal(dec.Reasons)
	ep, _ := json.Marshal(execParams{Timeout: rq.Timeout, PassEnv: rq.PassEnv, Retries: rq.Retries})
	mode := "enforce"
	if rq.Shadow {
		mode = "shadow"
	}
	run := &store.Run{
		ID: newID(), Session: rq.Session, Parent: rq.Parent, Principal: principal, Host: s.host, Cwd: dir,
		Command: rq.Command, Parsed: string(pj), Effect: string(dec.Effect),
		PolicyVerdict: string(dec.Verdict), PolicyReasons: string(rj), PolicyMode: mode, Sandbox: sbx,
		Request: string(ep), State: store.StatePending, CreatedAt: time.Now(),
	}
	switch {
	case dec.Verdict == policy.Deny:
		run.State = store.StateDenied
		run.EndedAt = run.CreatedAt
	case dec.Verdict == policy.Allow:
		run.State, run.Approval, run.ApprovedAt = store.StateApproved, "auto", run.CreatedAt
	case rq.Shadow:
		run.State, run.Approval, run.ApprovedAt = store.StateApproved, "shadow", run.CreatedAt
	}
	if err := s.store.InsertRun(run); err != nil {
		return nil, err
	}
	full, err := s.store.GetRun(run.ID)
	if err != nil {
		return nil, err
	}
	if full.State == store.StateDenied {
		return full, ErrDenied
	}
	return full, nil
}

// Run is the synchronous path used by a terminal: submit, ask the approver if
// the verdict is Ask, execute. The returned run is always the stored record,
// also when the error is ErrDenied or ErrNotAsked.
func (s *Service) Run(ctx context.Context, rq RunRequest) (*store.Run, error) {
	run, err := s.Submit(ctx, rq)
	if err != nil {
		return run, err
	}
	if run.State == store.StatePending {
		if rq.Approve == nil {
			return s.refuse(run, "none")
		}
		parsed, dec := s.Check(run.Command)
		ok, kind := rq.Approve(ctx, Preview{Command: run.Command, Parsed: parsed, Decision: dec})
		if !ok {
			return s.refuse(run, "refused")
		}
		moved, err := s.store.Approve(run.ID, kind, s.principalOf(ctx))
		if err != nil || !moved {
			return nil, errors.Join(err, ErrNotPending)
		}
	}
	return s.Execute(ctx, run.ID, ExecOpts{Stdout: rq.Stdout, Stderr: rq.Stderr})
}

func (s *Service) refuse(run *store.Run, why string) (*store.Run, error) {
	if _, err := s.store.Transition(run.ID, store.StatePending, store.StateDenied); err != nil {
		return nil, err
	}
	if err := s.store.UpdateRun(run.ID, store.StateDenied, why, time.Now()); err != nil {
		return nil, err
	}
	full, err := s.store.GetRun(run.ID)
	if err != nil {
		return nil, err
	}
	return full, ErrNotAsked
}

// Approve moves a pending run to approved. The approval is re-decided against
// the current policy (a rule added since submission can still deny it), must
// arrive inside the approval window, and cannot come from the principal that
// submitted the run. kind records how it was obtained ("tty", "api").
func (s *Service) Approve(ctx context.Context, idFrag, kind string) (*store.Run, error) {
	by := s.principalOf(ctx)
	run, err := s.Get(idFrag)
	if err != nil {
		return nil, err
	}
	if run.State != store.StatePending {
		return run, fmt.Errorf("%w (state %s)", ErrNotPending, run.State)
	}
	if run.Principal == by {
		return run, ErrSelfApproval
	}
	if time.Since(run.CreatedAt) > s.approvalTTL {
		s.settle(run.ID, store.StatePending, store.StateExpired)
		full, _ := s.store.GetRun(run.ID)
		return full, ErrExpired
	}
	if _, dec := s.Check(run.Command); dec.Verdict == policy.Deny {
		s.settle(run.ID, store.StatePending, store.StateDenied)
		full, _ := s.store.GetRun(run.ID)
		return full, ErrDenied
	}
	moved, err := s.store.Approve(run.ID, kind, by)
	if err != nil {
		return nil, err
	}
	if !moved {
		return run, ErrNotPending
	}
	return s.store.GetRun(run.ID)
}

// Reject refuses a pending run.
func (s *Service) Reject(ctx context.Context, idFrag string) (*store.Run, error) {
	by := s.principalOf(ctx)
	run, err := s.Get(idFrag)
	if err != nil {
		return nil, err
	}
	moved, err := s.store.Transition(run.ID, store.StatePending, store.StateDenied)
	if err != nil {
		return nil, err
	}
	if !moved {
		return run, fmt.Errorf("%w (state %s)", ErrNotPending, run.State)
	}
	if err := s.store.UpdateRun(run.ID, store.StateDenied, "rejected:"+by, time.Now()); err != nil {
		return nil, err
	}
	return s.store.GetRun(run.ID)
}

// Pending lists runs waiting for approval, oldest first, with expired ones
// marked as such.
func (s *Service) Pending() ([]*store.Run, error) {
	runs, err := s.store.ListRuns(store.Filter{State: store.StatePending, Limit: 200})
	if err != nil {
		return nil, err
	}
	out := runs[:0]
	for _, r := range runs {
		if time.Since(r.CreatedAt) > s.approvalTTL {
			s.settle(r.ID, store.StatePending, store.StateExpired)
			continue
		}
		out = append(out, r)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Start executes an approved run in the background and returns immediately. The
// run lives as long as the service's base context, not the caller's.
func (s *Service) Start(runID string, opts ExecOpts) error {
	run, err := s.store.GetRun(runID)
	if err != nil {
		return err
	}
	if run.State != store.StateApproved {
		return fmt.Errorf("%w (state %s)", ErrNotRunnable, run.State)
	}
	safe.Go("start run "+runID, func() { _, _ = s.Execute(s.base, runID, opts) }, func(pe *safe.PanicError) {
		s.failInternal(runID, pe) // Execute contains its own panics; this is the last line
	})
	return nil
}

// Cancel stops a running run (killing its process group) or withdraws one that
// has not started. A finished run cannot be canceled.
func (s *Service) Cancel(ctx context.Context, idFrag string, admin bool) (*store.Run, error) {
	run, err := s.Get(idFrag)
	if err != nil {
		return nil, err
	}
	if !admin && run.Principal != s.principalOf(ctx) {
		return run, ErrForbidden
	}
	s.mu.Lock()
	cancel, running := s.running[run.ID]
	if running {
		s.canceled[run.ID] = true
	}
	s.mu.Unlock()
	switch {
	case running:
		cancel()
		return run, nil
	case run.State == store.StatePending || run.State == store.StateApproved:
		if moved, err := s.store.Transition(run.ID, run.State, store.StateCancelled); err != nil || !moved {
			return run, errors.Join(err, ErrNotRunnable)
		}
		_ = s.store.UpdateRun(run.ID, store.StateCancelled, "", time.Now())
		return s.store.GetRun(run.ID)
	}
	return run, fmt.Errorf("run is %s: nothing to cancel", run.State)
}

// Execute runs an approved run: claim it (approved to running, atomically, so a
// run executes at most once), run with linked retry attempts for transient
// failures of read-only commands, classify, redact and store the output.
func (s *Service) Execute(ctx context.Context, runID string, opts ExecOpts) (run *store.Run, err error) {
	s.execs.Add(1)
	defer s.execs.Done()
	defer func() {
		if v := recover(); v != nil {
			// Whatever panicked, the run must not stay "running" and the caller
			// must get the stored record and a ref, never the panic.
			pe := safe.CaptureFor("execute run "+runID, runID, v)
			s.failInternal(runID, pe)
			run, _ = s.store.GetRun(runID)
			err = pe
		}
	}()
	return s.execute(ctx, runID, opts)
}

// failInternal ends a run that hit a bug in conduit itself. It only acts on a
// run that is still running or approved, so it cannot overwrite a real outcome.
func (s *Service) failInternal(runID string, pe *safe.PanicError) {
	msg := "internal error while executing this command; see panic ref"
	for _, from := range []string{store.StateRunning, store.StateApproved} {
		if moved, err := s.store.Fail(runID, from, "internal_error", msg, pe.Rec.Ref, time.Now()); err == nil && moved {
			return
		}
	}
}

func (s *Service) execute(ctx context.Context, runID string, opts ExecOpts) (*store.Run, error) {
	run, err := s.store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if run.State == store.StateApproved {
		// Approval is not a bearer token for later: a human approval goes stale,
		// and a Deny added to the policy since stops a run that was approved before it.
		if (run.Approval != "auto" && run.Approval != "shadow") && time.Since(run.ApprovedAt) > s.approvalTTL {
			s.settle(runID, store.StateApproved, store.StateExpired)
			full, _ := s.store.GetRun(runID)
			return full, ErrExpired
		}
		if _, dec := s.Check(run.Command); dec.Verdict == policy.Deny {
			s.settle(runID, store.StateApproved, store.StateDenied)
			full, _ := s.store.GetRun(runID)
			return full, ErrDenied
		}
	}
	self := os.Getpid()
	selfStart, _ := proc.StartTime(self)
	moved, err := s.store.MarkRunning(runID, self, selfStart, time.Now())
	if err != nil {
		return nil, err
	}
	if !moved {
		return run, fmt.Errorf("%w (state %s)", ErrNotRunnable, run.State)
	}
	var ep execParams
	_ = json.Unmarshal([]byte(run.Request), &ep)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.running[runID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, runID)
		delete(s.canceled, runID)
		s.mu.Unlock()
	}()

	// Prove liveness while the command runs, so a crash that leaves this run
	// "running" can be recognized by the reaper.
	stopBeat := make(chan struct{})
	defer close(stopBeat)
	safe.Go("heartbeat "+runID, func() {
		t := time.NewTicker(s.heartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-t.C:
				_ = s.store.Heartbeat(runID, time.Now())
			}
		}
	}, nil)

	idempotent := cmdparse.Effect(run.Effect) == cmdparse.EffectRead || cmdparse.Effect(run.Effect) == cmdparse.EffectNone
	retries := 0
	if idempotent {
		retries = ep.Retries
	}
	var last string
	for n, retryOf := 1, 0; ; n++ {
		res, err := cmdrun.Run(ctx, cmdrun.Spec{
			Command: run.Command, Dir: run.Cwd, Timeout: ep.Timeout, Env: s.env, PassEnv: ep.PassEnv,
			Sandbox: run.Sandbox, Protect: s.protect, Stdout: opts.Stdout, Stderr: opts.Stderr,
			OnStart: func(pid int) {
				start, _ := proc.StartTime(pid)
				_ = s.store.SetProcess(runID, pid, start)
			},
		})
		if err != nil {
			return s.finish(run, store.StateFailed, err)
		}
		at, err := s.recordAttempt(context.WithoutCancel(ctx), run.ID, n, retryOf, res)
		if err != nil {
			return s.finish(run, store.StateFailed, err)
		}
		last = at.Class
		if cmdrun.Retryable(last) && retries > 0 && ctx.Err() == nil {
			retries--
			retryOf = n
			select {
			case <-time.After(time.Duration(n) * 500 * time.Millisecond):
				continue
			case <-ctx.Done():
			}
		}
		break
	}
	s.mu.Lock()
	wasCanceled := s.canceled[runID]
	s.mu.Unlock()
	state := store.StateDone
	switch {
	case wasCanceled:
		state = store.StateCancelled
	case last != cmdrun.ClassOK:
		state = store.StateFailed
	}
	return s.finish(run, state, nil)
}

func (s *Service) finish(run *store.Run, state string, cause error) (*store.Run, error) {
	if err := s.store.UpdateRun(run.ID, state, "", time.Now()); err != nil {
		return nil, errors.Join(cause, err)
	}
	full, err := s.store.GetRun(run.ID)
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	return full, cause
}

func (s *Service) recordAttempt(ctx context.Context, runID string, n, retryOf int, res *cmdrun.Result) (*store.Attempt, error) {
	out, nOut := cmdrun.Redact(res.Stdout)
	errb, nErr := cmdrun.Redact(res.Stderr)
	a := &store.Attempt{
		RunID: runID, N: n, RetryOf: retryOf, Started: res.Started, Ended: res.Ended,
		ExitCode: res.ExitCode, Signal: res.Signal, Class: cmdrun.Classify(res), StartErr: res.StartErr,
		StdoutBytes: res.StdoutBytes, StderrBytes: res.StderrBytes, Truncated: res.Truncated, Redactions: nOut + nErr,
	}
	sj, _ := json.Marshal(res.ScrubbedEnv)
	a.Scrubbed = string(sj)
	var err error
	if len(out) > 0 {
		if a.StdoutRef, err = s.blobs.Put(ctx, out, blob.Meta{Source: fmt.Sprintf("conduit://run/%s/%d/stdout", runID, n), Tags: "conduit stdout"}); err != nil {
			return nil, fmt.Errorf("store stdout: %w", err)
		}
	}
	if len(errb) > 0 {
		if a.StderrRef, err = s.blobs.Put(ctx, errb, blob.Meta{Source: fmt.Sprintf("conduit://run/%s/%d/stderr", runID, n), Tags: "conduit stderr"}); err != nil {
			return nil, fmt.Errorf("store stderr: %w", err)
		}
	}
	if err := s.store.InsertAttempt(a); err != nil {
		return nil, err
	}
	return a, nil
}

// settle moves a run between states and stamps its end, only if it is still in
// the expected state: another process may have moved it in the meantime, and
// that outcome must not be overwritten.
func (s *Service) settle(id, from, to string) {
	if moved, err := s.store.Transition(id, from, to); err == nil && moved {
		_ = s.store.UpdateRun(id, to, "", time.Now())
	}
}

// MaxTimeout is the longest a caller may ask a command to run.
const MaxTimeout = time.Hour

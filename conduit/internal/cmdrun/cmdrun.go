// Package cmdrun is the exec wrapper: it runs one command under a timeout in
// its own process group, with a scrubbed environment, captures stdout and
// stderr separately and bounded, and reports exit code and signal faithfully.
//
// It does not decide whether a command may run (internal/policy does) and it is
// not the final boundary: a sandbox profile belongs here next (go-sandbox).
package cmdrun

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hollis-labs/libs/core/panics"
)

// DefaultMaxCapture bounds each captured stream; the rest is counted, not kept.
const DefaultMaxCapture = 8 << 20

// KillGrace is how long a timed-out group gets between SIGTERM and SIGKILL.
const KillGrace = 2 * time.Second

// Spec is one attempt.
type Spec struct {
	Command    string        // run as bash -c Command
	Dir        string        // working directory; "" = current
	Timeout    time.Duration // 0 = none
	Env        []string      // base environment (KEY=VAL); scrubbed by Run
	PassEnv    []string      // names exempt from scrubbing
	MaxCapture int
	Sandbox    string    // profile name; "" or "none" runs unconfined
	Protect    []string  // control-plane paths the protect profile makes read-only
	Stdout     io.Writer // optional live tee
	Stderr     io.Writer // optional live tee
	// OnStart is called with the pid (also the process group id) as soon as the
	// command has started, so a crash of the owner can be cleaned up later.
	OnStart func(pid int)
}

// Result is what happened.
type Result struct {
	Started, Ended time.Time
	ExitCode       int    // -1 when killed by a signal or never started
	Signal         string // e.g. "terminated", "killed"
	TimedOut       bool
	StartErr       string // exec failed before the command ran
	Stdout, Stderr []byte
	StdoutBytes    int64 // total produced, including the part not kept
	StderrBytes    int64
	Truncated      bool
	ScrubbedEnv    []string // names removed from the environment
	Sandbox        string   // profile the command ran under, "" if none
	SandboxErr     string   // set when a required sandbox could not be applied; the command did not run
}

// OK reports a clean exit.
func (r *Result) OK() bool {
	return r.StartErr == "" && !r.TimedOut && r.Signal == "" && r.ExitCode == 0
}

type capture struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	max   int
	total int64
	tee   io.Writer
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(len(p))
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	if c.tee != nil {
		c.teeWrite(p)
	}
	return len(p), nil
}

// teeWrite copies to the live writer. os/exec calls Write from its own copying
// goroutine, where a panic would kill the whole process and cannot be recovered
// by the caller, so a failing or panicking writer is contained here and the tee
// is switched off; the run and its captured output carry on.
func (c *capture) teeWrite(p []byte) {
	defer func() {
		if v := recover(); v != nil {
			_ = panics.Capture("cmdrun live output writer", v)
			c.tee = nil
		}
	}()
	if _, err := c.tee.Write(p); err != nil {
		c.tee = nil // a closed terminal must not fail the run
	}
}

// Run executes the spec and always returns a Result; the error is reserved for
// a failure of the wrapper itself, not of the command.
func Run(ctx context.Context, sp Spec) (res *Result, err error) {
	var cmd *exec.Cmd
	// If anything in here panics after the command has started, the process
	// group must not outlive us: kill it, then report the panic as a wrapper error.
	defer func() {
		if v := recover(); v != nil {
			if cmd != nil && cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				go func() { _ = cmd.Wait() }()
			}
			res, err = nil, panics.Capture("cmdrun.Run", v)
		}
	}()
	limit := sp.MaxCapture
	if limit <= 0 {
		limit = DefaultMaxCapture
	}
	env, scrubbed := ScrubEnv(sp.Env, sp.PassEnv)

	cmd = exec.Command("/bin/bash", "-c", sp.Command) //nolint:gosec // running the caller's command is this package's job; policy decides before it is called
	cmd.Dir = sp.Dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := &capture{max: limit, tee: sp.Stdout}
	errc := &capture{max: limit, tee: sp.Stderr}
	cmd.Stdout, cmd.Stderr = out, errc
	cmd.Stdin = nil

	res = &Result{ScrubbedEnv: scrubbed, Started: time.Now(), ExitCode: -1}
	if sp.Sandbox != "" && sp.Sandbox != SandboxNone {
		ws := sp.Dir
		if ws == "" {
			ws, _ = os.Getwd()
		}
		cleanup, err := applySandbox(cmd, sp.Sandbox, ws, sp.Protect)
		if err != nil {
			res.SandboxErr = err.Error()
			res.StartErr = "sandbox: " + err.Error()
			res.Ended = time.Now()
			return res, nil
		}
		defer cleanup()
		res.Sandbox = sp.Sandbox
	}

	if err := cmd.Start(); err != nil {
		res.StartErr = err.Error()
		res.Ended = time.Now()
		return res, nil
	}

	if sp.OnStart != nil {
		func() {
			defer func() {
				if v := recover(); v != nil {
					_ = panics.Capture("cmdrun OnStart", v)
				}
			}()
			sp.OnStart(cmd.Process.Pid)
		}()
	}

	done := make(chan error, 1)
	panics.Go("cmdrun wait", func() { done <- cmd.Wait() }, func(pe *panics.Error) { done <- pe })

	var timeout <-chan time.Time
	if sp.Timeout > 0 {
		t := time.NewTimer(sp.Timeout)
		defer t.Stop()
		timeout = t.C
	}

	var waitErr error
	select {
	case waitErr = <-done:
	case <-timeout:
		res.TimedOut = true
		waitErr = killGroup(cmd, done)
	case <-ctx.Done():
		res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		waitErr = killGroup(cmd, done)
	}
	res.Ended = time.Now()

	var ee *exec.ExitError
	switch {
	case waitErr == nil:
		res.ExitCode = 0
	case errors.As(waitErr, &ee):
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = strings.ToLower(ws.Signal().String())
			res.ExitCode = 128 + int(ws.Signal())
		} else {
			res.ExitCode = ee.ExitCode()
		}
	default:
		res.StartErr = waitErr.Error()
	}

	res.Stdout, res.Stderr = out.buf.Bytes(), errc.buf.Bytes()
	res.StdoutBytes, res.StderrBytes = out.total, errc.total
	res.Truncated = out.total > int64(limit) || errc.total > int64(limit)
	return res, nil
}

// killGroup terminates the whole process group: TERM, then KILL after a grace.
func killGroup(cmd *exec.Cmd, done <-chan error) error {
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case err := <-done:
		_ = syscall.Kill(-pgid, syscall.SIGKILL) // stragglers that ignored TERM in the group
		return err
	case <-time.After(KillGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return <-done
	}
}

// Environ returns the current process environment.
func Environ() []string { return os.Environ() }

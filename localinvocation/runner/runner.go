// Package runner executes a structured local invocation and keeps its output
// independently of the client connection. It does not make policy decisions.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hollis-labs/tools/localinvocation/invocation"
)

const DefaultReadLimit = 64 << 10

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// EnvResolver exchanges an environment lease reference for execution values.
// Values are never put in the invocation record or outcome.
type EnvResolver func(ctx context.Context, ref string) ([]string, error)

type Options struct {
	PTY     bool          `json:"pty,omitempty"`
	Rows    uint16        `json:"rows,omitempty"`
	Cols    uint16        `json:"cols,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

type OutcomeKind string

const (
	OutcomeExit      OutcomeKind = "exit"
	OutcomeSignal    OutcomeKind = "signal"
	OutcomeToolError OutcomeKind = "tool_error"
	OutcomeTransport OutcomeKind = "transport_error"
	OutcomeTimeout   OutcomeKind = "timeout"
	OutcomeCancelled OutcomeKind = "cancelled"
	OutcomeUnknown   OutcomeKind = "unknown"
)

type Outcome struct {
	Kind            OutcomeKind `json:"kind"`
	ExitCode        *int        `json:"exit_code,omitempty"`
	Signal          string      `json:"signal,omitempty"`
	Error           string      `json:"error,omitempty"`
	CancelRequested bool        `json:"cancel_requested,omitempty"`
	CancelConfirmed bool        `json:"cancel_confirmed,omitempty"`
	Started         time.Time   `json:"started"`
	Ended           time.Time   `json:"ended"`
}

type Result struct {
	ID        string            `json:"id"`
	Done      bool              `json:"done"`
	Outcome   Outcome           `json:"outcome"`
	OutputRef map[string]string `json:"output_refs"`
}

// OutputPage is a bounded byte slice; NextCursor is the first unread byte.
// Truncated means more bytes existed at the time of this read.
type OutputPage struct {
	Stream     string `json:"stream"`
	Combined   bool   `json:"combined"`
	Data       []byte `json:"data"`
	NextCursor int64  `json:"next_cursor"`
	Truncated  bool   `json:"truncated"`
	Ref        string `json:"ref"`
}

type diskRecord struct {
	Command invocation.Command `json:"command"`
	Options Options            `json:"options"`
	Result  Result             `json:"result"`
}

type run struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	ptyFile  *os.File
	outputs  []*os.File
	copyDone chan struct{}
	record   diskRecord
	done     chan struct{}
	timer    *time.Timer
	cancel   bool
	timedOut bool
	exited   bool
}

// Manager owns running children. Reopening the same root allows output replay;
// a run with no final record is reported unknown and is never retried.
type Manager struct {
	root    string
	resolve EnvResolver
	mu      sync.Mutex
	runs    map[string]*run
	grace   time.Duration
}

func New(root string, resolve EnvResolver) (*Manager, error) {
	if root == "" {
		return nil, errors.New("output root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Manager{root: root, resolve: resolve, runs: make(map[string]*run), grace: 2 * time.Second}, nil
}

func (m *Manager) Start(ctx context.Context, command invocation.Command, opts Options) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(command.Argv) == 0 || command.Argv[0] == "" {
		return "", errors.New("argv[0] required")
	}
	id := command.IDs.AttemptID
	if !validID.MatchString(id) || id == "" {
		return "", errors.New("invalid invocation id")
	}
	var env []string
	if command.EnvironmentRef != "" {
		if m.resolve == nil {
			return "", errors.New("environment resolver required")
		}
		var err error
		env, err = m.resolve(ctx, command.EnvironmentRef)
		if err != nil {
			return "", fmt.Errorf("resolve environment: %w", err)
		}
	} else {
		env = os.Environ()
	}
	env = append(append([]string(nil), env...), invocation.ChildEnv(command.IDs)...)
	path := m.runPath(id)
	if err := os.Mkdir(path, 0700); err != nil {
		return "", err // includes an existing ID: never execute twice
	}
	cmd := exec.Command(command.Argv[0], command.Argv[1:]...)
	cmd.Dir, cmd.Env = command.CWD, env
	r := &run{cmd: cmd, done: make(chan struct{})}
	r.record = diskRecord{Command: command, Options: opts, Result: Result{ID: id, OutputRef: map[string]string{}}}
	// Persist before launching: after a host crash the attempt is unknown,
	// including a crash in the gap between process start and final receipt.
	if err := m.writeRecord(id, r.record); err != nil {
		return "", err
	}
	var err error
	if opts.PTY {
		rows, cols := opts.Rows, opts.Cols
		if rows == 0 {
			rows = 24
		}
		if cols == 0 {
			cols = 80
		}
		r.ptyFile, err = pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
		if err == nil {
			var out *os.File
			out, err = os.OpenFile(filepath.Join(path, "combined"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				r.outputs = append(r.outputs, out)
				r.record.Result.OutputRef["combined"] = "runner://" + id + "/combined"
				r.copyDone = make(chan struct{})
				go func() {
					_, _ = io.Copy(out, r.ptyFile)
					close(r.copyDone)
				}()
			}
		}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		for _, stream := range []string{"stdout", "stderr"} {
			var out *os.File
			out, err = os.OpenFile(filepath.Join(path, stream), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				break
			}
			r.outputs = append(r.outputs, out)
			r.record.Result.OutputRef[stream] = "runner://" + id + "/" + stream
		}
		if err == nil {
			cmd.Stdout, cmd.Stderr = r.outputs[0], r.outputs[1]
			err = cmd.Start()
		}
	}
	if err != nil {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
		for _, out := range r.outputs {
			_ = out.Close()
		}
		if r.ptyFile != nil {
			_ = r.ptyFile.Close()
		}
		r.record.Result.Done = true
		r.record.Result.Outcome = Outcome{Kind: OutcomeToolError, Error: err.Error(), Ended: time.Now()}
		if writeErr := m.writeRecord(id, r.record); writeErr != nil {
			return "", writeErr
		}
		return id, nil
	}
	r.record.Result.Outcome.Started = time.Now()
	if err = m.writeRecord(id, r.record); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		if r.ptyFile != nil {
			_ = r.ptyFile.Close()
		}
		for _, out := range r.outputs {
			_ = out.Close()
		}
		return "", err
	}
	m.mu.Lock()
	m.runs[id] = r
	m.mu.Unlock()
	if opts.Timeout > 0 {
		r.timer = time.AfterFunc(opts.Timeout, func() { _ = m.cancel(id, true) })
	}
	go m.wait(id, r)
	return id, nil
}

// Run is the blocking path for a terminal or shim. The context governs
// environment resolution and process start; dropping the caller does not kill
// the invocation. The caller may separately request Cancel using the ID.
func (m *Manager) Run(ctx context.Context, command invocation.Command, opts Options) (Result, error) {
	id, err := m.Start(ctx, command, opts)
	if err != nil {
		return Result{}, err
	}
	return m.Wait(id)
}

// Wait returns the final result. If this manager no longer owns the process,
// Result makes an unfinished persisted attempt unknown instead of retrying it.
func (m *Manager) Wait(id string) (Result, error) {
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r != nil {
		<-r.done
	}
	return m.Result(id)
}

func (m *Manager) wait(id string, r *run) {
	err := r.cmd.Wait()
	r.mu.Lock()
	r.exited = true
	r.mu.Unlock()
	if r.copyDone != nil {
		<-r.copyDone
	}
	if r.ptyFile != nil {
		_ = r.ptyFile.Close()
	}
	for _, out := range r.outputs {
		_ = out.Close()
	}
	r.mu.Lock()
	if r.timer != nil {
		r.timer.Stop()
	}
	o := r.record.Result.Outcome
	o.Ended = time.Now()
	o.CancelRequested = r.cancel
	var exit *exec.ExitError
	switch {
	case r.timedOut:
		o.Kind = OutcomeTimeout
	case r.cancel && errors.As(err, &exit):
		o.Kind, o.CancelConfirmed = OutcomeCancelled, true
	case err == nil:
		code := 0
		o.Kind, o.ExitCode = OutcomeExit, &code
	case errors.As(err, &exit):
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			o.Kind, o.Signal = OutcomeSignal, status.Signal().String()
		} else {
			code := exit.ExitCode()
			o.Kind, o.ExitCode = OutcomeExit, &code
		}
	default:
		o.Kind, o.Error = OutcomeToolError, err.Error()
	}
	r.record.Result.Outcome, r.record.Result.Done = o, true
	_ = m.writeRecord(id, r.record)
	r.mu.Unlock()
	close(r.done)
	m.mu.Lock()
	delete(m.runs, id)
	m.mu.Unlock()
}

func (m *Manager) Cancel(id string) error { return m.cancel(id, false) }

func (m *Manager) cancel(id string, timeout bool) error {
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r == nil {
		return errors.New("invocation is not running")
	}
	r.mu.Lock()
	if r.cancel || r.timedOut || r.exited {
		r.mu.Unlock()
		return nil
	}
	r.cancel, r.timedOut = !timeout, timeout
	if !timeout {
		r.record.Result.Outcome.CancelRequested = true
		if err := m.writeRecord(id, r.record); err != nil {
			r.cancel = false
			r.mu.Unlock()
			return err
		}
	}
	pid := r.cmd.Process.Pid
	done := r.done
	grace := m.grace
	r.mu.Unlock()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	go func() {
		t := time.NewTimer(grace)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
	return nil
}

func (m *Manager) Resize(id string, rows, cols uint16) error {
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r == nil || r.ptyFile == nil {
		return errors.New("running PTY not found")
	}
	return pty.Setsize(r.ptyFile, &pty.Winsize{Rows: rows, Cols: cols})
}

func (m *Manager) Result(id string) (Result, error) {
	if !validID.MatchString(id) {
		return Result{}, errors.New("invalid invocation id")
	}
	m.mu.Lock()
	r := m.runs[id]
	m.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		result := r.record.Result
		result.OutputRef = maps.Clone(result.OutputRef)
		r.mu.Unlock()
		return result, nil
	}
	b, err := os.ReadFile(filepath.Join(m.runPath(id), "record.json"))
	if err != nil {
		return Result{}, err
	}
	var record diskRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return Result{}, err
	}
	if record.Result.OutputRef == nil {
		record.Result.OutputRef = map[string]string{}
	}
	for _, stream := range []string{"stdout", "stderr", "combined"} {
		if _, err := os.Stat(filepath.Join(m.runPath(id), stream)); err == nil {
			record.Result.OutputRef[stream] = "runner://" + id + "/" + stream
		}
	}
	if !record.Result.Done {
		// The caller cannot infer whether an effect happened after this record.
		record.Result.Done = true
		record.Result.Outcome.Kind = OutcomeUnknown
		record.Result.Outcome.Error = "final result unavailable"
	}
	return record.Result, nil
}

// Observe reads one stream from a byte cursor. It never consumes the stream.
func (m *Manager) Observe(id, stream string, cursor int64, maxBytes int) (OutputPage, error) {
	if !validID.MatchString(id) || cursor < 0 {
		return OutputPage{}, errors.New("invalid id or cursor")
	}
	result, err := m.Result(id)
	if err != nil {
		return OutputPage{}, err
	}
	if _, ok := result.OutputRef[stream]; !ok {
		return OutputPage{}, errors.New("stream not available")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultReadLimit
	}
	if maxBytes > DefaultReadLimit {
		maxBytes = DefaultReadLimit
	}
	f, err := os.Open(filepath.Join(m.runPath(id), stream))
	if err != nil {
		return OutputPage{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return OutputPage{}, err
	}
	if cursor > stat.Size() {
		return OutputPage{}, errors.New("cursor beyond output")
	}
	buf := make([]byte, min(int64(maxBytes), stat.Size()-cursor))
	n, err := f.ReadAt(buf, cursor)
	if err != nil && err != io.EOF {
		return OutputPage{}, err
	}
	buf = buf[:n]
	next := cursor + int64(n)
	return OutputPage{Stream: stream, Combined: stream == "combined", Data: buf, NextCursor: next, Truncated: next < stat.Size(), Ref: result.OutputRef[stream]}, nil
}

func (m *Manager) runPath(id string) string { return filepath.Join(m.root, id) }

func (m *Manager) writeRecord(id string, record diskRecord) error {
	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(m.runPath(id), "record.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

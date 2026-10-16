// Package safe contains panics at the boundaries where they would otherwise
// take down a process or leave work half-done.
//
// A panic becomes a PanicError: a ref, the place it happened, and a message and
// stack that are redacted, stripped of control characters and bounded. The full
// record goes to the reporter (the log and the database, for the operator). What
// crosses a transport boundary is the ref and nothing else: panic text can carry
// anything the failing code was holding, including secrets and submitter input.
//
// Boundaries that matter here: every goroutine (an unrecovered panic in one kills
// the whole process), every request and tool call (one bad request must not stop
// the server), the parser and policy (they must fail closed), and the executor
// (it must not leave a run "running" or its process group alive).
package safe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/hollis-labs/conduit/internal/redact"
)

const (
	maxValueRunes = 2048
	maxStackRunes = 8192
	maxChainDepth = 16
)

// Record is what is kept about a panic.
type Record struct {
	Ref   string
	At    time.Time
	Where string // the boundary that caught it, e.g. "http POST /v1/exec"
	Value string // redacted, bounded
	Stack string // redacted, bounded; for the operator only
	RunID string // set when the panic happened while executing a run
}

// PanicError is a recovered panic as an error. Its Error() is safe to show to a
// remote caller: it carries the ref and the boundary name only.
type PanicError struct{ Rec Record }

func (e *PanicError) Error() string {
	return fmt.Sprintf("internal error (ref %s)", e.Rec.Ref)
}

// Ref extracts the panic ref from err, if err wraps a PanicError.
func Ref(err error) (string, bool) {
	var pe *PanicError
	if errors.As(err, &pe) {
		return pe.Rec.Ref, true
	}
	return "", false
}

// Reporter receives each recovered panic. It must not panic; Capture guards it
// anyway.
type Reporter interface{ Report(Record) }

type reporterFunc func(Record)

func (f reporterFunc) Report(r Record) { f(r) }

var (
	mu       sync.RWMutex
	reporter Reporter = reporterFunc(func(r Record) {
		// Default: stderr, so nothing is silent even before main wires a reporter.
		println("conduit: panic", r.Ref, "in", r.Where+":", r.Value)
	})
)

// SetReporter installs the reporter (main wires one that logs and persists).
func SetReporter(r Reporter) {
	mu.Lock()
	defer mu.Unlock()
	reporter = r
}

func newRef() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "pnc_" + hex.EncodeToString(b[:])
}

// describe renders a recovered value without trusting it: a panic value can be
// any type, and its String/Error method can itself panic.
func describe(v any) (out string) {
	defer func() {
		if recover() != nil {
			out = "<value could not be rendered>"
		}
	}()
	switch x := v.(type) {
	case nil:
		return "<nil panic>"
	case runtime.Error:
		return x.Error()
	case error:
		return unwrapChain(x)
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%T: %v", v, v)
	}
}

// unwrapChain renders an error and its causes, with a depth limit and a seen
// set so a cyclic chain cannot hang the handler.
func unwrapChain(err error) string {
	seen := map[error]bool{}
	out := ""
	for depth := 0; err != nil && depth < maxChainDepth; depth++ {
		if seen[err] {
			break
		}
		func() {
			defer func() {
				if recover() != nil {
					out += "<error could not be rendered>"
					err = nil
				}
			}()
			seen[err] = true
			if out != "" {
				out += ": "
			}
			out += err.Error()
			err = errors.Unwrap(err)
		}()
	}
	return out
}

// Capture builds the record for a recovered value and hands it to the reporter.
// Call it from a deferred function with the value recover() returned.
func Capture(where string, v any) *PanicError { return capture(where, "", v) }

func capture(where, runID string, v any) *PanicError {
	rec := Record{
		Ref:   newRef(),
		At:    time.Now(),
		Where: where,
		RunID: runID,
		Value: redact.Text(describe(v), maxValueRunes),
		Stack: redact.Text(string(debug.Stack()), maxStackRunes),
	}
	pe := &PanicError{Rec: rec}
	func() {
		defer func() { _ = recover() }() // a failing reporter must not turn one panic into two
		mu.RLock()
		r := reporter
		mu.RUnlock()
		r.Report(rec)
	}()
	return pe
}

// Call runs fn and converts a panic into a PanicError.
func Call(where string, fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = Capture(where, v)
		}
	}()
	return fn()
}

// Go runs fn in a new goroutine that cannot take the process down. A panic is
// captured and, if onPanic is non-nil, handed to it (for example to mark a run
// failed).
func Go(where string, fn func(), onPanic func(*PanicError)) {
	go func() {
		defer func() {
			if v := recover(); v != nil {
				pe := Capture(where, v)
				if onPanic != nil {
					func() {
						defer func() { _ = recover() }()
						onPanic(pe)
					}()
				}
			}
		}()
		fn()
	}()
}

// CaptureFor is Capture for a panic that happened while executing a run, so the
// record points at it.
func CaptureFor(where, runID string, v any) *PanicError {
	return capture(where, runID, v)
}

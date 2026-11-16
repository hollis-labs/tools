package runner

import (
	"encoding/json"
	"errors"
	"fmt"
)

// resultEnvelope is the CLI projection of libs/agent/resultenv/v1. The full
// byte streams remain in the runner's output refs; this is a bounded view.
type resultEnvelope struct {
	Version         string      `json:"version"`
	Outcome         string      `json:"outcome"`
	ExitCode        *int        `json:"exit_code,omitempty"`
	Signal          string      `json:"signal,omitempty"`
	CancelRequested *bool       `json:"cancel_requested,omitempty"`
	CancelConfirmed *bool       `json:"cancel_confirmed,omitempty"`
	Stdout          string      `json:"stdout,omitempty"`
	Stderr          string      `json:"stderr,omitempty"`
	CombinedOutput  string      `json:"combined_output,omitempty"`
	Truncation      *truncation `json:"truncation,omitempty"`
	Error           *failure    `json:"error,omitempty"`
}

type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type truncation struct {
	Truncated bool   `json:"truncated"`
	Cursor    string `json:"cursor,omitempty"`
}

// Envelope returns a resultenv/v1 JSON envelope with bounded text output.
// Binary output is retained byte-for-byte by Observe; this view is for JSON
// consumers. A final envelope is unavailable while the process is running.
func (m *Manager) Envelope(id string, maxBytes int) ([]byte, error) {
	r, err := m.Result(id)
	if err != nil {
		return nil, err
	}
	if !r.Done {
		return nil, errors.New("invocation still running")
	}
	e := resultEnvelope{Version: "resultenv/v1", ExitCode: r.Outcome.ExitCode, Signal: r.Outcome.Signal}
	if r.Outcome.CancelRequested {
		e.CancelRequested = &r.Outcome.CancelRequested
		e.CancelConfirmed = &r.Outcome.CancelConfirmed
	}
	switch r.Outcome.Kind {
	case OutcomeExit:
		if r.Outcome.ExitCode == nil {
			return nil, errors.New("exit outcome missing code")
		}
		if *r.Outcome.ExitCode == 0 {
			e.Outcome = "success"
		} else {
			e.Outcome = "nonzero_exit"
		}
	case OutcomeSignal:
		e.Outcome = "signal"
	case OutcomeToolError:
		e.Outcome = "tool_error"
		e.Error = &failure{Code: "exec_failed", Message: r.Outcome.Error}
	case OutcomeTransport:
		e.Outcome = "transport_error"
		e.Error = &failure{Code: "transport_error", Message: r.Outcome.Error}
	case OutcomeTimeout:
		e.Outcome = "timeout"
		e.Error = &failure{Code: "deadline_exceeded", Message: "invocation timed out"}
	case OutcomeCancelled:
		e.Outcome = "cancelled"
		e.Error = &failure{Code: "cancelled", Message: "cancellation confirmed"}
	case OutcomeUnknown:
		e.Outcome = "unknown"
		e.Error = &failure{Code: "result_lost", Message: "effect may have completed; reconcile before retry"}
	default:
		return nil, fmt.Errorf("unsupported outcome %q", r.Outcome.Kind)
	}
	cursors := map[string]int64{}
	truncated := false
	for _, stream := range []string{"stdout", "stderr", "combined"} {
		if _, ok := r.OutputRef[stream]; !ok {
			continue
		}
		page, err := m.Observe(id, stream, 0, maxBytes)
		if err != nil {
			return nil, err
		}
		switch stream {
		case "stdout":
			e.Stdout = string(page.Data)
		case "stderr":
			e.Stderr = string(page.Data)
		case "combined":
			e.CombinedOutput = string(page.Data)
		}
		cursors[stream] = page.NextCursor
		truncated = truncated || page.Truncated
	}
	if truncated {
		cursor, err := json.Marshal(cursors)
		if err != nil {
			return nil, err
		}
		e.Truncation = &truncation{Truncated: true, Cursor: string(cursor)}
	}
	return json.Marshal(e)
}

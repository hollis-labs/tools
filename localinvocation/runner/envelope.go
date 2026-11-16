package runner

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/libs/agent/resultenv"
)

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
	e := resultenv.Envelope{Version: resultenv.Version, ExitCode: r.Outcome.ExitCode, Signal: r.Outcome.Signal}
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
			e.Outcome = resultenv.Success
		} else {
			e.Outcome = resultenv.NonzeroExit
		}
	case OutcomeSignal:
		e.Outcome = resultenv.Signal
	case OutcomeToolError:
		e.Outcome = resultenv.ToolError
		e.Error = &resultenv.Failure{Code: "exec_failed", Message: r.Outcome.Error}
	case OutcomeTransport:
		e.Outcome = resultenv.TransportError
		e.Error = &resultenv.Failure{Code: "transport_error", Message: r.Outcome.Error}
	case OutcomeTimeout:
		e.Outcome = resultenv.Timeout
		e.Error = &resultenv.Failure{Code: "deadline_exceeded", Message: "invocation timed out"}
	case OutcomeCancelled:
		e.Outcome = resultenv.Cancelled
		e.Error = &resultenv.Failure{Code: "cancelled", Message: "cancellation confirmed"}
	case OutcomeUnknown:
		e.Outcome = resultenv.Unknown
		e.Error = &resultenv.Failure{Code: "result_lost", Message: "effect may have completed; reconcile before retry"}
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
		e.Truncation = &resultenv.Truncation{Truncated: true, Cursor: string(cursor)}
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

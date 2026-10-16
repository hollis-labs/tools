package service

import (
	"context"
	"fmt"

	"github.com/hollis-labs/conduit/internal/runbook"
	"github.com/hollis-labs/conduit/internal/store"
)

// Runbooks lists the named operations this service offers.
func (s *Service) Runbooks() []*runbook.Runbook {
	if s.runbooks == nil {
		return nil
	}
	return s.runbooks.List()
}

// SubmitRunbook renders a runbook with validated params and submits the result.
// The runbook's timeout, directory, retries and sandbox apply unless the request
// already sets them (a request can tighten the sandbox, never loosen it).
func (s *Service) SubmitRunbook(ctx context.Context, name string, params map[string]string, rq RunRequest) (*store.Run, error) {
	if s.runbooks == nil {
		return nil, fmt.Errorf("%w: %q", ErrNoRunbook, name)
	}
	rb, ok := s.runbooks.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoRunbook, name)
	}
	cmd, err := rb.Render(params)
	if err != nil {
		return nil, err
	}
	rq.Command = cmd
	if rq.Timeout == 0 {
		rq.Timeout = rb.Timeout
	}
	if rq.Dir == "" {
		rq.Dir = rb.Dir
	}
	if rq.Retries == 0 {
		rq.Retries = rb.Retries
	}
	if rq.Parent == "" {
		rq.Parent = "runbook:" + rb.Name
	}
	return s.submit(ctx, rq, rb)
}

// Runbook returns one runbook by name.
func (s *Service) Runbook(name string) (*runbook.Runbook, bool) {
	if s.runbooks == nil {
		return nil, false
	}
	return s.runbooks.Get(name)
}

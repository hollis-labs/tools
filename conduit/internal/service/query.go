package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/store"
)

// Get returns a run by id, prefix or suffix.
func (s *Service) Get(idFrag string) (*store.Run, error) {
	id, err := s.store.ResolveID(idFrag)
	if err != nil {
		return nil, err
	}
	return s.store.GetRun(id)
}

// List returns runs newest first.
func (s *Service) List(session, state string, limit int) ([]*store.Run, error) {
	return s.store.ListRuns(store.Filter{Session: session, State: state, Limit: limit})
}

// Recall returns the stored output of a run: stream is "stdout" or "stderr";
// attempt 0 means the last attempt. The bytes are what was stored, after
// redaction.
func (s *Service) Recall(ctx context.Context, idFrag, stream string, attempt int) ([]byte, error) {
	if s.blobs == nil {
		return nil, ErrNoBlobs
	}
	run, err := s.Get(idFrag)
	if err != nil {
		return nil, err
	}
	if len(run.Attempts) == 0 {
		return nil, fmt.Errorf("run %s has no attempts (state %s)", run.ID, run.State)
	}
	a := run.Attempts[len(run.Attempts)-1]
	if attempt > 0 {
		found := false
		for _, x := range run.Attempts {
			if x.N == attempt {
				a, found = x, true
			}
		}
		if !found {
			return nil, fmt.Errorf("run %s has no attempt %d", run.ID, attempt)
		}
	}
	ref := a.StdoutRef
	switch stream {
	case "stdout", "":
	case "stderr":
		ref = a.StderrRef
	default:
		return nil, fmt.Errorf("stream must be stdout or stderr, got %q", stream)
	}
	if ref == "" {
		return nil, nil // the stream was empty
	}
	return s.blobs.Get(ctx, ref)
}

// HistoryItem is a command offered by the picker.
type HistoryItem struct {
	Command string
	Source  string // "conduit" or "shell"
	Runs    int
	OK      int
	Last    time.Time
	Effect  string
}

// History returns commands to pick from: executed conduit runs first (most
// recent first, with outcome counts), then any extra shell-history lines not
// already present. extra is raw command text, newest first.
func (s *Service) History(limit int, extra []string) ([]HistoryItem, error) {
	entries, err := s.store.History(limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	out := make([]HistoryItem, 0, len(entries)+len(extra))
	for _, e := range entries {
		seen[e.Command] = true
		out = append(out, HistoryItem{Command: e.Command, Source: "conduit", Runs: e.Runs, OK: e.OK, Last: e.Last, Effect: e.Effect})
	}
	for _, c := range extra {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, HistoryItem{Command: c, Source: "shell"})
	}
	return out, nil
}

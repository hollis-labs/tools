package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/tools/conduit/internal/store"
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

// GetFor returns a run if the viewer may see it: everything for a viewer with
// all=true, otherwise only runs the viewer submitted. A run someone else owns is
// reported as not found, so its existence is not revealed.
func (s *Service) GetFor(idFrag, viewer string, all bool) (*store.Run, error) {
	run, err := s.Get(idFrag)
	if err != nil {
		return nil, err
	}
	if !all && run.Principal != viewer {
		return nil, store.ErrNotFound
	}
	return run, nil
}

// ListFor lists runs visible to the viewer.
func (s *Service) ListFor(viewer string, all bool, session, state string, limit int) ([]*store.Run, error) {
	f := store.Filter{Session: session, State: state, Limit: limit}
	if !all {
		f.Principal = viewer
		if viewer == "" {
			return nil, nil
		}
	}
	return s.store.ListRuns(f)
}

// RecallFor is Recall limited to runs the viewer may see.
func (s *Service) RecallFor(ctx context.Context, idFrag, stream string, attempt int, viewer string, all bool) ([]byte, error) {
	run, err := s.GetFor(idFrag, viewer, all)
	if err != nil {
		return nil, err
	}
	return s.Recall(ctx, run.ID, stream, attempt)
}

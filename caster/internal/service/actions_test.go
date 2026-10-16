package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/caster/internal/ref"
	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/store"
)

type fakeWriter struct {
	drafts []resolver.WorkspaceDraft
}

func (f *fakeWriter) Schemes() []string { return nil }
func (f *fakeWriter) Resolve(context.Context, ref.Ref) (resolver.Meta, error) {
	return resolver.Meta{}, nil
}
func (f *fakeWriter) Materialize(context.Context, ref.Ref) (resolver.Content, error) {
	return resolver.Content{}, nil
}
func (f *fakeWriter) WriteWorkspace(_ context.Context, d resolver.WorkspaceDraft) (resolver.WorkspaceReceipt, error) {
	f.drafts = append(f.drafts, d)
	return resolver.WorkspaceReceipt{Status: "created", ItemID: "ITEM1"}, nil
}

func newSvc(t *testing.T) (*Service, *fakeWriter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &fakeWriter{}
	return New(st, w), w
}

var okCtx = ActionContext{Namespace: "project/x/workspace/handoffs", SessionID: "s1", AgentID: "a1"}

func seed(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.Notes.Put(localCtx(), PutRequest{Name: "n/h", Content: "# Handoff\n\nbody"}); err != nil {
		t.Fatal(err)
	}
}

func TestSaveHandoffValidatesContext(t *testing.T) {
	s, w := newSvc(t)
	seed(t, s)
	for name, c := range map[string]ActionContext{
		"missing":  {Namespace: okCtx.Namespace},
		"user":     {Namespace: "user/chrispian/workspace/x", SessionID: "s", AgentID: "a"},
		"no-space": {Namespace: "project/x/memory/notes", SessionID: "s", AgentID: "a"},
	} {
		_, err := s.Actions.Run(localCtx(), ActRequest{Action: ActionSaveHandoff, Ref: "n/h", Context: c})
		if KindOf(err) != KindInvalid {
			t.Errorf("%s: want invalid, got %v", name, err)
		}
	}
	if len(w.drafts) != 0 {
		t.Fatalf("no write may happen on invalid input: %d", len(w.drafts))
	}
}

func TestSaveHandoffDryRunStoresAndWritesNothing(t *testing.T) {
	s, w := newSvc(t)
	seed(t, s)
	res, err := s.Actions.Run(localCtx(), ActRequest{Action: ActionSaveHandoff, Ref: "n/h", Context: okCtx, DryRun: true})
	if err != nil || res.Status != StatusDryRun || res.Plan == "" || len(w.drafts) != 0 {
		t.Fatalf("dry run: %+v %v writes=%d", res, err, len(w.drafts))
	}
}

func TestSaveHandoffWritesOnceAndReplays(t *testing.T) {
	s, w := newSvc(t)
	seed(t, s)
	run := func() ActResult {
		res, err := s.Actions.Run(localCtx(), ActRequest{Action: ActionSaveHandoff, Ref: "n/h", Context: okCtx})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	first, second := run(), run()
	if first.Status != StatusDone || first.Outcome != "tesseract://item/ITEM1" {
		t.Fatalf("first: %+v", first)
	}
	if second.Status != StatusReplayed || second.Outcome != first.Outcome || len(w.drafts) != 1 {
		t.Fatalf("a retry must replay, not write again: %+v writes=%d", second, len(w.drafts))
	}
	d := w.drafts[0]
	if d.Summary != "Handoff" || !strings.HasPrefix(d.Data["snapshot"].(string), "cas://sha256/") || d.IdempotencyKey != first.Key {
		t.Fatalf("draft not bound as expected: %+v", d)
	}
}

func TestSaveHandoffUnresolvedWritesNothing(t *testing.T) {
	s, w := newSvc(t)
	res, err := s.Actions.Run(localCtx(), ActRequest{Action: ActionSaveHandoff, Ref: "n/missing", Context: okCtx})
	if err != nil || res.State != StateNotFound || len(w.drafts) != 0 {
		t.Fatalf("%+v %v writes=%d", res, err, len(w.drafts))
	}
	if _, err := s.Actions.Run(localCtx(), ActRequest{Action: "nope", Ref: "n/h", Context: okCtx}); KindOf(err) != KindInvalid {
		t.Fatalf("unknown action must be invalid, got %v", err)
	}
}

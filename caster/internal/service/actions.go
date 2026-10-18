package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/store"
)

// WorkspaceWriter creates Tesseract workspace drafts. The Tesseract resolver
// implements it; a Service built without one reports the write actions as
// unavailable.
type WorkspaceWriter interface {
	WriteWorkspace(ctx context.Context, d resolver.WorkspaceDraft) (resolver.WorkspaceReceipt, error)
}

// Effect classes. Remote writes change another system and are reported as such.
const (
	EffectRemoteWrite = "remote_write"
)

// ActionSaveHandoff is the ID of the action that saves a ref as a Tesseract workspace draft.
const ActionSaveHandoff = "save-handoff"

// ActionInfo describes a registered action.
type ActionInfo struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Effect      string   `json:"effect"`
	Needs       []string `json:"needs"` // ActionContext fields that must be bound
}

// ActionContext is the ambient context an action binds its parameters from, so
// nothing is typed at click time: the caller (a session, a UI) supplies it.
type ActionContext struct {
	Namespace    string `json:"namespace,omitempty"`     // Tesseract workspace namespace
	SessionID    string `json:"session_id,omitempty"`    // authoring session
	AgentID      string `json:"agent_id,omitempty"`      // authoring agent
	WorkstreamID string `json:"workstream_id,omitempty"` // optional association
}

// ActionsRequest asks which actions apply to a ref.
type ActionsRequest struct {
	Ref string `json:"ref"`
}

// ActionsResult lists applicable actions. State is the ref's resolution state;
// nothing applies to a ref that does not resolve.
type ActionsResult struct {
	Ref     string       `json:"ref"`
	State   string       `json:"state"`
	Actions []ActionInfo `json:"actions"`
}

// ActRequest runs one action on a ref. DryRun resolves and plans but stores and
// writes nothing.
type ActRequest struct {
	Action  string        `json:"action"`
	Ref     string        `json:"ref"`
	Context ActionContext `json:"context"`
	DryRun  bool          `json:"dry_run,omitempty"`
}

// Act statuses.
const (
	StatusDone     = "done"
	StatusReplayed = "replayed"
	StatusDryRun   = "dry_run"
)

// ActResult reports an action. Source is the pinned cas:// snapshot the action
// worked from (so the original is always recoverable); Outcome is the ref the
// action produced. Key is the idempotency key.
type ActResult struct {
	Action  string `json:"action"`
	Status  string `json:"status"`
	State   string `json:"state"`
	Source  string `json:"source,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Key     string `json:"key,omitempty"`
	Plan    string `json:"plan,omitempty"`
}

// Actions is the domain service for registered operations on refs.
type Actions struct {
	store  *store.Store
	refs   *Refs
	writer WorkspaceWriter
}

var registry = []ActionInfo{
	{ID: ActionSaveHandoff, Label: "Save handoff (workspace draft)", Effect: EffectRemoteWrite,
		Description: "Snapshot the ref into CAS, then create a Tesseract workspace draft from its text with provenance back to the snapshot. Drafts are replaceable and reviewed before promotion. Idempotent per content and session.",
		Needs:       []string{"namespace", "session_id", "agent_id"}},
}

func lookup(id string) (ActionInfo, bool) {
	for _, a := range registry {
		if a.ID == id {
			return a, true
		}
	}
	return ActionInfo{}, false
}

// List returns the actions that apply to a ref. All current actions need text.
func (a *Actions) List(ctx context.Context, req ActionsRequest) (ActionsResult, error) {
	if err := require(ctx, ScopeRead); err != nil {
		return ActionsResult{}, err
	}
	m, err := a.refs.Head(ctx, RefRequest(req))
	if err != nil {
		return ActionsResult{}, err
	}
	out := ActionsResult{Ref: m.Ref, State: m.State, Actions: []ActionInfo{}}
	if m.State == StateResolved && (m.Mime == "" || strings.HasPrefix(m.Mime, "text/")) {
		out.Actions = append(out.Actions, registry...)
		sort.Slice(out.Actions, func(i, j int) bool { return out.Actions[i].ID < out.Actions[j].ID })
	}
	return out, nil
}

// Run executes an action.
func (a *Actions) Run(ctx context.Context, req ActRequest) (ActResult, error) {
	if err := require(ctx, ScopeAct); err != nil {
		return ActResult{}, err
	}
	def, ok := lookup(req.Action)
	if !ok {
		return ActResult{}, invalid(fmt.Errorf("unknown action %q", req.Action))
	}
	switch def.ID {
	case ActionSaveHandoff:
		return a.saveHandoff(ctx, def, req)
	}
	return ActResult{}, invalid(fmt.Errorf("action %q has no handler", def.ID))
}

func (a *Actions) saveHandoff(ctx context.Context, def ActionInfo, req ActRequest) (ActResult, error) {
	c := req.Context
	var missing []string
	for _, f := range []struct{ name, v string }{{"namespace", c.Namespace}, {"session_id", c.SessionID}, {"agent_id", c.AgentID}} {
		if strings.TrimSpace(f.v) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return ActResult{}, invalid(fmt.Errorf("%s needs context: %s", def.ID, strings.Join(missing, ", ")))
	}
	if err := checkWorkspaceNamespace(c.Namespace); err != nil {
		return ActResult{}, err
	}
	if a.writer == nil && !req.DryRun {
		return ActResult{}, &Error{KindUnavailable, fmt.Errorf("no Tesseract writer configured")}
	}
	// A real run works only from the pinned snapshot, so the key, the draft and
	// the provenance all describe the same bytes even if the source is floating.
	// A dry run reads the source but stores nothing.
	src := req.Ref
	var snap SnapResult
	if !req.DryRun {
		var err error
		if snap, err = a.refs.Snapshot(ctx, SnapRequest{Ref: req.Ref}); err != nil {
			return ActResult{}, err
		}
		if snap.State != StateResolved {
			return ActResult{Action: def.ID, State: snap.State}, nil
		}
		src = snap.CAS
	}
	got, err := a.refs.Get(ctx, GetRequest{Ref: src, MaxBytes: -1})
	if err != nil {
		return ActResult{}, err
	}
	res := ActResult{Action: def.ID, State: got.State}
	if got.State != StateResolved {
		return res, nil
	}
	if got.Binary {
		return ActResult{}, invalid(fmt.Errorf("%s: %s is not text", def.ID, req.Ref))
	}
	sum := sha256.Sum256(got.Data)
	sha := hex.EncodeToString(sum[:])
	res.Key = fmt.Sprintf("caster:%s:%s:%s", def.ID, sha[:16], c.SessionID)
	res.Source = "cas://sha256/" + sha
	if req.DryRun {
		res.Status = StatusDryRun
		res.Plan = fmt.Sprintf("snapshot %s (%d bytes) into %s, then create a workspace draft in %s as agent %s, session %s", req.Ref, len(got.Data), res.Source, c.Namespace, c.AgentID, c.SessionID)
		return res, nil
	}
	if prev, perr := a.store.LookupAction(res.Key); perr == nil {
		res.Status, res.Outcome = StatusReplayed, prev.Outcome
		return res, nil
	}
	rec, err := a.writer.WriteWorkspace(ctx, resolver.WorkspaceDraft{
		Namespace: c.Namespace, Summary: summarize(string(got.Data), "", req.Ref), Body: string(got.Data),
		IdempotencyKey: res.Key, AuthorAgentID: c.AgentID, SessionID: c.SessionID, WorkstreamID: c.WorkstreamID,
		Tags: []string{"handoff", "caster"},
		Data: map[string]any{"action": def.ID, "source_ref": req.Ref, "snapshot": snap.CAS, "sha256": sha, "by": "caster"},
	})
	if err != nil {
		return ActResult{}, classify(err)
	}
	res.Outcome = "tesseract://item/" + rec.ItemID
	res.Status = StatusDone
	if rec.Status == "replayed" {
		res.Status = StatusReplayed
	}
	if _, err := a.store.RecordAction(store.ActionRecord{Key: res.Key, Action: def.ID, Source: res.Source, Outcome: res.Outcome}); err != nil {
		return res, classify(err)
	}
	return res, nil
}

// checkWorkspaceNamespace keeps agent-authored writes inside a project/app/org/
// session workspace and out of the operator's own user/ scope.
func checkWorkspaceNamespace(ns string) error {
	parts := strings.Split(ns, "/")
	if parts[0] == "user" {
		return invalid(fmt.Errorf("namespace %q is in the user scope; agent-authored drafts go in a project/app/org/session workspace", ns))
	}
	for i, p := range parts {
		if p == "workspace" && i >= 1 {
			return nil
		}
	}
	return invalid(fmt.Errorf("namespace %q must look like {scope}/{id}/workspace/{purpose}", ns))
}

// summarize makes a one-line summary: the first heading or line, else the title.
func summarize(body, title, ref string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" && !strings.HasPrefix(line, "<!--") {
			if len(line) > 200 {
				line = strings.ToValidUTF8(line[:200], "")
			}
			return line
		}
	}
	if title != "" {
		return title
	}
	return ref
}

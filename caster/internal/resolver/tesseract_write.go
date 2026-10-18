package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hollis-labs/caster/internal/mcpclient"
)

// WorkspaceDraft is one Tesseract workspace item to create. IdempotencyKey makes
// the create safe to retry: the same key and arguments replay the original.
type WorkspaceDraft struct {
	Namespace, Summary, Body string
	IdempotencyKey           string
	AuthorAgentID, SessionID string
	WorkstreamID             string
	Tags                     []string
	Data                     map[string]any // provenance: the record's own fields
}

// WorkspaceReceipt is Tesseract's answer: Status is "created" or "replayed".
type WorkspaceReceipt struct {
	Status string
	ItemID string
}

// WriteWorkspace creates a workspace item through the gateway (workspace_write).
// Workspace is Tesseract's draft domain: content is replaceable and deletable,
// and promotion to memory or knowledge stays a separate reviewed step.
func (t Tesseract) WriteWorkspace(ctx context.Context, d WorkspaceDraft) (WorkspaceReceipt, error) {
	args := map[string]any{
		"namespace": d.Namespace, "summary": d.Summary, "body": d.Body,
		"idempotency_key": d.IdempotencyKey, "author_agent_id": d.AuthorAgentID, "session_id": d.SessionID,
	}
	if d.WorkstreamID != "" {
		args["workstream_id"] = d.WorkstreamID
	}
	if len(d.Tags) > 0 {
		b, _ := json.Marshal(d.Tags)
		args["tags"] = string(b)
	}
	if d.Data != nil {
		b, _ := json.Marshal(d.Data) // a JSON-encoded string is stored exactly as sent
		args["data"] = string(b)
	}
	out, err := t.call(ctx, "workspace_write", args)
	var te *mcpclient.ToolError
	if errors.As(err, &te) {
		if st, ok := errState(te); ok && st == StateDenied {
			return WorkspaceReceipt{}, fmt.Errorf("tesseract: write denied: %s", string(te.Body))
		}
		return WorkspaceReceipt{}, fmt.Errorf("tesseract: workspace_write: %s", string(te.Body))
	}
	if err != nil {
		return WorkspaceReceipt{}, err
	}
	var raw struct {
		Status string `json:"status"`
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(out, &raw); err != nil || raw.ItemID == "" {
		return WorkspaceReceipt{}, fmt.Errorf("tesseract: unexpected workspace_write response")
	}
	return WorkspaceReceipt{Status: raw.Status, ItemID: raw.ItemID}, nil
}

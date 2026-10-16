package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/caster/internal/mcpclient"
	"github.com/hollis-labs/caster/internal/ref"
)

// selector turns a ref into tesseract tool arguments.
func selector(r ref.Ref) map[string]any {
	switch r.Auth {
	case "item":
		return map[string]any{"item_id": r.Path}
	case "revision":
		return map[string]any{"revision_id": r.Path}
	}
	ns, key := "", r.Path
	if i := strings.LastIndex(r.Path, "/"); i >= 0 {
		ns, key = r.Path[:i], r.Path[i+1:]
	}
	return map[string]any{"domain": r.Auth, "namespace": ns, "key": key}
}

func (t Tesseract) call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if t.Gateway == nil {
		return nil, fmt.Errorf("tesseract: %w: no MCP gateway configured (set CASTER_TESSERACT_MCP or add ~/.tether/catalog/mcp-servers/tesseract.yaml)", ErrUnavailable)
	}
	out, err := t.Gateway.Call(ctx, tool, args)
	var te *mcpclient.ToolError
	switch {
	case errors.As(err, &te):
		return nil, te
	case err != nil:
		return nil, fmt.Errorf("tesseract: %w: %v", ErrUnavailable, err)
	}
	return out, nil
}

// errState maps a tool error body to a resolver state, if it is an outcome
// rather than a failure.
func errState(te *mcpclient.ToolError) (string, bool) {
	var b struct{ Code string }
	_ = json.Unmarshal(te.Body, &b)
	switch b.Code {
	case "not_found", "deleted":
		return StateNotFound, true
	case "namespace_not_permitted", "forbidden", "auth_required":
		return StateDenied, true
	}
	return "", false
}

func (t Tesseract) gwResolve(ctx context.Context, r ref.Ref) (Meta, error) {
	m := Meta{Ref: r, State: StateNotFound}
	if r.Rev > 0 {
		return m, fmt.Errorf("tesseract: @rev does not apply to %s refs; use tesseract://revision/<id> to pin", r.Auth)
	}
	args := selector(r)
	if r.Auth == "item" || r.Auth == "revision" {
		args = map[string]any{"uri": "tesseract://" + r.Auth + "/" + r.Path}
	}
	out, err := t.call(ctx, "tesseract_ref_resolve", args)
	var te *mcpclient.ToolError
	if errors.As(err, &te) {
		if st, ok := errState(te); ok {
			m.State = st
			return m, nil
		}
		return m, te
	}
	if err != nil {
		return m, err
	}
	var res struct {
		Status     string `json:"status"`
		ItemID     string `json:"item_id"`
		RevisionID string `json:"revision_id"`
		Domain     string `json:"domain"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return m, err
	}
	if res.Status != "resolved" {
		m.State = StateNotFound
		return m, nil
	}
	m.State, m.Kind, m.Mime = StateResolved, "tesseract_"+res.Domain, "text/markdown"
	m.Title = res.ItemID
	if r.Auth == "revision" {
		m.Revision = r.Path
	}
	return m, nil
}

func (t Tesseract) gwMaterialize(ctx context.Context, r ref.Ref) (Content, error) {
	m, err := t.gwResolve(ctx, r)
	if err != nil || m.State != StateResolved {
		return Content{Meta: m}, err
	}
	tool, args := "tesseract_get", selector(r)
	if r.Auth == "revision" {
		tool = "tesseract_get_revision"
	}
	out, err := t.call(ctx, tool, args)
	var te *mcpclient.ToolError
	if errors.As(err, &te) {
		if st, ok := errState(te); ok {
			m.State = st
			return Content{Meta: m}, nil
		}
		return Content{Meta: m}, te
	}
	if err != nil {
		return Content{Meta: m}, err
	}
	data, title, rev := renderItem(out)
	m.Title, m.Revision, m.Size, m.ContentHash = title, rev, int64(len(data)), digest(data)
	return Content{Meta: m, Data: data}, nil
}

// renderItem renders a Tesseract revision/item object as markdown: a short
// header, then the payload body (or summary), else the payload as JSON.
func renderItem(raw json.RawMessage) (data []byte, title, rev string) {
	var o struct {
		RevisionID string          `json:"revision_id"`
		ItemID     string          `json:"item_id"`
		Domain     string          `json:"domain"`
		Namespace  string          `json:"namespace"`
		Key        string          `json:"memory_key"`
		Status     string          `json:"status"`
		Tags       []string        `json:"tags"`
		CreatedAt  string          `json:"created_at"`
		Payload    json.RawMessage `json:"payload"`
	}
	_ = json.Unmarshal(raw, &o)
	var p struct {
		Body    string `json:"body"`
		Summary string `json:"summary"`
	}
	_ = json.Unmarshal(o.Payload, &p)
	body := p.Body
	if body == "" {
		body = p.Summary
	}
	if body == "" {
		if len(o.Payload) > 0 {
			pretty, _ := json.MarshalIndent(o.Payload, "", "  ")
			body = "```json\n" + string(pretty) + "\n```"
		} else {
			pretty, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
			body = "```json\n" + string(pretty) + "\n```"
		}
	}
	title = o.Namespace
	if o.Key != "" {
		title += "/" + o.Key
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- tesseract %s item=%s revision=%s status=%s created=%s tags=%s -->\n\n%s\n",
		o.Domain, o.ItemID, o.RevisionID, o.Status, o.CreatedAt, strings.Join(o.Tags, ","), strings.TrimSpace(body))
	return []byte(b.String()), title, o.RevisionID
}

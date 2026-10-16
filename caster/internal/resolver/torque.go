package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/caster/internal/ref"
)

// DefaultTorqueURL is the local Torque HTTP API.
const DefaultTorqueURL = "http://127.0.0.1:8990"

// Torque resolves torque://task/<id> through Torque's read-only REST API.
type Torque struct {
	BaseURL string
	Client  *http.Client
}

// Schemes implements Resolver.
func (Torque) Schemes() []string { return []string{ref.SchemeTorque} }

type torqueTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Description string `json:"description"`
	ProjectID   string `json:"project_id"`
	UpdatedAt   string `json:"updated_at"`
}

func (t Torque) fetch(ctx context.Context, r ref.Ref) (*torqueTask, Meta, error) {
	m := Meta{Ref: r, State: StateNotFound}
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		base = DefaultTorqueURL
	}
	c := t.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/tasks/"+r.Path, nil)
	if err != nil {
		return nil, m, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, m, fmt.Errorf("torque: %w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, m, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		m.State = StateDenied
		return nil, m, nil
	case resp.StatusCode != http.StatusOK:
		return nil, m, fmt.Errorf("torque: %w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, m, err
	}
	var task torqueTask
	if err := json.Unmarshal(body, &task); err != nil || task.ID == "" {
		return nil, m, fmt.Errorf("torque: unexpected response shape")
	}
	m.State, m.Kind, m.Title, m.Mime, m.Revision = StateResolved, "task", task.ID+" "+task.Title, "text/markdown", task.UpdatedAt
	return &task, m, nil
}

// Resolve implements Resolver.
func (t Torque) Resolve(ctx context.Context, r ref.Ref) (Meta, error) {
	_, m, err := t.fetch(ctx, r)
	return m, err
}

// Materialize implements Resolver: the task rendered as markdown.
func (t Torque) Materialize(ctx context.Context, r ref.Ref) (Content, error) {
	task, m, err := t.fetch(ctx, r)
	if err != nil || task == nil {
		return Content{Meta: m}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n- status: %s\n- project: %s\n- updated: %s\n\n%s\n",
		task.ID, task.Title, task.Status, task.ProjectID, task.UpdatedAt, strings.TrimSpace(task.Description))
	data := []byte(b.String())
	m.Size, m.ContentHash = int64(len(data)), digest(data)
	return Content{Meta: m, Data: data}, nil
}

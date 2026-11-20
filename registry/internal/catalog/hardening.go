package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
)

func validateExamples(kind string, examples []ToolExample) error {
	if len(examples) == 0 {
		return nil
	}
	if kind != "tool" || len(examples) > 50 {
		return failure("validation_error", "record.examples", "only tools accept up to 50 authored examples")
	}
	seen := map[string]bool{}
	for _, e := range examples {
		if strings.TrimSpace(e.Name) == "" || seen[e.Name] {
			return failure("validation_error", "record.examples.name", "unique nonempty example names required")
		}
		seen[e.Name] = true
		var args map[string]json.RawMessage
		if json.Unmarshal(e.Arguments, &args) != nil || args == nil {
			return failure("validation_error", "record.examples.arguments", "example arguments must be a JSON object")
		}
	}
	return nil
}

// Every weight is authored and shared-schema validated, never mined as authority.
// A decision binds the complete proposal hash and source version. Editing any
// relationship attribute requires a new proposal and independent decision.
func (s *Service) decideRelation(ctx context.Context, tx store.Tx, p libauth.Principal, r *Record, a Args) error {
	if a.Decision != "reviewed" || strings.TrimSpace(a.Reason) == "" {
		return failure("validation_error", "decision", "reviewed and a reason required; remove unwanted proposals explicitly")
	}
	role := s.reviewers[p.ID]
	if role != "ground" && role != "elevated" && role != "owner" {
		return failure("forbidden", "reviewer", "configured reviewer required")
	}
	for i := range r.Edges {
		e := &r.Edges[i]
		if e.ID != a.EdgeID {
			continue
		}
		if e.Relation.Status != "proposed" || a.Hash == "" || a.Hash != digest(e.Relation) {
			return failure("version_conflict", "hash", "current proposed relation hash required")
		}
		if e.ProposedBy == "" || e.ProposedBy == p.ID {
			return failure("forbidden", "reviewer", "a known independent proposer is required; recreate legacy proposals before review")
		}
		// Elevated records retain the reserved owner signoff boundary.
		if (r.Kind == "agent" || r.Kind == "config" || r.Kind == "agency" || r.Kind == "launch_profile") && role != "owner" {
			return failure("forbidden", "reviewer", "owner review required for elevated relationships")
		}
		if err := checkEdges(ctx, tx, *r, false); err != nil {
			return err
		}
		e.ReviewHash = a.Hash
		e.Relation.Status = "reviewed"
		e.Relation.Source = "reviewed"
		e.ReviewedBy = p.ID
		e.ReviewedAt = time.Now().UTC().Format(time.RFC3339Nano)
		e.Reason = a.Reason
		return e.Relation.Validate()
	}
	return failure("not_found", "edge_id", "edge not found")
}

type ReferenceIssue struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Edge     Edge   `json:"edge"`
}
type ReferenceReport struct {
	Issues   []ReferenceIssue `json:"issues"`
	Examined int              `json:"examined"`
	Cursor   string           `json:"cursor,omitempty"`
	HasMore  bool             `json:"has_more"`
	Boundary int64            `json:"boundary"`
	AsOf     string           `json:"as_of"`
}

func (s *Service) referencesReport(ctx context.Context, caller string, query Query) (ReferenceReport, error) {
	query.Full = true
	page, err := s.list(ctx, caller, query)
	out := ReferenceReport{Issues: []ReferenceIssue{}, Examined: len(page.Records), Cursor: page.Cursor, HasMore: page.HasMore, Boundary: page.Boundary, AsOf: page.AsOf}
	if err != nil {
		return out, err
	}
	for _, r := range page.Records {
		for _, e := range r.Edges {
			if e.TargetState != "resolved" {
				out.Issues = append(out.Issues, ReferenceIssue{ID: r.ID, Revision: r.RevisionID, Edge: e})
			}
		}
	}
	return out, nil
}

type KindMigration struct {
	ID           string `json:"id"`
	RecordID     string `json:"record_id"`
	FromSchema   string `json:"from_schema"`
	ToSchema     string `json:"to_schema"`
	FromRevision string `json:"from_revision"`
	ToRevision   string `json:"to_revision"`
	Actor        string `json:"actor"`
	Reason       string `json:"reason"`
	MigratedAt   string `json:"migrated_at"`
}

func (s *Service) migrationList(ctx context.Context, a Args) (any, error) {
	if a.ID == "" {
		return nil, failure("validation_error", "id", "record id required")
	}
	limit := a.Query.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, failure("validation_error", "query.limit", "limit must be 1..200")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,record_id,from_schema,to_schema,from_revision,to_revision,actor,reason,migrated_at FROM kind_migrations WHERE record_id=? AND (?='' OR id>?) ORDER BY id LIMIT ?`, a.ID, a.Query.Cursor, a.Query.Cursor, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []KindMigration{}
	for rows.Next() {
		var m KindMigration
		if err = rows.Scan(&m.ID, &m.RecordID, &m.FromSchema, &m.ToSchema, &m.FromRevision, &m.ToRevision, &m.Actor, &m.Reason, &m.MigratedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	more := len(out) > limit
	cursor := ""
	if more {
		out = out[:limit]
		cursor = out[len(out)-1].ID
	}
	return map[string]any{"migrations": out, "has_more": more, "cursor": cursor}, nil
}

package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/kinds"
)

type Service struct {
	DB          *store.Store
	authorities map[string]string
	reviewers   map[string]string
}

func New(db *store.Store, authorities map[string]string, reviewers map[string]string) *Service {
	bindings := map[string]string{}
	for k, v := range authorities {
		bindings[k] = v
	}
	roles := map[string]string{}
	for k, v := range reviewers {
		roles[k] = v
	}
	return &Service{DB: db, authorities: bindings, reviewers: roles}
}

var Operations = []string{"kinds_list", "schema_get", "record_create", "record_get", "record_list", "record_search", "record_update", "record_archive", "record_delete", "record_verify", "revision_list", "revision_get", "relation_create", "relation_list", "relation_remove", "tool_resolve", "tool_classify", "tool_propose", "review_list", "review_decide", "snapshot_get", "diagnostics"}

func (s *Service) Call(ctx context.Context, op string, a Args) (any, error) {
	p, ok := libauth.FromContext(ctx)
	if !ok || !p.Verified || p.ID == "" || !p.HasScope(scope(op)) {
		return nil, failure("forbidden", "scope", scope(op)+" required")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, invalid("arguments", err)
	}
	if err = kinds.ValidateJSONValue(raw); err != nil {
		return nil, invalid("arguments", err)
	}
	known := false
	for _, v := range Operations {
		if v == op {
			known = true
			break
		}
	}
	if !known {
		return nil, failure("not_found", "operation", "unknown operation")
	}
	switch op {
	case "kinds_list":
		return map[string]any{"schemas": kinds.Schemas(), "edges": kinds.EdgeSpecs()}, nil
	case "schema_get":
		b, err := kinds.Schema(a.Schema)
		if err != nil {
			return nil, failure("unknown_schema", "schema", err.Error())
		}
		return json.RawMessage(b), nil
	case "diagnostics":
		if checkErr := s.DB.Ping(ctx); checkErr != nil {
			return nil, failure("unavailable", "database", "database is not ready")
		}
		return map[string]any{"ready": true, "storage": "sqlite", "schemas": kinds.Schemas()}, nil
	case "record_list", "record_search", "snapshot_get", "review_list":
		if op == "snapshot_get" {
			a.Query.Full = true
			if a.Query.Lifecycle == "" {
				a.Query.Lifecycle = "*"
			}
		}
		if op == "review_list" {
			a.Query.Kind = "tool"
			if a.Query.ReviewState == "" {
				a.Query.ReviewState = "pending"
			}
		}
		return s.list(ctx, p.ID, a.Query)
	case "record_get", "tool_resolve", "tool_classify", "relation_list", "revision_list", "revision_get":
		return s.read(ctx, op, a)
	default:
		return s.mutate(ctx, p, op, a)
	}
}
func readRecord(ctx context.Context, q store.Querier, id string, boundary int64) (Record, error) {
	var raw []byte
	statement := `SELECT body FROM revisions WHERE id=? ORDER BY seq DESC LIMIT 1`
	args := []any{id}
	if boundary >= 0 {
		statement = `SELECT body FROM revisions WHERE id=? AND seq<=? ORDER BY seq DESC LIMIT 1`
		args = append(args, boundary)
	}
	err := q.QueryRowContext(ctx, statement, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, failure("not_found", "id", "record not found")
	}
	if err != nil {
		return Record{}, err
	}
	var r Record
	err = json.Unmarshal(raw, &r)
	return r, err
}
func (s *Service) read(ctx context.Context, op string, a Args) (any, error) {
	var out any
	err := s.DB.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
		id := a.ID
		if op == "tool_resolve" && a.Alias != "" {
			if id != "" {
				return failure("validation_error", "id", "choose id or alias")
			}
			if checkErr := q.QueryRowContext(ctx, `SELECT id FROM aliases WHERE system=? AND alias=?`, a.AliasSystem, a.Alias).Scan(&id); checkErr != nil {
				if errors.Is(checkErr, sql.ErrNoRows) {
					return failure("not_found", "alias", "alias not found")
				}
				return checkErr
			}
		}
		if op == "revision_get" {
			var raw []byte
			if checkErr := q.QueryRowContext(ctx, `SELECT body FROM revisions WHERE id=? AND token=?`, id, a.Revision).Scan(&raw); checkErr != nil {
				if errors.Is(checkErr, sql.ErrNoRows) {
					return failure("not_found", "revision", "revision not found")
				}
				return checkErr
			}
			out = json.RawMessage(raw)
			return nil
		}
		r, err := readRecord(ctx, q, id, -1)
		if err != nil {
			return err
		}
		if op == "revision_list" {
			// History pages contain immutable complete records, ordered by sequence.
			limit := a.Query.Limit
			if limit == 0 {
				limit = 50
			}
			if limit < 1 || limit > 200 {
				return failure("validation_error", "query.limit", "limit must be 1..200")
			}
			before := int64(0)
			if a.Revision != "" {
				if checkErr := q.QueryRowContext(ctx, `SELECT seq FROM revisions WHERE id=? AND token=?`, id, a.Revision).Scan(&before); checkErr != nil {
					return failure("not_found", "revision", "revision cursor not found")
				}
			}
			rows, err := q.QueryContext(ctx, `SELECT body FROM revisions WHERE id=? AND (?=0 OR seq<?) ORDER BY seq DESC LIMIT ?`, id, before, before, limit+1)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			rs := []Record{}
			for rows.Next() {
				var b []byte
				var x Record
				if checkErr := rows.Scan(&b); checkErr != nil {
					return checkErr
				}
				if checkErr := json.Unmarshal(b, &x); checkErr != nil {
					return checkErr
				}
				rs = append(rs, x)
			}
			if checkErr := rows.Err(); checkErr != nil {
				return checkErr
			}
			more := len(rs) > limit
			next := ""
			if more {
				rs = rs[:limit]
				next = rs[len(rs)-1].RevisionID
			}
			out = map[string]any{"revisions": rs, "has_more": more, "before_revision": next}
			return nil
		}
		if r.Lifecycle == "deleted" {
			return failure("deleted", "id", "record is a tombstone; use history")
		}
		if op == "tool_classify" || op == "tool_resolve" {
			if r.Kind != "tool" {
				return failure("validation_error", "id", "tool required")
			}
		}
		if checkErr := resolveEdges(ctx, q, &r, -1); checkErr != nil {
			return checkErr
		}
		switch op {
		case "tool_classify":
			var tool kinds.Tool
			if checkErr := json.Unmarshal(r.Payload, &tool); checkErr != nil {
				return checkErr
			}
			out = map[string]any{"id": r.ID, "version_token": r.VersionToken, "definition_hash": r.Review.DefinitionHash, "lifecycle": r.Lifecycle, "declared": tool.Definition.Annotations, "review": r.Review, "defaults": map[string]any{"side_effect": []string{"write", "destructive"}, "idempotent": false, "egress": "external", "approval_tier": "elevated"}}
		case "relation_list":
			edges := r.Edges
			if a.Direction != "" && a.Direction != "outgoing" && a.Direction != "incoming" && a.Direction != "both" {
				return failure("validation_error", "direction", "outgoing, incoming or both required")
			}
			if a.Direction == "incoming" {
				edges = []Edge{}
			}
			if a.Direction == "incoming" || a.Direction == "both" {
				incoming, err := incomingEdges(ctx, q, r.ID)
				if err != nil {
					return err
				}
				edges = append(edges, incoming...)
			}
			out = map[string]any{"id": r.ID, "version_token": r.VersionToken, "edges": edges}
		default:
			out = r
		}
		return nil
	})
	return out, err
}
func (s *Service) mutate(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	authority := s.authorities[p.ID]
	if op == "review_decide" && s.reviewers[p.ID] == "" {
		return nil, failure("forbidden", "reviewer", "configured reviewer authority required")
	}
	if op != "review_decide" && authority == "" {
		return nil, failure("forbidden", "authority", "caller has no publication authority binding")
	}
	var result Record
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		if op == "record_create" && a.IdempotencyKey != "" {
			var h string
			var raw []byte
			err := tx.QueryRowContext(ctx, `SELECT digest,body FROM receipts WHERE caller=? AND authority=? AND key=?`, p.ID, authority, a.IdempotencyKey).Scan(&h, &raw)
			if err == nil {
				if h != digest(a) {
					return failure("version_conflict", "idempotency_key", "key reused with changed request")
				}
				return json.Unmarshal(raw, &result)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var r Record
		var err error
		if op == "record_create" {
			r, err = prepare(a.Record, authority)
			if err != nil {
				return err
			}
			if a.ID != "" && a.ID != r.ID {
				return failure("validation_error", "id", "id disagrees with canonical payload identity")
			}
			_, err = readRecord(ctx, tx, r.ID, -1)
			if err == nil {
				return failure("version_conflict", "id", "canonical identity already claimed")
			}
			var ce *Error
			if !errors.As(err, &ce) || ce.Code != "not_found" {
				return err
			}
			r.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		} else {
			r, err = readRecord(ctx, tx, a.ID, -1)
			if err != nil {
				return err
			}
			if r.Lifecycle == "deleted" {
				return failure("deleted", "id", "tombstone cannot be changed")
			}
			if a.Version == "" || a.Version != r.VersionToken {
				return failure("version_conflict", "version", "current version token required")
			}
			if op != "review_decide" && r.Authority != authority {
				return failure("forbidden", "authority", "caller does not own this record")
			}
			if op == "record_update" {
				next, err := prepare(a.Record, authority)
				if err != nil {
					return err
				}
				if next.ID != r.ID || next.Kind != r.Kind {
					return failure("validation_error", "record.payload.id", "identity is immutable")
				}
				if r.Lifecycle == "archived" {
					return failure("validation_error", "lifecycle", "archived records cannot be replaced")
				}
				next.CreatedAt = r.CreatedAt
				if r.Kind != "project" {
					next.Edges = r.Edges
				}
				if next.Review != nil && r.Review != nil && next.Review.DefinitionHash == r.Review.DefinitionHash {
					next.Review = r.Review
				}
				r = next
			}
		}
		switch op {
		case "record_create", "record_update":
			if checkErr := checkEdges(ctx, tx, r, a.AllowUnresolved); checkErr != nil {
				return checkErr
			}
			r.LastVerified = time.Now().UTC().Format(time.RFC3339Nano)
			r.VerifiedBy = p.ID
			r.VerificationScope = "source_attestation"
		case "record_verify":
			r.LastVerified = time.Now().UTC().Format(time.RFC3339Nano)
			r.VerifiedBy = p.ID
			r.VerificationScope = "source_attestation"
		case "record_archive":
			r.Lifecycle = "archived"
		case "record_delete":
			if r.Lifecycle != "archived" {
				return failure("validation_error", "lifecycle", "archive before deletion")
			}
			r.Lifecycle = "deleted"
		case "relation_create":
			if a.Relation == nil {
				return failure("validation_error", "relation", "relation required")
			}
			if checkErr := a.Relation.Validate(); checkErr != nil {
				return invalid("relation", checkErr)
			}
			if a.Relation.From != (kinds.Reference{Authority: "registry", Kind: r.Kind, ID: r.ID}) {
				return failure("validation_error", "relation.from", "source must be the exact local record reference")
			}
			if a.Relation.Status != "proposed" || a.Relation.Source == "reviewed" {
				return failure("forbidden", "relation.status", "generic relationship writes create proposals only")
			}
			e := Edge{ID: "edge:" + newID(), Relation: *a.Relation}
			r.Edges = append(r.Edges, e)
			if checkErr := checkEdges(ctx, tx, r, a.AllowUnresolved); checkErr != nil {
				return checkErr
			}
			if checkErr := projectEdges(&r); checkErr != nil {
				return checkErr
			}
		case "relation_remove":
			found := false
			es := []Edge{}
			for _, e := range r.Edges {
				if e.ID == a.EdgeID {
					found = true
				} else {
					es = append(es, e)
				}
			}
			if !found {
				return failure("not_found", "edge_id", "edge not found")
			}
			r.Edges = es
			if checkErr := projectEdges(&r); checkErr != nil {
				return checkErr
			}
		case "tool_propose", "review_decide":
			if r.Kind != "tool" || r.Review == nil {
				return failure("validation_error", "id", "tool required")
			}
			if a.Hash == "" || a.Hash != r.Review.DefinitionHash {
				return failure("version_conflict", "hash", "current definition hash required")
			}
			if a.Classification != nil {
				if checkErr := a.Classification.Validate(); checkErr != nil {
					return invalid("classification", checkErr)
				}
			}
			if strings.TrimSpace(a.Reason) == "" {
				return failure("validation_error", "reason", "review reason required")
			}
			if op == "tool_propose" {
				if a.Classification == nil {
					return failure("validation_error", "classification", "classification required")
				}
				r.Review.Proposed = a.Classification
				r.Review.ProposedBy = p.ID
				r.Review.State = "proposed"
				r.Review.QueueState = "proposed"
				r.Review.Effective = nil
				r.Review.ReviewedBy = ""
				r.Review.ReviewedAt = ""
				r.Review.Approval = nil
			} else {
				r.Review.Approval = nil // Every decision owns its own evidence.
				switch a.Decision {
				case "reviewed":
					if a.Classification == nil {
						return failure("validation_error", "classification", "effective classification required")
					}
					role := s.reviewers[p.ID]
					if role != "ground" && role != "elevated" && role != "owner" {
						return failure("forbidden", "reviewer", "reviewer has no configured review authority")
					}
					priorRestrictive, err := priorRestrictsGround(ctx, tx, r)
					if err != nil {
						return err
					}
					if !groundReview(r, a.Classification) || priorRestrictive {
						if role == "ground" {
							a.Decision = "needs_human"
							r.Review.Proposed = a.Classification
							r.Review.ProposedBy = p.ID
							r.Review.Effective = nil
							r.Review.QueueState = "needs_human"
							break
						}
						if a.Approval == nil || strings.TrimSpace(a.Approval.Authority) == "" || strings.TrimSpace(a.Approval.Kind) == "" || strings.TrimSpace(a.Approval.ID) == "" {
							return failure("validation_error", "approval", "elevated review needs an existing approval/evidence reference")
						}
						r.Review.Approval = a.Approval
					}
					r.Review.Effective = a.Classification
					r.Review.QueueState = "closed"
				case "denied", "quarantined":
					r.Review.Effective = nil
					r.Review.QueueState = "closed"
				case "needs_human":
					r.Review.Effective = nil
					r.Review.QueueState = "needs_human"
				default:
					return failure("validation_error", "decision", "reviewed, denied, quarantined or needs_human required")
				}
				r.Review.State = a.Decision
				if a.Decision == "needs_human" {
					r.Review.State = "proposed"
				}
				r.Review.ReviewedBy = p.ID
				r.Review.ReviewedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
			r.Review.Reason = a.Reason
			if a.Decision == "needs_human" {
				r.Review.ReviewedBy = ""
				r.Review.ReviewedAt = ""
				r.Review.Approval = nil
			}
		}
		if op == "relation_create" || op == "relation_remove" {
			r.LastVerified = ""
			r.VerifiedBy = ""
			r.VerificationScope = ""
		}
		if checkErr := save(ctx, tx, &r); checkErr != nil {
			return checkErr
		}
		result = r
		if op == "record_create" && a.IdempotencyKey != "" {
			raw, err := json.Marshal(r)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO receipts(caller,authority,key,digest,body) VALUES(?,?,?,?,?)`, p.ID, authority, a.IdempotencyKey, digest(a), raw)
			return err
		}
		return nil
	})
	return result, err
}
func prepare(in *Input, authority string) (Record, error) {
	if in == nil {
		return Record{}, failure("validation_error", "record", "record required")
	}
	if strings.TrimSpace(in.Key) == "" {
		return Record{}, failure("validation_error", "record.key", "human key required")
	}
	if checkErr := kinds.ValidateJSON(in.Schema, in.Payload); checkErr != nil {
		if errors.Is(checkErr, kinds.ErrUnknownSchema) {
			return Record{}, failure("unknown_schema", "record.schema", checkErr.Error())
		}
		return Record{}, invalid("record.payload", checkErr)
	}
	var common struct{ ID, Name, Summary, Description, Owner, Subtype, Lifecycle string }
	if checkErr := json.Unmarshal(in.Payload, &common); checkErr != nil {
		return Record{}, invalid("record.payload", checkErr)
	}
	kind := strings.TrimSuffix(in.Schema, "/1")
	switch kind {
	case "project", "tool", "agent", "agency", "skill", "document", "workstream", "correlation":
	default:
		return Record{}, failure("unknown_schema", "record.schema", "not a record schema")
	}
	id := kind + ":" + common.ID
	if kind == "tool" {
		id = common.ID
	}
	r := Record{ID: id, Kind: kind, Schema: in.Schema, Key: in.Key, Name: common.Name, Summary: in.Summary, Owner: common.Owner, Subtype: common.Subtype, Lifecycle: common.Lifecycle, Authority: authority, Source: in.Source, Payload: append(json.RawMessage(nil), in.Payload...), Metadata: in.Metadata, Edges: []Edge{}}
	if r.Name == "" {
		r.Name = r.ID
	} // display fallback; authored payload stays unchanged
	if r.Lifecycle == "" {
		r.Lifecycle = "active"
	}
	if r.Summary == "" {
		r.Summary = common.Summary
	}
	if r.Summary == "" {
		r.Summary = common.Description
	}
	if kind == "tool" {
		t, err := kinds.ParseToolJSON(in.Payload)
		if err != nil {
			return r, invalid("record.payload", err)
		}
		r.Name = t.Definition.Name
		hash, err := kinds.DefinitionHash(*t)
		if err != nil {
			return r, invalid("record.payload.definition", err)
		}
		r.Review = &Review{DefinitionHash: hash, State: "unreviewed", QueueState: "open"}
	}
	if kind == "project" {
		p, err := kinds.ParseProjectJSON(in.Payload)
		if err != nil {
			return r, err
		}
		for _, e := range p.Edges {
			targetKind, _, ok := strings.Cut(e.To, ":")
			if !ok {
				return r, failure("validation_error", "record.payload.edges", "kind-qualified target required")
			}
			r.Edges = append(r.Edges, Edge{ID: "edge:" + newID(), Relation: kinds.Relation{Schema: kinds.RelationSchema, Type: e.Type, From: kinds.Reference{Authority: "registry", Kind: kind, ID: id}, To: kinds.Reference{Authority: "registry", Kind: targetKind, ID: e.To}, Source: "authored", Status: "proposed"}})
		}
	}
	return r, nil
}
func save(ctx context.Context, tx store.Tx, r *Record) error {
	var other string
	err := tx.QueryRowContext(ctx, `SELECT r.id FROM records c JOIN revisions r ON r.seq=c.seq WHERE r.kind=? AND r.authority=? AND json_extract(r.body,'$.key')=? AND r.id<>?`, r.Kind, r.Authority, r.Key, r.ID).Scan(&other)
	if err == nil {
		return failure("version_conflict", "record.key", "key already exists for this kind and authority")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	r.VersionToken = newID()
	r.RevisionID = r.VersionToken
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `INSERT INTO revisions(id,token,kind,authority,lifecycle,name,search_text,body) VALUES(?,?,?,?,?,?,?,?)`, r.ID, r.VersionToken, r.Kind, r.Authority, r.Lifecycle, r.Name, strings.ToLower(r.Name+" "+r.Summary+" "+string(r.Payload)), []byte("{}"))
	if err != nil {
		return err
	}
	r.Sequence, err = res.LastInsertId()
	if err != nil {
		return err
	}
	for i := range r.Edges {
		r.Edges[i].SourceRevision = r.RevisionID
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE revisions SET body=? WHERE seq=?`, raw, r.Sequence); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO records(id,seq) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET seq=excluded.seq`, r.ID, r.Sequence); err != nil {
		return err
	}
	if r.Kind == "tool" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM aliases WHERE id=?`, r.ID); err != nil {
			return err
		}
		t, err := kinds.ParseToolJSON(r.Payload)
		if err != nil {
			return err
		}
		for _, a := range t.Aliases {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT id FROM aliases WHERE system=? AND alias=?`, a.System, a.Alias).Scan(&existing)
			if err == nil {
				return failure("alias_conflict", "record.payload.aliases", "alias already belongs to another identity")
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO aliases(system,alias,id) VALUES(?,?,?)`, a.System, a.Alias, r.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
func checkEdges(ctx context.Context, q store.Querier, r Record, allow bool) error {
	for _, e := range r.Edges {
		if checkErr := e.Relation.Validate(); checkErr != nil {
			return invalid("edges", checkErr)
		}
		ref := e.Relation.To
		if ref.Authority != "registry" {
			continue
		}
		if ref.ID == r.ID && ref.Kind == r.Kind {
			continue
		}
		target, err := readRecord(ctx, q, ref.ID, -1)
		if err != nil {
			var ce *Error
			if errors.As(err, &ce) && ce.Code == "not_found" && allow {
				continue
			}
			if errors.As(err, &ce) && ce.Code == "not_found" {
				return failure("reference_unresolved", "relation.to", "local target missing; explicitly allow unresolved references")
			}
			return err
		}
		if target.Kind != ref.Kind {
			return failure("validation_error", "relation.to.kind", "target kind disagrees with identity")
		}
		if target.Lifecycle == "deleted" && !allow {
			return failure("reference_unresolved", "relation.to", "target deleted; explicitly allow unresolved references")
		}
	}
	return nil
}
func resolveEdges(ctx context.Context, q store.Querier, r *Record, boundary int64) error {
	for i := range r.Edges {
		e := &r.Edges[i]
		e.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
		e.TargetState = "unchecked"
		if e.Relation.To.Authority != "registry" {
			continue
		}
		target, err := readRecord(ctx, q, e.Relation.To.ID, boundary)
		if err != nil {
			var ce *Error
			if errors.As(err, &ce) && ce.Code == "not_found" {
				e.TargetState = "unresolved"
				continue
			}
			return err
		}
		e.TargetState = "resolved"
		if target.Kind != e.Relation.To.Kind {
			e.TargetState = "unresolved"
		} else if target.Lifecycle == "archived" || target.Lifecycle == "deleted" {
			e.TargetState = target.Lifecycle
		}
	}
	return nil
}
func projectEdges(r *Record) error {
	if r.Kind != "project" {
		return nil
	}
	var p map[string]json.RawMessage
	if checkErr := json.Unmarshal(r.Payload, &p); checkErr != nil {
		return checkErr
	}
	edges := []kinds.ProjectEdge{}
	for _, e := range r.Edges {
		if e.Relation.To.Authority != "registry" {
			return failure("validation_error", "relation.to.authority", "project descriptor edges require local canonical targets")
		}
		edges = append(edges, kinds.ProjectEdge{Type: e.Relation.Type, To: e.Relation.To.ID})
	}
	b, err := json.Marshal(edges)
	if err != nil {
		return err
	}
	p["edges"] = b
	r.Payload, err = json.Marshal(p)
	if err != nil {
		return err
	}
	return kinds.ValidateJSON(kinds.ProjectSchema, r.Payload)
}
func internalError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return failure("unavailable", "service", fmt.Sprintf("operation could not complete (%T)", err))
}

// Ground approvers can only confirm read-only ground decisions that do not
// relax declared hints. Every other approval needs configured elevation and
// an existing approval reference; Registry does not implement that workflow.
func groundReview(r Record, c *kinds.Classification) bool {
	if (c.InputDependent != nil && *c.InputDependent) || c.RuleRef != "" || c.ArgRules != "" || len(c.FlagPatterns) > 0 {
		return false
	}
	if len(c.SideEffect) != 1 || c.SideEffect[0] != "read" || c.ApprovalTier != "ground" || c.Idempotent == nil || c.Egress != "none" || c.SecretAccess != "none" {
		return false
	}
	var t kinds.Tool
	if json.Unmarshal(r.Payload, &t) != nil {
		return false
	}
	var hints struct {
		ReadOnly    *bool `json:"readOnlyHint"`
		Destructive *bool `json:"destructiveHint"`
		Idempotent  *bool `json:"idempotentHint"`
		OpenWorld   *bool `json:"openWorldHint"`
	}
	if json.Unmarshal(t.Definition.Annotations, &hints) != nil {
		return false
	}
	if hints.ReadOnly == nil || !*hints.ReadOnly || hints.Destructive == nil || *hints.Destructive || hints.OpenWorld == nil || *hints.OpenWorld {
		return false
	}
	if *c.Idempotent && (hints.Idempotent == nil || !*hints.Idempotent) {
		return false
	}
	return true
}

func incomingEdges(ctx context.Context, q store.Querier, id string) ([]Edge, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.body FROM records c JOIN revisions r ON c.seq=r.seq WHERE r.lifecycle!='deleted'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	edges := []Edge{}
	for rows.Next() {
		var raw []byte
		var r Record
		if checkErr := rows.Scan(&raw); checkErr != nil {
			return nil, checkErr
		}
		if checkErr := json.Unmarshal(raw, &r); checkErr != nil {
			return nil, checkErr
		}
		for _, e := range r.Edges {
			if e.Relation.To.Authority == "registry" && e.Relation.To.ID == id {
				e.Inverse, _ = kinds.Inverse(e.Relation.Type)
				edges = append(edges, e)
			}
		}
	}
	if checkErr := rows.Err(); checkErr != nil {
		return nil, checkErr
	}
	if checkErr := rows.Close(); checkErr != nil {
		return nil, checkErr
	}
	resolved := Record{Edges: edges}
	if checkErr := resolveEdges(ctx, q, &resolved, -1); checkErr != nil {
		return nil, checkErr
	}
	return resolved.Edges, nil
}

// Proposals and metadata revisions cannot erase an authoritative same-hash
// decision. Compare the latest immutable decided revision, not current state.
func priorRestrictsGround(ctx context.Context, q store.Querier, r Record) (bool, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT body FROM revisions WHERE id=? AND json_extract(body,'$.review.definition_hash')=? AND COALESCE(json_extract(body,'$.review.reviewed_by'),'')!='' AND json_extract(body,'$.review.state') IN ('reviewed','denied','quarantined') ORDER BY seq DESC LIMIT 1`, r.ID, r.Review.DefinitionHash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var prior Record
	if err := json.Unmarshal(raw, &prior); err != nil {
		return false, err
	}
	if prior.Review.State != "reviewed" || prior.Review.Effective == nil {
		return true, nil
	}
	return !groundReview(prior, prior.Review.Effective), nil
}

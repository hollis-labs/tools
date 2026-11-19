package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
)

// ImportRequest is a complete observed snapshot, never an instruction to delete
// records absent from that snapshot. Connectors remain temporary and unscheduled.
type ImportRequest struct {
	Connector          string        `json:"connector"`
	ExpectedCheckpoint string        `json:"expected_checkpoint"`
	Checkpoint         string        `json:"checkpoint"`
	Entries            []ImportEntry `json:"entries"`
}
type ImportEntry struct {
	SourceKey      string `json:"source_key"`
	SourceRevision string `json:"source_revision"`
	Record         *Input `json:"record"`
}
type ImportItem struct {
	SourceKey        string `json:"source_key"`
	SourceRevision   string `json:"source_revision"`
	SourceDigest     string `json:"source_digest"`
	ID               string `json:"id"`
	ExpectedRevision string `json:"expected_revision"`
	Action           string `json:"action"`
	Reason           string `json:"reason,omitempty"`
}
type ImportCounts struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Rejected  int `json:"rejected"`
}
type ImportPlan struct {
	ID               string        `json:"plan_id"`
	Caller           string        `json:"caller"`
	Authority        string        `json:"authority"`
	Request          ImportRequest `json:"request"`
	SourceDigest     string        `json:"source_digest"`
	Items            []ImportItem  `json:"items"`
	Counts           ImportCounts  `json:"counts"`
	Digest           string        `json:"digest"`
	Temporary        bool          `json:"temporary"`
	SchedulerEnabled bool          `json:"scheduler_enabled"`
}
type ImportResult struct {
	PlanID     string       `json:"plan_id"`
	Counts     ImportCounts `json:"counts"`
	Checkpoint string       `json:"checkpoint"`
	Replayed   bool         `json:"replayed"`
}

func sourceCheckpoint(ctx context.Context, q store.Querier, authority, connector string) (string, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT checkpoint FROM import_checkpoints WHERE authority=? AND connector=?`, authority, connector).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
func absent(err error) bool { var e *Error; return errors.As(err, &e) && e.Code == "not_found" }

func (s *Service) importCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	authority := s.authorities[p.ID]
	if authority == "" {
		return nil, failure("forbidden", "authority", "configured owning authority required")
	}
	if op == "import_checkpoint" {
		if strings.TrimSpace(a.ID) == "" {
			return nil, failure("validation_error", "id", "connector required")
		}
		checkpoint, err := sourceCheckpoint(ctx, s.DB, authority, a.ID)
		return map[string]any{"connector": a.ID, "checkpoint": checkpoint, "temporary": true, "scheduler_enabled": false}, err
	}
	var out any
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		if op == "import_prepare" {
			if a.Import == nil {
				return failure("validation_error", "import", "snapshot required")
			}
			request := *a.Import
			if strings.TrimSpace(request.Connector) == "" || strings.TrimSpace(request.Checkpoint) == "" || len(request.Entries) == 0 || len(request.Entries) > 200 {
				return failure("validation_error", "import", "connector, checkpoint and 1..200 entries required")
			}
			// Copy before sorting; direct Go callers retain ownership of their slice.
			request.Entries = append([]ImportEntry(nil), request.Entries...)
			sort.Slice(request.Entries, func(i, j int) bool { return request.Entries[i].SourceKey < request.Entries[j].SourceKey })
			checkpoint, err := sourceCheckpoint(ctx, tx, authority, request.Connector)
			if err != nil {
				return err
			}
			if checkpoint != request.ExpectedCheckpoint {
				return failure("version_conflict", "expected_checkpoint", "source checkpoint changed")
			}
			plan := ImportPlan{ID: "import:" + newID(), Caller: p.ID, Authority: authority, Request: request, Items: []ImportItem{}, Temporary: true}
			plan.SourceDigest, err = canonicalDigest(request)
			if err != nil {
				return err
			}
			seenKeys := map[string]bool{}
			seenIDs := map[string]bool{}
			for _, entry := range request.Entries {
				item := ImportItem{SourceKey: entry.SourceKey, SourceRevision: entry.SourceRevision, Action: "rejected"}
				next, prepareErr := prepare(entry.Record, authority)
				if prepareErr != nil {
					item.Reason = "invalid_record"
				} else if entry.SourceKey == "" || entry.SourceRevision == "" || seenKeys[entry.SourceKey] || seenIDs[next.ID] {
					item.Reason = "duplicate_or_missing_source_identity"
				} else {
					seenKeys[entry.SourceKey] = true
					seenIDs[next.ID] = true
					item.ID = next.ID
					item.SourceDigest, _ = canonicalDigest(entry.Record)
					current, e := readRecord(ctx, tx, next.ID, -1)
					if e != nil && !absent(e) {
						return e
					}
					if e == nil {
						item.ExpectedRevision = current.RevisionID
					}
					var oldID, oldSourceRevision, oldDigest, oldRegistry string
					e = tx.QueryRowContext(ctx, `SELECT id,source_revision,source_digest,registry_revision FROM import_sources WHERE authority=? AND connector=? AND source_key=?`, authority, request.Connector, entry.SourceKey).Scan(&oldID, &oldSourceRevision, &oldDigest, &oldRegistry)
					if e != nil && !errors.Is(e, sql.ErrNoRows) {
						return e
					}
					switch {
					case e == nil && oldID != next.ID:
						item.Reason = "source_identity_changed"
					case e == nil && oldSourceRevision == entry.SourceRevision && oldDigest != item.SourceDigest:
						item.Reason = "source_revision_reused"
					case e == nil && current.RevisionID != oldRegistry:
						if oldDigest != item.SourceDigest {
							item.Reason = "both_sides_changed"
						} else {
							item.Reason = "registry_changed"
						}
					case e == nil && oldDigest == item.SourceDigest:
						item.Action = "unchanged"
					case e == nil && current.Authority == authority && current.Lifecycle != "deleted" && current.Lifecycle != "archived":
						item.Action = "updated"
					case e == nil:
						item.Reason = "record_unavailable"
					case item.ExpectedRevision != "":
						item.Reason = "untracked_registry_identity"
					default:
						item.Action = "created"
					}
					// A different stable key cannot claim the same identity later.
					var claimed string
					e = tx.QueryRowContext(ctx, `SELECT source_key FROM import_sources WHERE authority=? AND connector=? AND id=?`, authority, request.Connector, next.ID).Scan(&claimed)
					if e == nil && claimed != entry.SourceKey {
						item.Action = "rejected"
						item.Reason = "source_key_conflict"
					} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
						return e
					}
				}
				switch item.Action {
				case "created":
					plan.Counts.Created++
				case "updated":
					plan.Counts.Updated++
				case "unchanged":
					plan.Counts.Unchanged++
				default:
					plan.Counts.Rejected++
				}
				plan.Items = append(plan.Items, item)
			}
			plan.Digest, err = canonicalDigest(plan)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO import_plans(id,caller,authority,body) VALUES(?,?,?,?)`, plan.ID, p.ID, authority, raw)
			out = plan
			return err
		}
		if a.PlanID == "" || a.Hash == "" || a.SourceDigest == "" || a.IdempotencyKey == "" {
			return failure("validation_error", "plan_id", "plan, digest, source confirmation and idempotency key required")
		}
		var oldHash string
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT digest,body FROM import_receipts WHERE caller=? AND authority=? AND key=?`, p.ID, authority, a.IdempotencyKey).Scan(&oldHash, &raw)
		if err == nil {
			if oldHash != digest(a) {
				return failure("version_conflict", "idempotency_key", "key reused with changed request")
			}
			var result ImportResult
			if err = json.Unmarshal(raw, &result); err != nil {
				return err
			}
			result.Replayed = true
			out = result
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT body FROM import_plans WHERE id=? AND caller=? AND authority=?`, a.PlanID, p.ID, authority).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("not_found", "plan_id", "caller-owned plan not found")
		}
		if err != nil {
			return err
		}
		var plan ImportPlan
		if err = json.Unmarshal(raw, &plan); err != nil {
			return err
		}
		if plan.Digest != a.Hash || plan.SourceDigest != a.SourceDigest {
			return failure("version_conflict", "hash", "plan or source snapshot changed")
		}
		if plan.Counts.Rejected > 0 {
			return failure("validation_error", "import", "batch contains rejected entries; nothing committed")
		}
		checkpoint, err := sourceCheckpoint(ctx, tx, authority, plan.Request.Connector)
		if err != nil {
			return err
		}
		if checkpoint != plan.Request.ExpectedCheckpoint {
			return failure("version_conflict", "expected_checkpoint", "checkpoint changed after prepare")
		}
		// Check every baseline before writing any record. WriteTx also rolls back
		// revisions, aliases, source bindings, receipt and checkpoint on any failure.
		for _, item := range plan.Items {
			var r Record
			r, err = readRecord(ctx, tx, item.ID, -1)
			if err != nil && !absent(err) {
				return err
			}
			if r.RevisionID != item.ExpectedRevision {
				return failure("version_conflict", "version", "registry changed after prepare")
			}
		}
		for i, item := range plan.Items {
			revision := item.ExpectedRevision
			if item.Action != "unchanged" {
				var r Record
				r, err = prepare(plan.Request.Entries[i].Record, authority)
				if err != nil {
					return err
				}
				if item.Action == "updated" {
					var old Record
					old, err = readRecord(ctx, tx, r.ID, -1)
					if err != nil {
						return err
					}
					r.CreatedAt = old.CreatedAt
					if r.Kind != "project" {
						for _, e := range old.Edges {
							if !isPayloadEdge(e) {
								r.Edges = append(r.Edges, e)
							}
						}
					}
					if r.Review != nil && old.Review != nil && r.Review.DefinitionHash == old.Review.DefinitionHash {
						r.Review = old.Review
					}
				} else {
					r.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				}
				if err = save(ctx, tx, &r); err != nil {
					return err
				}
				revision = r.RevisionID
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO import_sources(authority,connector,source_key,id,source_revision,source_digest,registry_revision) VALUES(?,?,?,?,?,?,?) ON CONFLICT(authority,connector,source_key) DO UPDATE SET source_revision=excluded.source_revision,source_digest=excluded.source_digest,registry_revision=excluded.registry_revision`, authority, plan.Request.Connector, item.SourceKey, item.ID, item.SourceRevision, item.SourceDigest, revision)
			if err != nil {
				return err
			}
		}
		// Validate the resulting graph after inserting the complete batch, so forward
		// references work without weakening admission for unresolved dependencies.
		for _, item := range plan.Items {
			var r Record
			r, err = readRecord(ctx, tx, item.ID, -1)
			if err != nil {
				return err
			}
			if err = checkEdges(ctx, tx, r, false); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO import_checkpoints(authority,connector,checkpoint) VALUES(?,?,?) ON CONFLICT(authority,connector) DO UPDATE SET checkpoint=excluded.checkpoint`, authority, plan.Request.Connector, plan.Request.Checkpoint)
		if err != nil {
			return err
		}
		result := ImportResult{PlanID: plan.ID, Counts: plan.Counts, Checkpoint: plan.Request.Checkpoint}
		raw, err = json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO import_receipts(caller,authority,key,digest,body) VALUES(?,?,?,?,?)`, p.ID, authority, a.IdempotencyKey, digest(a), raw)
		out = result
		return err
	})
	return out, err
}

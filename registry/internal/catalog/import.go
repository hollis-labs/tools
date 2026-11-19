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
	"github.com/hollis-labs/substrate/kinds"
)

type ImportLimits struct {
	MaxBytes           int `json:"max_bytes"`
	MaxItems           int `json:"max_items"`
	MaxActive          int `json:"max_active_preparations"`
	EligibilitySeconds int `json:"eligibility_seconds"`
}

func DefaultImportLimits() ImportLimits {
	return ImportLimits{MaxBytes: MaxRequestBytes, MaxItems: 200, MaxActive: 20, EligibilitySeconds: 86400}
}
func (s *Service) ConfigureImportLimits(limits ImportLimits) error {
	if limits.MaxBytes < 1 || limits.MaxBytes > MaxRequestBytes || limits.MaxItems < 1 || limits.MaxItems > 200 || limits.MaxActive < 1 || limits.MaxActive > 20 || limits.EligibilitySeconds < 1 || limits.EligibilitySeconds > 86400 {
		return failure("validation_error", "import_limits", "limits must be positive and no greater than 2 MiB, 200 items, 20 active, 86400 seconds")
	}
	s.importLimits = limits
	return nil
}

type ImportRequest struct {
	Connector          string        `json:"connector"`
	SourceAuthority    string        `json:"source_authority"`
	MappingVersion     string        `json:"mapping_version"`
	ExpectedCheckpoint string        `json:"expected_checkpoint"`
	Checkpoint         string        `json:"checkpoint"`
	Entries            []ImportEntry `json:"entries"`
}
type ImportEntry struct {
	SourceKey              string `json:"source_key"`
	SourceRevision         string `json:"source_revision"`
	ExpectedSourceRevision string `json:"expected_source_revision,omitempty"`
	Record                 *Input `json:"record"`
}
type ImportItem struct {
	SourceKey               string `json:"source_key"`
	SourceRevision          string `json:"source_revision"`
	SourceDigest            string `json:"source_digest"`
	ID                      string `json:"id"`
	ExpectedRevision        string `json:"expected_revision"`
	ExpectedEdgeSetRevision string `json:"expected_edge_set_revision"`
	ExpectedSourceState     string `json:"expected_source_state"`
	ProposedRevision        string `json:"proposed_revision"`
	ProposedEdgeSetRevision string `json:"proposed_edge_set_revision"`
	Action                  string `json:"action"`
	Reason                  string `json:"reason,omitempty"`
}
type ImportCounts struct {
	Created      int `json:"created"`
	Updated      int `json:"updated"`
	Unchanged    int `json:"unchanged"`
	Rejected     int `json:"rejected"`
	NotCommitted int `json:"not_committed"`
}
type GraphDiagnostic struct {
	RecordID  string   `json:"record_id"`
	CyclePath []string `json:"cycle_path,omitempty"`
}
type ImportPlan struct {
	Admission       Admission        `json:"admission"`
	GraphDiagnostic *GraphDiagnostic `json:"graph_diagnostic,omitempty"`
	ID              string           `json:"plan_id"`
	Caller          string           `json:"caller"`
	Authority       string           `json:"authority"`
	Request         ImportRequest    `json:"request"`
	SourceDigest    string           `json:"source_digest"`
	Items           []ImportItem     `json:"items"`
	Candidates      []Record         `json:"candidates"`
	// Counts are prospective only; committed outcomes exist in ImportResult.
	Counts           ImportCounts `json:"planned_counts"`
	Digest           string       `json:"digest"`
	PreparedAt       string       `json:"prepared_at"`
	ExpiresAt        string       `json:"expires_at"`
	Expired          bool         `json:"expired"`
	Limits           ImportLimits `json:"limits"`
	Temporary        bool         `json:"temporary"`
	SchedulerEnabled bool         `json:"scheduler_enabled"`
}
type ImportResult struct {
	ReceiptID  string       `json:"receipt_id"`
	PlanID     string       `json:"plan_id"`
	Counts     ImportCounts `json:"counts"`
	Checkpoint string       `json:"checkpoint"`
	Replayed   bool         `json:"replayed"`
}
type ImportFailure struct {
	PlanID     string       `json:"plan_id"`
	InputCount int          `json:"input_count"`
	Counts     ImportCounts `json:"counts"`
	Items      []ImportItem `json:"items"`
}
type SourceCheckpoint struct {
	CurrentRegistryRevision string `json:"current_registry_revision,omitempty"`
	CurrentAvailability     string `json:"current_availability,omitempty"`
	SourceAuthority         string `json:"source_authority"`
	NativeID                string `json:"native_id"`
	ID                      string `json:"registry_id"`
	SourceRevision          string `json:"accepted_source_revision"`
	SourceDigest            string `json:"accepted_source_digest"`
	MappingVersion          string `json:"mapping_version"`
	RegistryRevision        string `json:"resulting_registry_revision"`
	EdgeSetRevision         string `json:"resulting_edge_set_revision"`
	ReceiptID               string `json:"import_receipt_id"`
}

func sourceCheckpoint(ctx context.Context, q store.Querier, authority, connector string) (string, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT checkpoint FROM import_checkpoints WHERE authority=? AND connector=?`, authority, connector).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
func sourceState(ctx context.Context, q store.Querier, authority, connector, key string) (SourceCheckpoint, bool, error) {
	c := SourceCheckpoint{NativeID: key}
	err := q.QueryRowContext(ctx, `SELECT id,source_revision,source_digest,registry_revision,source_authority,mapping_version,edge_set_revision,import_receipt_id FROM import_sources WHERE authority=? AND connector=? AND source_key=?`, authority, connector, key).Scan(&c.ID, &c.SourceRevision, &c.SourceDigest, &c.RegistryRevision, &c.SourceAuthority, &c.MappingVersion, &c.EdgeSetRevision, &c.ReceiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceCheckpoint{}, false, nil
	}
	return c, err == nil, err
}
func absent(err error) bool { var e *Error; return errors.As(err, &e) && e.Code == "not_found" }
func preparationDigest(p ImportPlan) (string, error) {
	p.Digest = ""
	p.Expired = false
	return canonicalDigest(p)
}
func (s *Service) importCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	authority := s.authorities[p.ID]
	if authority == "" {
		return nil, failure("forbidden", "authority", "configured owning authority required")
	}
	if op == "import_checkpoint" {
		if strings.TrimSpace(a.ID) == "" {
			return nil, failure("validation_error", "id", "connector required")
		}
		var checkpoint string
		states := []SourceCheckpoint{}
		err := s.DB.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
			var err error
			checkpoint, err = sourceCheckpoint(ctx, q, authority, a.ID)
			if err != nil {
				return err
			}
			rows, err := q.QueryContext(ctx, `SELECT source_key FROM import_sources WHERE authority=? AND connector=? ORDER BY source_key`, authority, a.ID)
			if err != nil {
				return err
			}
			keys := []string{}
			for rows.Next() {
				var key string
				if err = rows.Scan(&key); err != nil {
					_ = rows.Close()
					return err
				}
				keys = append(keys, key)
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			for _, key := range keys {
				state, _, e := sourceState(ctx, q, authority, a.ID, key)
				if e != nil {
					return e
				}
				current, currentErr := readRecord(ctx, q, state.ID, -1)
				if currentErr != nil && !absent(currentErr) {
					return currentErr
				}
				state.CurrentRegistryRevision = current.RevisionID
				state.CurrentAvailability = current.Lifecycle
				if currentErr != nil {
					state.CurrentAvailability = "unavailable"
				}
				states = append(states, state)
			}
			return nil
		})
		return map[string]any{"connector": a.ID, "checkpoint": checkpoint, "sources": states, "temporary": true, "scheduler_enabled": false}, err
	}
	if op == "import_prepare" {
		return s.prepareImport(ctx, p.ID, authority, a)
	}
	return s.commitImport(ctx, p.ID, authority, a)
}
func (s *Service) prepareImport(ctx context.Context, caller, authority string, a Args) (ImportPlan, error) {
	var out ImportPlan
	if a.Import == nil || a.IdempotencyKey == "" {
		return out, failure("validation_error", "import", "snapshot and scoped preparation idempotency key required")
	}
	raw, marshalErr := json.Marshal(a)
	if marshalErr != nil {
		return out, marshalErr
	}
	if len(raw) > MaxRequestBytes {
		return out, failure("quota_exceeded", "import", "preparation byte cap exceeded; no allocation")
	}
	request := *a.Import
	request.Entries = append([]ImportEntry(nil), request.Entries...)
	if request.Connector == "" || request.SourceAuthority == "" || request.MappingVersion == "" || request.Checkpoint == "" || len(request.Entries) == 0 {
		return out, failure("validation_error", "import", "connector/source authority/mapping/checkpoint and bounded entries required")
	}
	if len(request.Entries) > 200 {
		return out, failure("quota_exceeded", "import", "200 item cap exceeded; no allocation")
	}
	sort.Slice(request.Entries, func(i, j int) bool { return request.Entries[i].SourceKey < request.Entries[j].SourceKey })
	normalized := a
	normalized.Import = &request
	requestDigest, digestErr := canonicalDigest(normalized)
	if digestErr != nil {
		return out, digestErr
	}
	now := s.now().UTC()
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var storedDigest string
		var stored []byte
		lookupErr := tx.QueryRowContext(ctx, `SELECT k.digest,p.body FROM import_prepare_keys k JOIN import_plans p ON p.id=k.plan_id WHERE k.caller=? AND k.authority=? AND k.key=?`, caller, authority, a.IdempotencyKey).Scan(&storedDigest, &stored)
		if lookupErr == nil {
			if storedDigest != requestDigest {
				return failure("version_conflict", "idempotency_key", "preparation key reused with changed request")
			}
			if err := json.Unmarshal(stored, &out); err != nil {
				return err
			}
			expiry, err := time.Parse(time.RFC3339Nano, out.ExpiresAt)
			if err != nil {
				return err
			}
			out.Expired = !now.Before(expiry)
			return nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return lookupErr
		}
		if len(raw) > s.importLimits.MaxBytes || len(request.Entries) > s.importLimits.MaxItems {
			return failure("quota_exceeded", "import", "configured byte/item cap exceeded; no allocation")
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_plans WHERE caller=? AND committed=0 AND julianday(expires_at)>julianday(?)`, caller, now.Format(time.RFC3339Nano)).Scan(&active); err != nil {
			return err
		}
		if active >= s.importLimits.MaxActive {
			return failure("quota_exceeded", "import", "active preparation quota exceeded; no allocation")
		}
		cp, err := sourceCheckpoint(ctx, tx, authority, request.Connector)
		if err != nil {
			return err
		}
		if cp != request.ExpectedCheckpoint {
			return failure("version_conflict", "expected_checkpoint", "source checkpoint changed")
		}
		plan := ImportPlan{ID: "import:" + newID(), Caller: caller, Authority: authority, Request: request, PreparedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(time.Duration(s.importLimits.EligibilitySeconds) * time.Second).Format(time.RFC3339Nano), Limits: s.importLimits, Temporary: true, Items: []ImportItem{}, Candidates: []Record{}}
		plan.Admission, err = schemaAdmission(request.MappingVersion)
		if err != nil {
			return err
		}
		plan.SourceDigest, err = canonicalDigest(request)
		if err != nil {
			return err
		}
		seenKeys := map[string]bool{}
		seenIDs := map[string]bool{}
		for _, entry := range request.Entries {
			item := ImportItem{SourceKey: entry.SourceKey, SourceRevision: entry.SourceRevision, Action: "rejected"}
			candidate, admissionErr := prepare(entry.Record, authority)
			if admissionErr != nil {
				item.Reason = "invalid_record"
			} else if entry.SourceKey == "" || entry.SourceRevision == "" || seenKeys[entry.SourceKey] || seenIDs[candidate.ID] {
				item.Reason = "duplicate_or_missing_source_identity"
			} else {
				seenKeys[entry.SourceKey] = true
				seenIDs[candidate.ID] = true
				item.ID = candidate.ID
				item.SourceDigest, _ = canonicalDigest(entry.Record)
				current, readErr := readRecord(ctx, tx, candidate.ID, -1)
				if readErr != nil && !absent(readErr) {
					return readErr
				}
				if readErr == nil {
					item.ExpectedRevision = current.RevisionID
					item.ExpectedEdgeSetRevision = current.RevisionID
				}
				old, tracked, stateErr := sourceState(ctx, tx, authority, request.Connector, entry.SourceKey)
				if stateErr != nil {
					return stateErr
				}
				item.ExpectedSourceState, _ = canonicalDigest(old)
				switch {
				case tracked && (old.ID != candidate.ID || old.SourceAuthority != request.SourceAuthority):
					item.Reason = "source_identity_changed"
				case tracked && old.MappingVersion == request.MappingVersion && old.SourceRevision == entry.SourceRevision && old.SourceDigest != item.SourceDigest:
					item.Reason = "source_revision_reused"
				case tracked && (current.RevisionID != old.RegistryRevision || current.RevisionID != old.EdgeSetRevision):
					if old.SourceDigest != item.SourceDigest || old.MappingVersion != request.MappingVersion {
						item.Reason = "both_sides_changed"
					} else {
						item.Reason = "registry_changed"
					}
				case tracked && (current.Authority != authority || current.Lifecycle == "archived" || current.Lifecycle == "deleted"):
					item.Reason = "record_unavailable"
				case tracked && old.SourceRevision != entry.SourceRevision && entry.ExpectedSourceRevision != old.SourceRevision:
					item.Reason = "source_order_unverified"
				case tracked && old.SourceDigest == item.SourceDigest && old.MappingVersion == request.MappingVersion:
					item.Action = "unchanged"
				case tracked:
					item.Action = "updated"
				case item.ExpectedRevision != "":
					item.Reason = "untracked_registry_identity"
				default:
					item.Action = "created"
				}
				var claimed string
				stateErr = tx.QueryRowContext(ctx, `SELECT source_key FROM import_sources WHERE authority=? AND connector=? AND id=?`, authority, request.Connector, candidate.ID).Scan(&claimed)
				if stateErr == nil && claimed != entry.SourceKey {
					item.Action = "rejected"
					item.Reason = "source_key_conflict"
				} else if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
					return stateErr
				}
				if item.Action == "unchanged" {
					candidate = current
				} else if item.Action != "rejected" {
					candidate.CreatedAt = now.Format(time.RFC3339Nano)
					if readErr == nil {
						candidate.CreatedAt = current.CreatedAt
						if candidate.Kind != "project" {
							for _, edge := range current.Edges {
								if !isPayloadEdge(edge) {
									candidate.Edges = append(candidate.Edges, edge)
								}
							}
						}
						if candidate.Review != nil && current.Review != nil && candidate.Review.DefinitionHash == current.Review.DefinitionHash {
							candidate.Review = current.Review
						}
					}
					candidate.RevisionID = newID()
					candidate.VersionToken = candidate.RevisionID
					candidate.UpdatedAt = now.Format(time.RFC3339Nano)
					candidate.MappingVersion = request.MappingVersion
					candidate.Publication = "draft"
					for i := range candidate.Edges {
						candidate.Edges[i].SourceRevision = candidate.RevisionID
					}
				}
				item.ProposedRevision = candidate.RevisionID
				item.ProposedEdgeSetRevision = candidate.RevisionID
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
				candidate = Record{}
			}
			plan.Items = append(plan.Items, item)
			plan.Candidates = append(plan.Candidates, candidate)
		}
		// Resolve internal forward targets against the jointly allocated staging set.
		proposed := map[string]Record{}
		for i, c := range plan.Candidates {
			if plan.Items[i].Action != "rejected" {
				proposed[c.ID] = c
			}
		}
		for i := range plan.Candidates {
			if plan.Items[i].Action == "created" || plan.Items[i].Action == "updated" {
				for j := range plan.Candidates[i].Edges {
					e := &plan.Candidates[i].Edges[j]
					if e.Relation.To.Authority == "registry" && e.TargetRevision == "" {
						target, ok := proposed[e.Relation.To.ID]
						if !ok {
							target, err = readRecord(ctx, tx, e.Relation.To.ID, -1)
							if err != nil && !absent(err) {
								return err
							}
						}
						if plan.Candidates[i].DependencyPins == nil {
							plan.Candidates[i].DependencyPins = map[string]string{}
						}
						plan.Candidates[i].DependencyPins[e.ID] = target.RevisionID
					}
				}
			}
		}
		// Graph errors reject the whole staged batch without inserting canonical rows.
		if plan.Counts.Rejected == 0 {
			if graphErr := validateImportGraph(ctx, tx, plan.Candidates); graphErr != nil {
				rejectedIndex := 0
				if diagnostic, ok := internalError(graphErr).Details.(GraphDiagnostic); ok {
					plan.GraphDiagnostic = &diagnostic
					for i, item := range plan.Items {
						if item.ID == diagnostic.RecordID {
							rejectedIndex = i
							break
						}
					}
				}
				plan.Items[rejectedIndex].Action = "rejected"
				plan.Items[rejectedIndex].Reason = internalError(graphErr).Code
				plan.Counts = ImportCounts{Rejected: 1, NotCommitted: len(plan.Items) - 1}
			}
		}
		for i, item := range plan.Items {
			if item.Action == "rejected" {
				plan.Request.Entries[i].Record = nil
			}
		}
		plan.Digest, err = preparationDigest(plan)
		if err != nil {
			return err
		}
		body, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO import_plans(id,caller,authority,body,expires_at) VALUES(?,?,?,?,?)`, plan.ID, caller, authority, body, plan.ExpiresAt); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO import_prepare_keys(caller,authority,key,digest,plan_id) VALUES(?,?,?,?,?)`, caller, authority, a.IdempotencyKey, requestDigest, plan.ID)
		out = plan
		return err
	})
	return out, err
}
func (s *Service) commitImport(ctx context.Context, caller, authority string, a Args) (ImportResult, error) {
	var out ImportResult
	var attempted ImportPlan
	failedIndex := -1
	if a.PlanID == "" || a.Hash == "" || a.SourceDigest == "" || a.IdempotencyKey == "" {
		return out, failure("validation_error", "plan_id", "plan/digest/source confirmation and commit key required")
	}
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var oldHash string
		var raw []byte
		replayErr := tx.QueryRowContext(ctx, `SELECT digest,body FROM import_receipts WHERE caller=? AND authority=? AND key=?`, caller, authority, a.IdempotencyKey).Scan(&oldHash, &raw)
		if replayErr == nil {
			if oldHash != digest(a) {
				return failure("version_conflict", "idempotency_key", "commit key reused with changed request")
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return err
			}
			out.Replayed = true
			return nil
		}
		if !errors.Is(replayErr, sql.ErrNoRows) {
			return replayErr
		}
		var committed bool
		err := tx.QueryRowContext(ctx, `SELECT body,committed FROM import_plans WHERE id=? AND caller=? AND authority=?`, a.PlanID, caller, authority).Scan(&raw, &committed)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("not_found", "plan_id", "caller-owned preparation not found")
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &attempted); err != nil {
			return err
		}
		plan := attempted
		hash, err := preparationDigest(plan)
		if err != nil {
			return err
		}
		if hash != plan.Digest || hash != a.Hash || plan.SourceDigest != a.SourceDigest {
			return failure("version_conflict", "hash", "prepared manifest or confirmed source changed")
		}
		admission, err := schemaAdmission(plan.Request.MappingVersion)
		if err != nil {
			return err
		}
		if admission != plan.Admission {
			return failure("version_conflict", "admission", "schema/mapping admission changed after preparation")
		}
		expiry, err := time.Parse(time.RFC3339Nano, plan.ExpiresAt)
		if err != nil {
			return err
		}
		if !s.now().UTC().Before(expiry) {
			return failure("version_conflict", "expires_at", "preparation commit eligibility expired; use a new key")
		}
		if committed {
			return failure("version_conflict", "plan_id", "preparation already committed; replay the original commit key")
		}
		if plan.Counts.Rejected > 0 {
			return failure("validation_error", "import", "rejected staged batch; zero canonical changes")
		}
		cp, err := sourceCheckpoint(ctx, tx, authority, plan.Request.Connector)
		if err != nil {
			return err
		}
		if cp != plan.Request.ExpectedCheckpoint {
			return failure("version_conflict", "expected_checkpoint", "checkpoint changed after prepare")
		}
		for i, item := range plan.Items {
			failedIndex = i
			r, readErr := readRecord(ctx, tx, item.ID, -1)
			if readErr != nil && !absent(readErr) {
				return readErr
			}
			if r.RevisionID != item.ExpectedRevision || r.RevisionID != item.ExpectedEdgeSetRevision {
				return failure("version_conflict", "version", "record/edge base changed after prepare")
			}
			state, _, stateErr := sourceState(ctx, tx, authority, plan.Request.Connector, item.SourceKey)
			if stateErr != nil {
				return stateErr
			}
			stateDigest, _ := canonicalDigest(state)
			if stateDigest != item.ExpectedSourceState {
				return failure("version_conflict", "source", "source position changed after prepare")
			}
			if r.RevisionID != "" && (r.Authority != authority || r.Lifecycle == "archived" || r.Lifecycle == "deleted") {
				return failure("forbidden", "authority", "prepared base is no longer eligible")
			}
			admitted, admissionErr := prepare(plan.Request.Entries[i].Record, authority)
			if admissionErr != nil {
				return admissionErr
			}
			if admitted.ID != item.ID {
				return failure("version_conflict", "record", "candidate identity changed")
			}
		}
		failedIndex = -1
		if err = validateImportGraph(ctx, tx, plan.Candidates); err != nil {
			if diagnostic, ok := internalError(err).Details.(GraphDiagnostic); ok {
				for i, item := range plan.Items {
					if item.ID == diagnostic.RecordID {
						failedIndex = i
						break
					}
				}
			}
			return err
		}
		receiptID := "import-result:" + newID()
		for i, item := range plan.Items {
			failedIndex = i
			r := plan.Candidates[i]
			if item.Action != "unchanged" {
				if r.RevisionID != item.ProposedRevision || r.RevisionID != item.ProposedEdgeSetRevision {
					return failure("version_conflict", "revision", "prepared revision binding changed")
				}
				if err = savePrepared(ctx, tx, &r); err != nil {
					return err
				}
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO import_sources(authority,connector,source_key,id,source_revision,source_digest,registry_revision,source_authority,mapping_version,edge_set_revision,import_receipt_id) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(authority,connector,source_key) DO UPDATE SET source_revision=excluded.source_revision,source_digest=excluded.source_digest,registry_revision=excluded.registry_revision,source_authority=excluded.source_authority,mapping_version=excluded.mapping_version,edge_set_revision=excluded.edge_set_revision,import_receipt_id=excluded.import_receipt_id`, authority, plan.Request.Connector, item.SourceKey, item.ID, item.SourceRevision, item.SourceDigest, r.RevisionID, plan.Request.SourceAuthority, plan.Request.MappingVersion, r.RevisionID, receiptID)
			if err != nil {
				return err
			}
		}
		failedIndex = -1
		_, err = tx.ExecContext(ctx, `INSERT INTO import_checkpoints(authority,connector,checkpoint) VALUES(?,?,?) ON CONFLICT(authority,connector) DO UPDATE SET checkpoint=excluded.checkpoint`, authority, plan.Request.Connector, plan.Request.Checkpoint)
		if err != nil {
			return err
		}
		out = ImportResult{ReceiptID: receiptID, PlanID: plan.ID, Counts: plan.Counts, Checkpoint: plan.Request.Checkpoint}
		raw, err = json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO import_receipts(caller,authority,key,digest,body) VALUES(?,?,?,?,?)`, caller, authority, a.IdempotencyKey, digest(a), raw); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE import_plans SET committed=1 WHERE id=?`, plan.ID)
		return err
	})
	if err != nil && attempted.ID != "" {
		report := ImportFailure{PlanID: attempted.ID, InputCount: len(attempted.Items), Items: append([]ImportItem(nil), attempted.Items...)}
		for i := range report.Items {
			if report.Items[i].Action == "rejected" || i == failedIndex {
				report.Items[i].Action = "rejected"
				report.Counts.Rejected++
			} else {
				report.Items[i].Action = "not_committed"
				report.Counts.NotCommitted++
			}
		}
		if report.Counts.Rejected == 0 && len(report.Items) > 0 {
			report.Items[0].Action = "rejected"
			report.Items[0].Reason = internalError(err).Code
			report.Counts.Rejected++
			report.Counts.NotCommitted--
		}
		raw, _ := json.Marshal(report)
		// Failure evidence is deliberately separate from the rolled-back canonical
		// transaction and contains only sanitized identities/counts, never raw inputs.
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, logErr := s.DB.ExecContext(logCtx, `INSERT INTO import_failures(id,plan_id,attempted_at,body) VALUES(?,?,?,?)`, newID(), attempted.ID, s.now().UTC().Format(time.RFC3339Nano), raw)
		wire := *internalError(err)
		wire.Details = report
		if logErr != nil {
			wire.Remedy += " Failure evidence could not be persisted."
		}
		return out, &wire
	}
	return out, err
}

// validateImportGraph sees staged nodes only inside this call. No normal read
// can observe them. It validates final identities, target eligibility and cycles.
func validateImportGraph(ctx context.Context, q store.Querier, records []Record) error {
	staged := map[string]Record{}
	for _, r := range records {
		staged[r.ID] = r
	}
	loaded := map[string]Record{}
	load := func(id string) (Record, error) {
		if r, ok := staged[id]; ok {
			return r, nil
		}
		if r, ok := loaded[id]; ok {
			return r, nil
		}
		if len(loaded) > 10000 {
			return Record{}, failure("validation_error", "edges", "import graph exceeds validation bound")
		}
		r, err := readRecord(ctx, q, id, -1)
		if err != nil {
			return r, err
		}
		loaded[id] = r
		return r, nil
	}
	var reaches func(string, string, map[string]bool) ([]string, error)
	reaches = func(from, to string, seen map[string]bool) ([]string, error) {
		if from == to {
			return []string{to}, nil
		}
		if seen[from] {
			return nil, nil
		}
		seen[from] = true
		r, err := load(from)
		if err != nil {
			return nil, err
		}
		for _, edge := range r.Edges {
			if edge.Relation.To.Authority == "registry" {
				path, e := reaches(edge.Relation.To.ID, to, seen)
				if e != nil {
					return nil, e
				}
				if path != nil {
					return append([]string{from}, path...), nil
				}
			}
		}
		return nil, nil
	}
	graphError := func(code, id, message string, path []string) *Error {
		e := failure(code, "edges", message)
		e.Details = GraphDiagnostic{RecordID: id, CyclePath: path}
		return e
	}

	// Include the reachable existing graph in cycle admission, rather than only
	// checking the relation types on imported records. The staged overlay wins
	// for every identity, including existing unresolved edges into the batch.
	reachable := append([]Record(nil), records...)
	seenRecords := map[string]bool{}
	for _, r := range records {
		seenRecords[r.ID] = true
	}
	for i := 0; i < len(reachable); i++ {
		for _, edge := range reachable[i].Edges {
			if edge.Relation.To.Authority != "registry" || seenRecords[edge.Relation.To.ID] {
				continue
			}
			target, err := load(edge.Relation.To.ID)
			if err != nil {
				continue
			} // The validation below reports unresolved targets.
			seenRecords[target.ID] = true
			reachable = append(reachable, target)
		}
	}
	for _, r := range reachable {
		for _, edge := range r.Edges {
			if err := edge.Relation.Validate(); err != nil {
				return invalid("edges", err)
			}
			ref := edge.Relation.To
			if ref.Authority != "registry" {
				continue
			}
			target, err := load(ref.ID)
			if err != nil {
				return graphError("reference_unresolved", r.ID, "required import target unavailable", nil)
			}
			if target.Kind != ref.Kind || target.Lifecycle == "archived" || target.Lifecycle == "deleted" {
				return graphError("reference_unavailable", r.ID, "import target kind or lifecycle ineligible", nil)
			}
			pin := edge.TargetRevision
			if pin == "" {
				pin = r.DependencyPins[edge.ID]
			}
			if pin != "" && pin != target.RevisionID {
				var id string
				if err = q.QueryRowContext(ctx, `SELECT id FROM revisions WHERE id=? AND token=?`, ref.ID, pin).Scan(&id); err != nil {
					return graphError("reference_unavailable", r.ID, "pinned import target revision unavailable", nil)
				}
			}
			allowed, err := kinds.AllowsCycle(edge.Relation.Type)
			if err != nil {
				return invalid("edges", err)
			}
			if !allowed {
				path, err := reaches(ref.ID, r.ID, map[string]bool{})
				if err != nil {
					return graphError("reference_unresolved", r.ID, "import graph target unavailable", nil)
				}
				if path != nil {
					return graphError("validation_error", r.ID, "forbidden import relation cycle", append([]string{r.ID}, path...))
				}
			}
		}
	}
	return nil
}

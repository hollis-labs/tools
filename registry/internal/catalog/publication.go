package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tools/registry/contracts"
)

// Candidate binds both the draft and the pointer that publication may replace.
// An empty expected pointer means this identity has never been published.
type Candidate struct {
	ID                        string `json:"id"`
	Revision                  string `json:"revision"`
	ExpectedPublishedRevision string `json:"expected_published_revision"`
}
type SubjectRecord struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Revision      string `json:"revision"`
	Authority     string `json:"authority"`
	ContentDigest string `json:"content_digest"`
}
type PinnedTarget struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
type SubjectEdge struct {
	ID         string         `json:"id"`
	Revision   string         `json:"revision"`
	Relation   string         `json:"relation"`
	Target     PinnedTarget   `json:"target"`
	Attributes kinds.Relation `json:"attributes"`
}
type Admission struct {
	SchemaDigest    string `json:"schema_digest"`
	MappingVersion  string `json:"mapping_version"`
	EnvelopeVersion string `json:"envelope_version"`
}
type PublicationSubject struct {
	Schema          string        `json:"schema"`
	Action          string        `json:"action"`
	Record          SubjectRecord `json:"record"`
	EdgeSetRevision string        `json:"edge_set_revision"`
	Edges           []SubjectEdge `json:"edges"`
	Admission       Admission     `json:"admission"`
}
type Approval struct {
	ID             string               `json:"approval_id"`
	Revision       string               `json:"receipt_revision"`
	Issuer         string               `json:"issuer"`
	PreparedBy     string               `json:"prepared_by"`
	Authority      string               `json:"authority"`
	Principal      string               `json:"principal"`
	PolicyRevision string               `json:"policy_revision"`
	Decision       string               `json:"decision"`
	Action         string               `json:"action"`
	Candidates     []Candidate          `json:"candidates"`
	Subjects       []PublicationSubject `json:"subjects"`
	SubjectDigests []string             `json:"subject_digests"`
	ManifestDigest string               `json:"batch_manifest_digest"`
	DecidedAt      string               `json:"decided_at"`
	ExpiresAt      string               `json:"expires_at"`
	Elevated       bool                 `json:"elevated"`
}
type PublicationResult struct {
	ID                  string            `json:"receipt_id"`
	ApprovalID          string            `json:"approval_id"`
	ApprovalRevision    string            `json:"approval_revision"`
	PublishedBy         string            `json:"published_by"`
	PublishedAt         string            `json:"published_at"`
	Records             []Record          `json:"records"`
	Replayed            bool              `json:"replayed"`
	CurrentAvailability map[string]string `json:"current_availability"`
}
type PublicationPolicy struct {
	Revision string            `json:"revision"`
	Roles    map[string]string `json:"roles"`
}

func canonicalDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if err = kinds.ValidateJSONValue(raw); err != nil {
		return "", err
	}
	b, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}
func readPublished(ctx context.Context, q store.Querier, id string, boundary int64) (Record, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT body FROM publication_events WHERE id=? AND (?<0 OR seq<=?) ORDER BY seq DESC LIMIT 1`, id, boundary, boundary).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, failure("not_found", "publication", "no published revision")
	}
	if err != nil {
		return Record{}, err
	}
	var r Record
	err = json.Unmarshal(raw, &r)
	if err == nil && boundary < 0 {
		err = overlayVerification(ctx, q, &r)
	}
	return r, err
}
func writePublished(ctx context.Context, tx store.Tx, r Record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO publication_events(id,revision,body) VALUES(?,?,?)`, r.ID, r.RevisionID, raw)
	if err != nil {
		return err
	}
	seq, err := result.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO published(id,seq) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET seq=excluded.seq`, r.ID, seq)
	return err
}
func recordPublicationLifecycle(ctx context.Context, tx store.Tx, r Record) error {
	old, err := readPublished(ctx, tx, r.ID, -1)
	var ce *Error
	if errors.As(err, &ce) && ce.Code == "not_found" {
		return nil
	}
	if err != nil {
		return err
	}
	old.Lifecycle = r.Lifecycle
	return writePublished(ctx, tx, old)
}
func loadApproval(ctx context.Context, q store.Querier, id string) (Approval, error) {
	var a Approval
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT body FROM publication_approvals WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, failure("not_found", "approval_id", "approval not found")
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	return a, err
}
func saveApproval(ctx context.Context, tx store.Tx, a Approval) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO publication_decisions(revision,approval_id,body) VALUES(?,?,?)`, a.Revision, a.ID, raw); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO publication_approvals(id,revision,body) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,body=excluded.body`, a.ID, a.Revision, raw)
	return err
}
func (s *Service) policy(ctx context.Context, tx store.Tx) (PublicationPolicy, error) {
	var policy PublicationPolicy
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT body FROM publication_policy WHERE singleton=1`).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &policy)
		return policy, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return policy, err
	}
	// One-time bootstrap freezes the existing configured review roles. Later
	// config edits cannot silently grant publication roles; administration is CAS.
	policy = PublicationPolicy{Revision: newID(), Roles: s.reviewers}
	raw, err = json.Marshal(policy)
	if err != nil {
		return policy, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO publication_policies(revision,body) VALUES(?,?)`, policy.Revision, raw); err != nil {
		return policy, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO publication_policy(singleton,revision,body) VALUES(1,?,?)`, policy.Revision, raw)
	return policy, err
}
func validRole(role string) bool { return role == "ground" || role == "elevated" || role == "owner" }
func publicationElevated(r Record) bool {
	switch r.Kind {
	case "agent", "agency", "config", "mcp_server", "launch_profile":
		return true
	case "tool":
		return r.Review == nil || r.Review.State != "reviewed" || r.Review.Effective == nil || !groundReview(r, r.Review.Effective)
	}
	return false
}
func normalizedCandidates(in []Candidate) ([]Candidate, error) {
	out := append([]Candidate(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) == 0 || len(out) > 200 {
		return nil, failure("validation_error", "candidates", "1..200 candidates required")
	}
	for i, c := range out {
		if c.ID == "" || c.Revision == "" || (i > 0 && out[i-1].ID == c.ID) {
			return nil, failure("validation_error", "candidates", "unique identities and exact revisions required")
		}
	}
	return out, nil
}
func schemaAdmission(mapping string) (Admission, error) {
	bundle := map[string]json.RawMessage{}
	for _, name := range kinds.Schemas() {
		b, e := kinds.Schema(name)
		if e != nil {
			return Admission{}, e
		}
		bundle[name] = b
	}
	schemaDigest, err := canonicalDigest(struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
		Edges   []kinds.EdgeSpec           `json:"edge_specs"`
	}{bundle, kinds.EdgeSpecs()})
	if err != nil {
		return Admission{}, err
	}
	if mapping == "" {
		mapping = "native/1"
	}
	return Admission{SchemaDigest: schemaDigest, MappingVersion: mapping, EnvelopeVersion: "registry.authored-envelope/2"}, nil
}
func subjectFor(ctx context.Context, q store.Querier, r Record, batch map[string]string) (PublicationSubject, error) {
	if err := validatePublicationRecord(r); err != nil {
		return PublicationSubject{}, err
	}
	// Envelope/2 enumerates every authored field outside the admitted kind payload.
	content, err := canonicalDigest(Input{Examples: r.Examples, Schema: r.Schema, Key: r.Key, Summary: r.Summary, Source: r.Source, Payload: r.Payload, Metadata: r.Metadata})
	if err != nil {
		return PublicationSubject{}, err
	}
	admission, err := schemaAdmission(r.MappingVersion)
	if err != nil {
		return PublicationSubject{}, err
	}
	sub := PublicationSubject{Schema: "registry.publication-subject/1", Action: "publish", Record: SubjectRecord{ID: r.ID, Kind: r.Schema, Revision: r.RevisionID, Authority: r.Authority, ContentDigest: content}, EdgeSetRevision: r.RevisionID, Edges: []SubjectEdge{}, Admission: admission}
	seen := map[string]bool{}
	for _, e := range r.Edges {
		if seen[e.ID] {
			return sub, failure("validation_error", "edges", "duplicate edge identity")
		}
		seen[e.ID] = true
		ref := e.Relation.To
		pin := e.TargetRevision
		if pin == "" {
			pin = r.DependencyPins[e.ID]
		}
		if r.Publication == "published" && r.TargetPins[e.ID] != "" {
			pin = r.TargetPins[e.ID]
		}
		if ref.Authority == "registry" {
			if pin == "" {
				pin = batch[ref.ID]
			}
			if pin != "" {
				target, er := readRecord(ctx, q, ref.ID, -1)
				if er != nil || target.Kind != ref.Kind {
					return sub, failure("reference_unavailable", "edges", "batch target kind mismatch")
				}
			}
			if pin == "" {
				target, er := readPublished(ctx, q, ref.ID, -1)
				if er != nil {
					return sub, failure("reference_unavailable", "edges", "target is not published")
				}
				if target.Lifecycle == "archived" || target.Lifecycle == "deleted" || target.Kind != ref.Kind {
					return sub, failure("reference_unavailable", "edges", "target unavailable")
				}
				pin = target.RevisionID
			}
		} else if pin == "" {
			return sub, failure("reference_unavailable", "edges", "external dependencies need a verified immutable version contract")
		}
		if ref.Authority == "registry" && batch[ref.ID] != pin {
			var found string
			err := q.QueryRowContext(ctx, `SELECT revision FROM publication_events WHERE id=? AND revision=? LIMIT 1`, ref.ID, pin).Scan(&found)
			if err != nil {
				return sub, failure("reference_unavailable", "edges", "explicit target revision has not been published")
			}
		}
		sub.Edges = append(sub.Edges, SubjectEdge{ID: e.ID, Revision: e.SourceRevision, Relation: string(e.Relation.Type), Target: PinnedTarget{ID: ref.ID, Revision: pin}, Attributes: e.Relation})
	}
	sort.Slice(sub.Edges, func(i, j int) bool { return sub.Edges[i].ID < sub.Edges[j].ID })
	return sub, nil
}
func (s *Service) publicationCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	var out any
	if op == "publication_approval_get" || op == "publication_policy_get" {
		err := s.DB.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
			if op == "publication_approval_get" {
				ap, err := loadApproval(ctx, q, a.ApprovalID)
				out = ap
				return err
			}
			var raw []byte
			err := q.QueryRowContext(ctx, `SELECT body FROM publication_policy WHERE singleton=1`).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				out = PublicationPolicy{Roles: s.reviewers}
				return nil
			}
			if err != nil {
				return err
			}
			var policy PublicationPolicy
			if err = json.Unmarshal(raw, &policy); err != nil {
				return err
			}
			out = policy
			return nil
		})
		return out, err
	}

	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		// Committed replay precedes policy/approval/candidate validation. This is
		// historical evidence only, requires current read authorization, and never writes.
		authority := s.authorities[p.ID]
		var requestDigest string
		if op == "publication_publish" {
			if !p.HasScope("registry.read") {
				return failure("forbidden", "scope", "registry.read required for publication and replay")
			}
			if a.IdempotencyKey == "" {
				return failure("validation_error", "idempotency_key", "required")
			}
			var er error
			a.Candidates, er = normalizedCandidates(a.Candidates)
			if er != nil {
				return er
			}
			requestDigest, er = canonicalDigest(struct {
				Action string `json:"action"`
				Args   Args   `json:"args"`
			}{op, a})
			if er != nil {
				return er
			}
			var h string
			var raw []byte
			er = tx.QueryRowContext(ctx, `SELECT digest,body FROM publication_receipts WHERE caller=? AND authority=? AND key=?`, p.ID, authority, a.IdempotencyKey).Scan(&h, &raw)
			if er == nil {
				if h != requestDigest {
					return failure("version_conflict", "idempotency_key", "changed publication request")
				}
				var result PublicationResult
				if er = json.Unmarshal(raw, &result); er != nil {
					return er
				}
				result.Replayed = true
				result.CurrentAvailability = availability(ctx, tx, result.Records)
				out = result
				return nil
			}
			if !errors.Is(er, sql.ErrNoRows) {
				return er
			}
		}
		policy, err := s.policy(ctx, tx)
		if err != nil {
			return err
		}
		if op == "publication_policy_set" {
			if policy.Roles[p.ID] != "owner" {
				return failure("forbidden", "policy", "existing policy owner required")
			}
			if a.Version != policy.Revision {
				return failure("version_conflict", "version", "current policy revision required")
			}
			if len(a.Roles) == 0 {
				return failure("validation_error", "roles", "nonempty roles required")
			}
			for _, role := range a.Roles {
				if !validRole(role) {
					return failure("validation_error", "roles", "ground, elevated or owner required")
				}
			}
			policy = PublicationPolicy{Revision: newID(), Roles: a.Roles}
			raw, er := json.Marshal(policy)
			if er != nil {
				return er
			}
			if _, er = tx.ExecContext(ctx, `INSERT INTO publication_policies(revision,body) VALUES(?,?)`, policy.Revision, raw); er != nil {
				return er
			}
			_, er = tx.ExecContext(ctx, `UPDATE publication_policy SET revision=?,body=? WHERE singleton=1`, policy.Revision, raw)
			out = policy
			return er
		}
		switch op {
		case "publication_approval_prepare":
			if authority == "" {
				return failure("forbidden", "authority", "owning authority required")
			}
			candidates, err := normalizedCandidates(a.Candidates)
			if err != nil {
				return err
			}
			ap := Approval{ID: newID(), Revision: newID(), Issuer: "registry-local", PreparedBy: p.ID, Authority: authority, PolicyRevision: policy.Revision, Decision: "pending", Action: "publish", Candidates: candidates, Subjects: []PublicationSubject{}, SubjectDigests: []string{}, ExpiresAt: a.ExpiresAt}
			if ap.ExpiresAt != "" {
				expiry, er := time.Parse(time.RFC3339Nano, ap.ExpiresAt)
				if er != nil || !expiry.After(time.Now()) {
					return failure("validation_error", "expires_at", "future RFC3339 expiry required")
				}
			}
			batch := map[string]string{}
			for _, c := range candidates {
				batch[c.ID] = c.Revision
			}
			for _, c := range candidates {
				r, er := readRecord(ctx, tx, c.ID, -1)
				if er != nil {
					return er
				}
				if r.Authority != authority {
					return failure("forbidden", "authority", "candidate owned by another authority")
				}
				if r.RevisionID != c.Revision {
					return failure("version_conflict", "revision", "current draft required")
				}
				if r.Lifecycle == "archived" || r.Lifecycle == "deleted" {
					return failure("reference_unavailable", "lifecycle", "candidate unavailable")
				}
				sub, er := subjectFor(ctx, tx, r, batch)
				if er != nil {
					return er
				}
				h, er := canonicalDigest(sub)
				if er != nil {
					return er
				}
				ap.Subjects = append(ap.Subjects, sub)
				ap.SubjectDigests = append(ap.SubjectDigests, h)
				ap.Elevated = ap.Elevated || publicationElevated(r)
			}
			ap.ManifestDigest, err = canonicalDigest(ap.SubjectDigests)
			if err != nil {
				return err
			}
			out = ap
			return saveApproval(ctx, tx, ap)
		case "publication_approval_decide":
			ap, err := loadApproval(ctx, tx, a.ApprovalID)
			if err != nil {
				return err
			}
			if !validRole(policy.Roles[p.ID]) {
				return failure("forbidden", "reviewer", "publication role required")
			}
			if a.ApprovalRevision != ap.Revision {
				return failure("version_conflict", "approval_revision", "current approval revision required")
			}
			if a.Hash != ap.ManifestDigest {
				return failure("version_conflict", "hash", "exact reviewed manifest digest required")
			}
			if a.Decision != "approved" && a.Decision != "denied" && a.Decision != "withdrawn" {
				return failure("validation_error", "decision", "approved, denied or withdrawn required")
			}
			if a.Decision == "approved" && ap.Elevated {
				return failure("forbidden", "human_approval", "verified human decision integration is unavailable; elevated publication remains closed")
			}
			if a.Decision == "approved" && ap.PolicyRevision != policy.Revision {
				return failure("version_conflict", "policy", "prepare against current policy")
			}
			if ap.Decision != "pending" && a.Decision != "withdrawn" {
				return failure("version_conflict", "decision", "prepare new approval after a decision")
			}
			ap.Revision = newID()
			ap.Principal = p.ID
			ap.Decision = a.Decision
			ap.DecidedAt = time.Now().UTC().Format(time.RFC3339Nano)
			out = ap
			return saveApproval(ctx, tx, ap)
		case "publication_publish":
			ap, err := loadApproval(ctx, tx, a.ApprovalID)
			if err != nil {
				return err
			}
			if authority == "" || authority != ap.Authority {
				return failure("forbidden", "authority", "approval owning authority required")
			}
			if ap.Revision != a.ApprovalRevision || ap.ManifestDigest != a.Hash {
				return failure("version_conflict", "approval_revision", "exact approval revision and manifest required")
			}
			if ap.Elevated {
				return failure("forbidden", "human_approval", "elevated publication unavailable")
			}
			if ap.Decision != "approved" || ap.Action != "publish" || ap.PolicyRevision != policy.Revision || !validRole(policy.Roles[ap.Principal]) {
				return failure("forbidden", "approval", "current approved decision and role policy required")
			}
			if ap.ExpiresAt != "" {
				expiry, er := time.Parse(time.RFC3339Nano, ap.ExpiresAt)
				if er != nil || !time.Now().Before(expiry) {
					return failure("forbidden", "approval", "approval expired")
				}
			}
			same, _ := canonicalDigest(a.Candidates)
			want, _ := canonicalDigest(ap.Candidates)
			if same != want {
				return failure("version_conflict", "candidates", "exact approved candidate set required")
			}
			result := PublicationResult{ID: newID(), ApprovalID: ap.ID, ApprovalRevision: ap.Revision, PublishedBy: p.ID, PublishedAt: time.Now().UTC().Format(time.RFC3339Nano), Records: []Record{}}
			batch := map[string]string{}
			for _, c := range ap.Candidates {
				batch[c.ID] = c.Revision
			}
			for i, c := range ap.Candidates {
				r, er := readRecord(ctx, tx, c.ID, -1)
				if er != nil {
					return er
				}
				if r.RevisionID != c.Revision {
					return failure("version_conflict", "revision", "draft changed")
				}
				if r.Lifecycle == "archived" || r.Lifecycle == "deleted" {
					return failure("reference_unavailable", "lifecycle", "candidate unavailable")
				}
				current, er := readPublished(ctx, tx, c.ID, -1)
				var ce *Error
				if er != nil && (!errors.As(er, &ce) || ce.Code != "not_found") {
					return er
				}
				if current.RevisionID != c.ExpectedPublishedRevision {
					return failure("version_conflict", "expected_published_revision", "published pointer changed")
				}
				if publicationElevated(r) {
					return failure("forbidden", "human_approval", "elevated publication unavailable")
				}
				// Preserve approved target pins even if a target has since published a newer
				// revision. Eligibility checks its current lifecycle, never silently repins.
				sub := ap.Subjects[i]
				for _, e := range sub.Edges {
					if e.Attributes.To.Authority != "registry" {
						return failure("reference_unavailable", "edges", "external executable dependency verification unavailable")
					}
					if batch[e.Target.ID] == e.Target.Revision {
						continue
					}
					target, er := readPublished(ctx, tx, e.Target.ID, -1)
					if er != nil || target.Lifecycle == "archived" || target.Lifecycle == "deleted" {
						return failure("reference_unavailable", "edges", "pinned target unavailable")
					}
				}
				fresh, er := subjectForPinned(ctx, tx, r, sub)
				if er != nil {
					return er
				}
				h, er := canonicalDigest(fresh)
				if er != nil {
					return er
				}
				if h != ap.SubjectDigests[i] {
					return failure("version_conflict", "subject", "publication subject changed")
				}
				r.Publication = "published"
				r.PublishedBy = p.ID
				r.PublishedAt = result.PublishedAt
				r.TargetPins = map[string]string{}
				for _, e := range sub.Edges {
					r.TargetPins[e.ID] = e.Target.Revision
				}
				result.Records = append(result.Records, r)
			}
			if err := checkPublicationCycles(ctx, tx, result.Records); err != nil {
				return err
			}
			if err := checkPublishedAliases(ctx, tx, result.Records); err != nil {
				return err
			}
			for _, r := range result.Records {
				if err := writePublished(ctx, tx, r); err != nil {
					return err
				}
			}
			result.CurrentAvailability = availability(ctx, tx, result.Records)
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO publication_receipts(caller,authority,key,digest,body) VALUES(?,?,?,?,?)`, p.ID, authority, a.IdempotencyKey, requestDigest, raw)
			out = result
			return err
		}
		return failure("not_found", "operation", "unknown publication operation")
	})
	return out, err
}
func subjectForPinned(ctx context.Context, q store.Querier, r Record, approved PublicationSubject) (PublicationSubject, error) {
	pins := map[string]string{}
	for _, e := range approved.Edges {
		pins[e.Target.ID] = e.Target.Revision
	}
	return subjectFor(ctx, q, r, pins)
}
func availability(ctx context.Context, q store.Querier, records []Record) map[string]string {
	out := map[string]string{}
	for _, r := range records {
		current, err := readPublished(ctx, q, r.ID, -1)
		switch {
		case err != nil:
			out[r.ID] = "unavailable"
		case current.Lifecycle == "archived" || current.Lifecycle == "deleted":
			out[r.ID] = current.Lifecycle
		case current.RevisionID != r.RevisionID:
			out[r.ID] = "superseded"
		default:
			out[r.ID] = "current"
		}
	}
	return out
}

// Draft aliases cannot take an alias still owned by another published identity.
// Validate the resulting batch, allowing atomic transfers between its members.
func checkPublishedAliases(ctx context.Context, q store.Querier, records []Record) error {
	batch := map[string]bool{}
	for _, r := range records {
		batch[r.ID] = true
	}
	seen := map[string]string{}
	check := func(r Record) error {
		if r.Kind != "tool" {
			return nil
		}
		var t kinds.Tool
		if err := json.Unmarshal(r.Payload, &t); err != nil {
			return err
		}
		for _, a := range t.Aliases {
			key := a.System + "\x00" + a.Alias
			if owner, ok := seen[key]; ok && owner != r.ID {
				return failure("alias_conflict", "aliases", "published alias belongs to another identity")
			}
			seen[key] = r.ID
		}
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT e.body FROM published p JOIN publication_events e ON e.seq=p.seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		var r Record
		if err = json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if !batch[r.ID] {
			if err = check(r); err != nil {
				return err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, r := range records {
		if err = check(r); err != nil {
			return err
		}
	}
	return nil
}

func overlayVerification(ctx context.Context, q store.Querier, r *Record) error {
	var by, at string
	err := q.QueryRowContext(ctx, `SELECT verified_by,verified_at FROM source_verifications WHERE id=? AND revision=? ORDER BY seq DESC LIMIT 1`, r.ID, r.RevisionID).Scan(&by, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	r.VerifiedBy = by
	r.LastVerified = at
	r.VerificationScope = "source_attestation"
	return nil
}

// Re-admit retained drafts against the current contract. Migration never grants
// eligibility to old records that bypassed current secret/schema/dependency rules.
func validatePublicationRecord(r Record) error {
	raw, err := json.Marshal(Input{Examples: r.Examples, Schema: r.Schema, Key: r.Key, Summary: r.Summary, Source: r.Source, Payload: r.Payload, Metadata: r.Metadata})
	if err != nil {
		return err
	}
	if err = kinds.ValidateNoSecretsJSON(raw); err != nil {
		return failure("validation_error", "record", "secret-like or invalid value refused")
	}
	if err = kinds.ValidateJSON(r.Schema, r.Payload); err != nil {
		return invalid("record.payload", err)
	}
	if err = validateExamples(r.Kind, r.Examples); err != nil {
		return err
	}
	expected, err := payloadEdges(r)
	if err != nil {
		return err
	}
	actual := map[string]Edge{}
	for _, e := range r.Edges {
		if err = e.Relation.Validate(); err != nil {
			return invalid("edges", err)
		}
		if isPayloadEdge(e) {
			actual[e.ID] = e
		}
	}
	if len(actual) != len(expected) {
		return failure("validation_error", "edges", "typed dependency snapshot requires a new draft edit")
	}
	for _, e := range expected {
		have, ok := actual[e.ID]
		if have.Relation.Status == "reviewed" && have.ReviewedBy != "" {
			have.Relation.Status = "proposed"
			have.Relation.Source = "authored"
			if have.ReviewHash != digest(have.Relation) {
				return failure("validation_error", "edges", "relationship review binding differs from typed dependency")
			}
		}
		if !ok || digest(have.Relation) != digest(e.Relation) || have.TargetRevision != e.TargetRevision {
			return failure("validation_error", "edges", "typed dependency snapshot requires a new draft edit")
		}
	}
	return nil
}

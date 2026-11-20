package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
	kinds "github.com/hollis-labs/tools/registry/contracts"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"time"
)

type LaunchFacts struct {
	URN               string `json:"urn,omitempty"`
	Session           string `json:"session,omitempty"`
	RunID             string `json:"run_id,omitempty"`
	TaskID            string `json:"task_id,omitempty"`
	WorkstreamID      string `json:"workstream_id,omitempty"`
	TokenJTI          string `json:"token_jti,omitempty"`
	ManifestDigest    string `json:"manifest_digest,omitempty"`
	ExportReceiptID   string `json:"export_receipt_id,omitempty"`
	ActivitySHA256    string `json:"activity_sha256,omitempty"`
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	Traceparent       string `json:"traceparent,omitempty"`
}

var launchFields = []string{"urn", "session", "run_id", "task_id", "workstream_id", "token_jti", "manifest_digest", "export_receipt_id", "activity_sha256", "provider_session_id", "traceparent"}

type ensureRequest struct {
	Key         string   `json:"key"`
	Name        string   `json:"name,omitempty"`
	ProjectURNs []string `json:"project_urns,omitempty"`
}
type MintResult struct {
	ID           string `json:"id"`
	Created      bool   `json:"created"`
	VersionToken string `json:"version_token"`
}

func decodeMint(raw []byte, op string, a *Args) error {
	if op == "workstream_ensure" {
		var r ensureRequest
		if e := Decode(raw, &r); e != nil {
			return e
		}
		a.Key, a.Name, a.ProjectURNs = r.Key, r.Name, r.ProjectURNs
	} else {
		var r LaunchFacts
		if e := Decode(raw, &r); e != nil {
			return e
		}
		a.LaunchFacts = &r
	}
	return nil
}

// ULIDs encode milliseconds and 80 random bits in Crockford base32.
func mintULID(now time.Time) (string, error) {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, e := rand.Read(b[6:]); e != nil {
		return "", e
	}
	n := new(big.Int).SetBytes(b[:])
	mask := big.NewInt(31)
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		out[i] = alphabet[new(big.Int).And(n, mask).Int64()]
		n.Rsh(n, 5)
	}
	return string(out), nil
}
func factsMap(f *LaunchFacts) map[string]any {
	raw, _ := json.Marshal(f)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
func validateMintArgs(a Args) error {
	allowed := Args{Key: a.Key, Name: a.Name, ProjectURNs: a.ProjectURNs, LaunchFacts: a.LaunchFacts, IdempotencyKey: a.IdempotencyKey}
	if !reflect.DeepEqual(a, allowed) {
		return failure("validation_error", "arguments", "only typed mint fields accepted")
	}
	return nil
}

func (s *Service) mint(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	authority := s.authorities[p.ID]
	if authority == "" || !p.HasScope("registry.write") {
		return nil, failure("forbidden", "authority", "bound service principal and registry.write required")
	}
	if strings.TrimSpace(a.IdempotencyKey) == "" {
		return nil, failure("validation_error", "idempotency_key", "retry key required")
	}
	if err := validateMintArgs(a); err != nil {
		return nil, err
	}
	domain, key, kind := "", "", ""
	var request any
	if op == "workstream_ensure" {
		if a.LaunchFacts != nil || strings.TrimSpace(a.Key) == "" {
			return nil, failure("validation_error", "key", "nonempty stable key required")
		}
		projects := append([]string{}, a.ProjectURNs...)
		sort.Strings(projects)
		for i, v := range projects {
			if strings.TrimSpace(v) == "" || (i > 0 && projects[i-1] == v) {
				return nil, failure("validation_error", "project_urns", "nonempty distinct projects required")
			}
		}
		a.ProjectURNs = projects
		domain = digest(projects)
		key = a.Key
		kind = "workstream"
		request = ensureRequest{a.Key, a.Name, projects}
	} else {
		if a.Key != "" || a.Name != "" || len(a.ProjectURNs) > 0 || a.LaunchFacts == nil {
			return nil, failure("validation_error", "arguments", "typed launch facts required")
		}
		f := a.LaunchFacts
		if strings.TrimSpace(f.URN) == "" || strings.TrimSpace(f.Session) == "" || strings.TrimSpace(f.RunID) == "" {
			return nil, failure("validation_error", "launch", "urn, session and run_id required")
		}
		domain = f.Session
		key = f.RunID
		kind = "correlation"
		request = f
	}
	hash := digest(request)
	var result MintResult
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		now := s.now().UTC()
		if _, e := tx.ExecContext(ctx, `DELETE FROM mint_receipts WHERE julianday(expires_at)<=julianday(?)`, now.Format(time.RFC3339Nano)); e != nil {
			return e
		}
		var id string
		var closed int
		e := tx.QueryRowContext(ctx, `SELECT id,closed FROM minted_identities WHERE operation=? AND authority=? AND domain=? AND logical_key=?`, op, authority, domain, key).Scan(&id, &closed)
		exists := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if exists && kind == "workstream" && closed != 0 {
			return failure("workstream_closed", "key", "closed key cannot be reminted")
		}
		var oldHash string
		var receipt []byte
		e = tx.QueryRowContext(ctx, `SELECT digest,body FROM mint_receipts WHERE operation=? AND caller=? AND authority=? AND key=?`, op, p.ID, authority, a.IdempotencyKey).Scan(&oldHash, &receipt)
		if e == nil {
			if oldHash != hash {
				return failure("version_conflict", "idempotency_key", "key reused with changed request")
			}
			return json.Unmarshal(receipt, &result)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var r Record
		if exists {
			r, e = readRecord(ctx, tx, id, -1)
			if e != nil {
				return e
			}
			if kind == "workstream" {
				var w kinds.Workstream
				if e = json.Unmarshal(r.Payload, &w); e != nil {
					return e
				}
				if r.Lifecycle != "active" || w.Status == "closed" {
					return failure("workstream_closed", "key", "closed key cannot be reminted")
				}
				if a.Name != "" && a.Name != w.Name {
					return failure("version_conflict", "name", "ensure cannot overwrite authored attributes")
				}
			} else {
				var c kinds.Correlation
				if e = json.Unmarshal(r.Payload, &c); e != nil {
					return e
				}
				for k, v := range factsMap(a.LaunchFacts) {
					if c.Metadata[k] != v {
						return failure("version_conflict", k, "launch facts differ; use conditional update for enrichment")
					}
				}
				if r.Lifecycle != "active" {
					return failure("version_conflict", "launch", "inactive launch cannot be registered again")
				}
			}
		} else {
			suffix, e := mintULID(now)
			if e != nil {
				return e
			}
			var payload any
			if kind == "workstream" {
				name := a.Name
				if name == "" {
					name = a.Key
				}
				payload = kinds.Workstream{ContentIdentity: kinds.ContentIdentity{Schema: kinds.WorkstreamSchema, ID: suffix, Name: name}, Status: "active", ProjectURNs: a.ProjectURNs}
			} else {
				f := a.LaunchFacts
				// The launch service owns the session reference. Other opaque
				// launch facts stay typed metadata rather than guessing foreign
				// authorities or confusing a launch run with a team run.
				links := map[string]kinds.Reference{"session": {Authority: authority, Kind: "session", ID: f.Session}}
				if f.WorkstreamID != "" {
					links["workstream"] = kinds.Reference{Authority: "registry", Kind: "workstream", ID: f.WorkstreamID}
				}
				c := kinds.Correlation{ContentIdentity: kinds.ContentIdentity{Schema: kinds.CorrelationSchema, ID: suffix, Name: f.Session + " / " + f.RunID, Metadata: factsMap(f)}, Links: links}
				if f.Traceparent != "" {
					c.Trace = &kinds.TraceReference{Traceparent: f.Traceparent}
				}
				payload = c
			}
			raw, e := json.Marshal(payload)
			if e != nil {
				return e
			}
			r, e = prepare(&Input{Schema: kind + "/1", Key: "mint:" + digest([]string{authority, domain, key}), Payload: raw}, authority)
			if e != nil {
				return e
			}
			r.CreatedAt = now.Format(time.RFC3339Nano)
			r.VerifiedBy = p.ID
			r.LastVerified = r.CreatedAt
			r.VerificationScope = "source_attestation"
			if e = save(ctx, tx, &r); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO minted_identities(operation,authority,domain,logical_key,id) VALUES(?,?,?,?,?)`, op, authority, domain, key, r.ID); e != nil {
				return e
			}
		}
		result = MintResult{r.ID, !exists, r.VersionToken}
		receipt, e = json.Marshal(result)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO mint_receipts(operation,caller,authority,key,digest,body,expires_at) VALUES(?,?,?,?,?,?,?)`, op, p.ID, authority, a.IdempotencyKey, hash, receipt, now.Add(30*24*time.Hour).Format(time.RFC3339Nano))
		return e
	})
	return result, err
}
func protectMinted(ctx context.Context, tx store.Tx, old, next Record) error {
	var op string
	e := tx.QueryRowContext(ctx, `SELECT operation FROM minted_identities WHERE id=?`, old.ID).Scan(&op)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if next.Schema != old.Schema || next.Kind != old.Kind {
		return failure("version_conflict", "schema", "minted kind immutable")
	}
	if next.Key != old.Key {
		return failure("version_conflict", "record.key", "minted key immutable")
	}
	if op == "workstream_ensure" {
		var before, after kinds.Workstream
		if e = json.Unmarshal(old.Payload, &before); e != nil {
			return e
		}
		if e = json.Unmarshal(next.Payload, &after); e != nil {
			return e
		}
		if !reflect.DeepEqual(before.ProjectURNs, after.ProjectURNs) {
			return failure("version_conflict", "project_urns", "minted project domain immutable")
		}
	}
	if op == "correlation_register" {
		var before, after kinds.Correlation
		if e = json.Unmarshal(old.Payload, &before); e != nil {
			return e
		}
		if e = json.Unmarshal(next.Payload, &after); e != nil {
			return e
		}
		for _, field := range launchFields {
			if v, ok := before.Metadata[field]; ok && after.Metadata[field] != v {
				return failure("version_conflict", "metadata."+field, "registered launch fact immutable")
			}
			if v, ok := after.Metadata[field]; ok {
				if _, ok = v.(string); !ok {
					return failure("validation_error", "metadata."+field, "launch facts must be strings")
				}
			}
		}
		for k, v := range before.Links {
			if after.Links[k] != v {
				return failure("version_conflict", "links", "registered launch links immutable")
			}
		}
	}
	return nil
}

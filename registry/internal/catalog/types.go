// Package catalog owns Registry persistence and service authorization. Transport
// adapters submit the same operations with a verified principal; none owns data.
package catalog

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/hollis-labs/substrate/kinds"
)

//go:embed migrations/*/*.sql
var Migrations embed.FS

const MaxRequestBytes = 2 << 20

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field,omitempty"`
	Remedy    string `json:"remedy"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func failure(code, field, message string) *Error {
	remedy := "Correct the named field and retry."
	switch code {
	case "version_conflict":
		remedy = "Read the current revision and retry with its version token."
	case "forbidden":
		remedy = "Use a credential with the required scope and owning authority binding."
	case "not_found":
		remedy = "Check the exact identifier and authority."
	case "deleted":
		remedy = "Read history; tombstoned identities cannot be reused."
	case "unavailable":
		remedy = "Retry after service readiness recovers."
	}
	return &Error{Code: code, Field: field, Message: message, Remedy: remedy, Retryable: code == "unavailable"}
}

type Source struct {
	URI        string `json:"uri,omitempty"`
	Revision   string `json:"revision,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
}
type Input struct {
	Schema   string          `json:"schema"`
	Key      string          `json:"key"`
	Summary  string          `json:"summary,omitempty"`
	Source   Source          `json:"source,omitempty"`
	Payload  json.RawMessage `json:"payload"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}
type Edge struct {
	SourceRevision string         `json:"source_revision,omitempty"`
	Inverse        string         `json:"inverse,omitempty"`
	ID             string         `json:"id"`
	Relation       kinds.Relation `json:"relation"`
	TargetState    string         `json:"target_state,omitempty"`
	CheckedAt      string         `json:"checked_at,omitempty"`
}
type Review struct {
	Approval       *kinds.Reference      `json:"approval,omitempty"`
	DefinitionHash string                `json:"definition_hash"`
	State          string                `json:"state"`
	QueueState     string                `json:"queue_state"`
	Proposed       *kinds.Classification `json:"proposed,omitempty"`
	Effective      *kinds.Classification `json:"effective,omitempty"`
	ProposedBy     string                `json:"proposed_by,omitempty"`
	ReviewedBy     string                `json:"reviewed_by,omitempty"`
	Reason         string                `json:"reason,omitempty"`
	ReviewedAt     string                `json:"reviewed_at,omitempty"`
}
type Record struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"`
	Schema            string          `json:"schema"`
	Key               string          `json:"key"`
	Name              string          `json:"name"`
	Summary           string          `json:"summary,omitempty"`
	Owner             string          `json:"owner,omitempty"`
	Subtype           string          `json:"subtype,omitempty"`
	Lifecycle         string          `json:"lifecycle"`
	Authority         string          `json:"authority"`
	Source            Source          `json:"source,omitempty"`
	Payload           json.RawMessage `json:"payload,omitempty"`
	Metadata          map[string]any  `json:"metadata,omitempty"`
	RevisionID        string          `json:"revision_id"`
	VersionToken      string          `json:"version_token"`
	Sequence          int64           `json:"sequence"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
	LastVerified      string          `json:"last_verified,omitempty"`
	VerifiedBy        string          `json:"verified_by,omitempty"`
	VerificationScope string          `json:"verification_scope,omitempty"`
	Edges             []Edge          `json:"edges"`
	Review            *Review         `json:"review,omitempty"`
}
type Query struct {
	Kind        string `json:"kind,omitempty"`
	Subtype     string `json:"subtype,omitempty"`
	Lifecycle   string `json:"lifecycle,omitempty"`
	Authority   string `json:"authority,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Key         string `json:"key,omitempty"`
	ReviewState string `json:"review_state,omitempty"`
	Relation    string `json:"relation,omitempty"`
	Search      string `json:"search,omitempty"`
	Full        bool   `json:"full,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
}
type Page struct {
	Records  []Record `json:"records"`
	Cursor   string   `json:"cursor,omitempty"`
	HasMore  bool     `json:"has_more"`
	Boundary int64    `json:"boundary"`
	AsOf     string   `json:"as_of"`
	Schemas  []string `json:"schemas"`
}

// Args is the common operation envelope. Unknown keys are errors, including
// spoofed authority, verification and review identity fields.
type Args struct {
	Direction       string                `json:"direction,omitempty"`
	Approval        *kinds.Reference      `json:"approval,omitempty"`
	ID              string                `json:"id,omitempty"`
	Version         string                `json:"version,omitempty"`
	Record          *Input                `json:"record,omitempty"`
	IdempotencyKey  string                `json:"idempotency_key,omitempty"`
	AllowUnresolved bool                  `json:"allow_unresolved,omitempty"`
	Query           Query                 `json:"query,omitempty"`
	Schema          string                `json:"schema,omitempty"`
	Revision        string                `json:"revision,omitempty"`
	Relation        *kinds.Relation       `json:"relation,omitempty"`
	EdgeID          string                `json:"edge_id,omitempty"`
	Hash            string                `json:"hash,omitempty"`
	Classification  *kinds.Classification `json:"classification,omitempty"`
	Decision        string                `json:"decision,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	AliasSystem     string                `json:"alias_system,omitempty"`
	Alias           string                `json:"alias,omitempty"`
}

func Decode(data []byte, out any) error {
	if len(data) > MaxRequestBytes {
		return failure("validation_error", "request", "request exceeds 2 MiB")
	}
	if checkErr := kinds.ValidateJSONValue(data); checkErr != nil {
		return invalid("request", checkErr)
	}
	if _, ok := out.(*Args); ok {
		var original struct {
			Relation       json.RawMessage `json:"relation"`
			Classification json.RawMessage `json:"classification"`
		}
		if err := json.Unmarshal(data, &original); err == nil {
			for schema, raw := range map[string]json.RawMessage{kinds.RelationSchema: original.Relation, kinds.ClassificationSchema: original.Classification} {
				if len(raw) > 0 && string(raw) != "null" {
					if checkErr := kinds.ValidateJSON(schema, raw); checkErr != nil {
						return invalid("arguments", checkErr)
					}
				}
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	if checkErr := d.Decode(out); checkErr != nil {
		return failure("validation_error", "request", checkErr.Error())
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return failure("validation_error", "request", "one JSON value required")
	}
	return nil
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func scope(op string) string {
	switch op {
	case "record_create", "record_update", "record_archive", "record_delete", "record_verify", "relation_create", "relation_remove", "tool_propose":
		return "registry.write"
	case "review_decide":
		return "registry.review"
	default:
		return "registry.read"
	}
}
func Scope(op string) string { return scope(op) }
func invalid(field string, err error) error {
	return failure("validation_error", field, fmt.Sprint(err))
}

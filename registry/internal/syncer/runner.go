// Package syncer runs temporary, one-way source connectors through Registry's
// prepare/commit boundary. It does not install schedules or publish records.
package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/hollis-labs/substrate/kinds"
)

var ErrInvalid = errors.New("syncer: invalid connector invocation")

// Connector is explicit migration scaffolding. Remove it at consumer cutover.
// MappingVersion identifies the source-to-Registry transformation, not a source
// revision. Sources must include stable native keys, source revision/digest and
// checkpoint bases in their batch using the import endpoint's contract.
type Connector struct {
	ID             string `json:"id"`
	MappingVersion string `json:"mapping_version"`
	Temporary      bool   `json:"temporary"`
}

// Source takes a bounded, coherent snapshot and returns the import batch JSON.
// An unavailable or incomplete source returns an error, never an empty success.
// It must not publish, reverse-push or resolve credential references to values.
type Source interface {
	Snapshot(context.Context) (json.RawMessage, error)
}

// Request carries canonical batch bytes. A transport adapter must submit these
// exact bytes and the key to prepare, preserving its authenticated caller scope.
type Request struct {
	Connector      Connector       `json:"connector"`
	RunID          string          `json:"run_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Batch          json.RawMessage `json:"batch"`
}

// Preparation describes the server-owned staging object. A rejected batch is a
// successful protocol response with Ready=false and its rejection report.
type Preparation struct {
	ID             string          `json:"id"`
	ManifestDigest string          `json:"manifest_digest"`
	Ready          bool            `json:"ready"`
	Report         json.RawMessage `json:"report,omitempty"`
}

// Importer adapts the authenticated import API. It owns no local replay cache:
// the server retains preparation IDs, expiry, checkpoints and committed receipts.
// Commit must reference the exact ID and manifest digest returned by Prepare.
type Importer interface {
	Prepare(context.Context, Request) (Preparation, error)
	Commit(context.Context, Preparation) (json.RawMessage, error)
}

type Result struct {
	ConnectorID    string          `json:"connector_id"`
	Temporary      bool            `json:"temporary"`
	RunID          string          `json:"run_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Preparation    Preparation     `json:"preparation"`
	Report         json.RawMessage `json:"report,omitempty"`
}

// Run is suitable for an on-demand invocation or an externally scheduled job.
// The scheduler supplies a new runID for each intended run and reuses that ID
// when retrying an uncertain response. The key deliberately excludes the batch
// digest: changed input under the same run ID must conflict at the server, not
// silently allocate another preparation. Expiry requires a new explicit run ID.
// After response loss plus upstream change, inspect the saved preparation or
// receipt before starting another run; a new run ID is not uncertain recovery.
// No automatic retries or writes to the source occur here.
func Run(ctx context.Context, connector Connector, runID string, source Source, importer Importer) (Result, error) {
	if ctx == nil || source == nil || importer == nil || !connector.Temporary || !validID(connector.ID) || !validID(connector.MappingVersion) || !validID(runID) {
		return Result{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	identity, _ := json.Marshal([]string{"registry.sync-run/1", connector.ID, runID})
	hash := sha256.Sum256(identity)
	key := "sync-" + hex.EncodeToString(hash[:])
	out := Result{ConnectorID: connector.ID, Temporary: true, RunID: runID, IdempotencyKey: key}
	batch, err := source.Snapshot(ctx)
	if err != nil {
		return out, err
	}
	// Bound allocations before canonicalization; shared admission rejects
	// ambiguous JSON and values outside the canonical numeric domain.
	if len(batch) == 0 || len(batch) > 2<<20 || kinds.ValidateJSONValue(batch) != nil {
		return out, ErrInvalid
	}
	canonical, err := jsoncanonicalizer.Transform(batch)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return out, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	prepared, err := importer.Prepare(ctx, Request{Connector: connector, RunID: runID, IdempotencyKey: key, Batch: canonical})
	if err != nil {
		return out, err
	}
	out.Preparation = prepared
	if !prepared.Ready {
		if len(prepared.Report) == 0 || kinds.ValidateJSONValue(prepared.Report) != nil || strings.TrimSpace(string(prepared.Report))[0] != '{' {
			return out, ErrInvalid
		}
		out.Report = prepared.Report
		return out, nil
	}
	if prepared.ID == "" || prepared.ManifestDigest == "" {
		return out, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Report, err = importer.Commit(ctx, prepared)
	return out, err
}

func validID(s string) bool {
	return len(s) > 0 && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}

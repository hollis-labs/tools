package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Connector only reads a source. Implementations supply stable keys and immutable
// revisions; Registry admission remains authoritative. No scheduler is installed.
// An external scheduler can invoke RunConnector explicitly during migration.
type Connector interface {
	ID() string
	Snapshot(context.Context, string) (ImportRequest, error)
}

func connectorSnapshot(ctx context.Context, c Connector, checkpoint string) (ImportRequest, error) {
	r, err := c.Snapshot(ctx, checkpoint)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ImportRequest{}, err
		}
		return ImportRequest{}, failure("unavailable", "source", "source snapshot unavailable")
	}
	r.Connector = c.ID()
	r.ExpectedCheckpoint = checkpoint
	r.Entries = append([]ImportEntry(nil), r.Entries...)
	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].SourceKey < r.Entries[j].SourceKey })
	return r, nil
}
func decodeResult(v any, dst any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// RunConnector requires an explicit stable run identity. Retry the same run ID
// after response loss; use a new one only for an intended new synchronization.
func RunConnector(ctx context.Context, call CallFunc, c Connector, runID string) (ImportResult, error) {
	return RunConnectorWithPreparationKey(ctx, call, c, runID)
}

// RunConnectorWithPreparationKey recovers frozen staging before reading current
// baselines. A completed run replays its original receipt even after source edits.
func RunConnectorWithPreparationKey(ctx context.Context, call CallFunc, c Connector, key string) (ImportResult, error) {
	var result ImportResult
	if strings.TrimSpace(key) == "" {
		return result, failure("validation_error", "run_id", "explicit stable run identity required")
	}
	var state ImportPreparationState
	v, err := call(ctx, "import_preparation_get", Args{IdempotencyKey: key})
	if err == nil {
		if err = decodeResult(v, &state); err != nil {
			return result, err
		}
		if state.Plan.Request.Connector != c.ID() {
			return result, failure("version_conflict", "run_id", "run belongs to another connector")
		}
	} else {
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != "not_found" {
			return result, err
		}
		v, err = call(ctx, "import_checkpoint", Args{ID: c.ID()})
		if err != nil {
			return result, err
		}
		var cp struct {
			Checkpoint string `json:"checkpoint"`
		}
		if err = decodeResult(v, &cp); err != nil {
			return result, err
		}
		request, readErr := connectorSnapshot(ctx, c, cp.Checkpoint)
		if readErr != nil {
			return result, readErr
		}
		v, err = call(ctx, "import_prepare", Args{Import: &request, IdempotencyKey: key})
		if err != nil {
			return result, err
		}
		if err = decodeResult(v, &state.Plan); err != nil {
			return result, err
		}
	}
	plan := state.Plan
	sourceDigest := plan.SourceDigest
	if !state.Committed {
		confirm, readErr := connectorSnapshot(ctx, c, plan.Request.ExpectedCheckpoint)
		if readErr != nil {
			return result, readErr
		}
		sourceDigest, err = canonicalDigest(confirm)
		if err != nil {
			return result, err
		}
		if sourceDigest != plan.SourceDigest {
			return result, failure("version_conflict", "source_digest", "source changed after prepare")
		}
	}
	v, err = call(ctx, "import_commit", Args{PlanID: plan.ID, Hash: plan.Digest, SourceDigest: sourceDigest, IdempotencyKey: plan.ID})
	if err != nil {
		return result, err
	}
	err = decodeResult(v, &result)
	return result, err
}

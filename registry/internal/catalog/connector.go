package catalog

import (
	"context"
	"encoding/json"
	"sort"
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
		return r, err
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

// RunConnector prepares, rereads the source to detect source-side edits, then
// commits with Registry-side CAS. It cannot lock a remote source: connectors must
// provide an immutable snapshot/checkpoint or hold a source lease through commit.
func RunConnector(ctx context.Context, call CallFunc, c Connector) (ImportResult, error) {
	var result ImportResult
	v, err := call(ctx, "import_checkpoint", Args{ID: c.ID()})
	if err != nil {
		return result, err
	}
	var cp struct {
		Checkpoint string `json:"checkpoint"`
	}
	if err = decodeResult(v, &cp); err != nil {
		return result, err
	}
	request, err := connectorSnapshot(ctx, c, cp.Checkpoint)
	if err != nil {
		return result, err
	}
	v, err = call(ctx, "import_prepare", Args{Import: &request})
	if err != nil {
		return result, err
	}
	var plan ImportPlan
	if err = decodeResult(v, &plan); err != nil {
		return result, err
	}
	confirm, err := connectorSnapshot(ctx, c, cp.Checkpoint)
	if err != nil {
		return result, err
	}
	sourceDigest, err := canonicalDigest(confirm)
	if err != nil {
		return result, err
	}
	if sourceDigest != plan.SourceDigest {
		return result, failure("version_conflict", "source_digest", "source changed after prepare")
	}
	v, err = call(ctx, "import_commit", Args{PlanID: plan.ID, Hash: plan.Digest, SourceDigest: sourceDigest, IdempotencyKey: plan.ID})
	if err != nil {
		return result, err
	}
	err = decodeResult(v, &result)
	return result, err
}

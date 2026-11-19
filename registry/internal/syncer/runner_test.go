package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type sourceFunc func(context.Context) (json.RawMessage, error)

func (f sourceFunc) Snapshot(ctx context.Context) (json.RawMessage, error) { return f(ctx) }

type fakeImporter struct {
	requests              []Request
	commits               []Preparation
	prepared              Preparation
	prepareErr, commitErr error
	afterPrepare          func()
}

func (f *fakeImporter) Prepare(_ context.Context, request Request) (Preparation, error) {
	f.requests = append(f.requests, request)
	if f.afterPrepare != nil {
		f.afterPrepare()
	}
	return f.prepared, f.prepareErr
}
func (f *fakeImporter) Commit(_ context.Context, p Preparation) (json.RawMessage, error) {
	f.commits = append(f.commits, p)
	return json.RawMessage(`{"created":1}`), f.commitErr
}

var connector = Connector{ID: "repo-skills", MappingVersion: "skills/1", Temporary: true}

func snapshot(raw string) sourceFunc {
	return func(context.Context) (json.RawMessage, error) { return json.RawMessage(raw), nil }
}
func readyImporter() *fakeImporter {
	return &fakeImporter{prepared: Preparation{ID: "prepared-1", ManifestDigest: "digest-1", Ready: true}}
}

func TestRunRetryIdentityAndCanonicalBatch(t *testing.T) {
	f := readyImporter()
	first, err := Run(context.Background(), connector, "scheduled-1", snapshot(`{"z":2,"items":[]}`), f)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := Run(context.Background(), connector, "scheduled-1", snapshot(`{ "items": [], "z": 2 }`), f)
	if err != nil {
		t.Fatal(err)
	}
	if first.IdempotencyKey != retry.IdempotencyKey || string(f.requests[0].Batch) != string(f.requests[1].Batch) {
		t.Fatal("same run did not preserve key/canonical request")
	}
	changed, err := Run(context.Background(), connector, "scheduled-1", snapshot(`{"items":[],"z":3}`), f)
	if err != nil {
		t.Fatal(err)
	}
	if changed.IdempotencyKey != first.IdempotencyKey || string(f.requests[2].Batch) == string(f.requests[0].Batch) {
		t.Fatal("changed source must reach server under original key for conflict detection")
	}
	next, err := Run(context.Background(), connector, "scheduled-2", snapshot(`{"items":[],"z":3}`), f)
	if err != nil {
		t.Fatal(err)
	}
	if next.IdempotencyKey == first.IdempotencyKey {
		t.Fatal("new run reused old receipt identity")
	}
	if len(f.commits) != 4 || f.commits[0].ID != "prepared-1" || f.commits[0].ManifestDigest != "digest-1" {
		t.Fatal("commit did not retain exact server preparation identity")
	}
}

func TestRunDoesNotRetryUncertainResponses(t *testing.T) {
	lost := errors.New("response lost")
	f := readyImporter()
	f.commitErr = lost
	out, err := Run(context.Background(), connector, "run", snapshot(`{"items":[]}`), f)
	if !errors.Is(err, lost) || len(f.commits) != 1 || out.Preparation.ID != "prepared-1" {
		t.Fatal("uncertain commit should retain identity without automatic retry")
	}
	f.prepareErr = lost
	_, err = Run(context.Background(), connector, "run", snapshot(`{"items":[]}`), f)
	if !errors.Is(err, lost) || len(f.commits) != 1 || f.requests[0].IdempotencyKey != f.requests[1].IdempotencyKey {
		t.Fatal("lost prepare response changed retry identity or called commit")
	}
}

func TestRejectedPreparationNeverCommits(t *testing.T) {
	f := &fakeImporter{prepared: Preparation{Report: json.RawMessage(`{"rejected":1,"not_committed":2}`)}}
	out, err := Run(context.Background(), connector, "run", snapshot(`{"items":[]}`), f)
	if err != nil || len(f.commits) != 0 || string(out.Report) != string(f.prepared.Report) {
		t.Fatal("rejected preparation must retain report with no commit")
	}
}

func TestInvalidAndCanceledRunHasNoSideEffects(t *testing.T) {
	for _, raw := range []string{"", "[]", "null", `{"a":1,"a":2}`, `{"a":9007199254740993}`} {
		f := readyImporter()
		_, err := Run(context.Background(), connector, "run", snapshot(raw), f)
		if !errors.Is(err, ErrInvalid) || len(f.requests) != 0 {
			t.Fatalf("invalid batch reached prepare: %q", raw)
		}
	}
	f := readyImporter()
	permanent := connector
	permanent.Temporary = false
	_, err := Run(context.Background(), permanent, "run", snapshot(`{}`), f)
	if !errors.Is(err, ErrInvalid) || len(f.requests) != 0 {
		t.Fatal("accepted permanent connector")
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.afterPrepare = cancel
	_, err = Run(ctx, connector, "run", snapshot(`{}`), f)
	if !errors.Is(err, context.Canceled) || len(f.commits) != 0 {
		t.Fatal("canceled between prepare and commit")
	}
}

func TestUnavailableSourceIsNotAnEmptySync(t *testing.T) {
	f := readyImporter()
	unavailable := errors.New("source unavailable")
	_, err := Run(context.Background(), connector, "run", sourceFunc(func(context.Context) (json.RawMessage, error) { return nil, unavailable }), f)
	if !errors.Is(err, unavailable) || len(f.requests) != 0 {
		t.Fatal("source failure reached import")
	}
}

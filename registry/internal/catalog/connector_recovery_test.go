package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectorResponseLossRecoversFrozenRunAfterRestart(t *testing.T) {
	for _, lostOperation := range []string{"import_prepare", "import_commit"} {
		t.Run(lostOperation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db")
			s := openTest(t, path)
			source := &fixtureConnector{request: batch("one", "", "a")}
			lost := errors.New("fixture response lost")
			call := func(ctx context.Context, op string, a Args) (any, error) {
				value, err := s.Call(ctx, op, a)
				if err == nil && op == lostOperation {
					return nil, lost
				}
				return value, err
			}
			if _, err := RunConnector(writer, call, source, "stable-run"); !errors.Is(err, lost) {
				t.Fatal(err)
			}
			stateValue, err := s.Call(writer, "import_preparation_get", Args{IdempotencyKey: "stable-run"})
			if err != nil {
				t.Fatal(err)
			}
			original := stateValue.(ImportPreparationState)
			if rowCount(t, s, "import_plans") != 1 {
				t.Fatal("lost staging identity")
			}
			if err = s.DB.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, path)
			source = &fixtureConnector{request: batch("one", "", "a")}
			if lostOperation == "import_commit" {
				source.request = batch("changed", "one", "different")
			}
			result, err := RunConnector(writer, s.Call, source, "stable-run")
			if err != nil {
				t.Fatal(err)
			}
			if result.PlanID != original.Plan.ID || result.Counts.Created != 1 || rowCount(t, s, "import_plans") != 1 || rowCount(t, s, "import_receipts") != 1 {
				t.Fatal("retry became a new sync", result)
			}
			if lostOperation == "import_commit" && (!result.Replayed || source.reads != 0) {
				t.Fatal("committed retry resampled source", result, source.reads)
			}
			_, err = s.Call(caller("other", "registry.read", "registry.write"), "import_preparation_get", Args{IdempotencyKey: "stable-run"})
			code(t, err, "not_found")
		})
	}
}
func TestConnectorRequiresExplicitRunIdentity(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	c := &fixtureConnector{request: batch("one", "", "a")}
	_, err := RunConnector(writer, s.Call, c, "")
	code(t, err, "validation_error")
	if c.reads != 0 || rowCount(t, s, "import_plans") != 0 {
		t.Fatal("missing identity sampled or staged source")
	}
}

type failingSourceConnector struct{}

func (failingSourceConnector) ID() string { return "fixture" }
func (failingSourceConnector) Snapshot(context.Context, string) (ImportRequest, error) {
	return ImportRequest{}, errors.New("private source content")
}
func TestConnectorSanitizesSourceErrors(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	_, err := RunConnector(writer, s.Call, failingSourceConnector{}, "failed-source")
	code(t, err, "unavailable")
	if strings.Contains(err.Error(), "private source content") || rowCount(t, s, "import_plans") != 0 {
		t.Fatal("source failure leaked or staged")
	}
}

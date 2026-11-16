package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func sharedSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	s, err := c.Compile("testdata/resultenv.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, schema *jsonschema.Schema, raw []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("schema: %v\n%s", err, raw)
	}
}

func TestSharedResultFixturesValidate(t *testing.T) {
	schema := sharedSchema(t)
	b, err := os.ReadFile("testdata/resultenv-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string          `json:"name"`
		Envelope json.RawMessage `json:"envelope"`
	}
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 10 {
		t.Fatalf("only %d shared fixtures", len(fixtures))
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) { validate(t, schema, fixture.Envelope) })
	}
}

func TestRunnerEnvelopeMatchesSharedSchema(t *testing.T) {
	schema := sharedSchema(t)
	zero, seven := 0, 7
	cases := []struct {
		name    string
		outcome Outcome
		stream  string
		bytes   string
		expect  string
	}{
		{"success", Outcome{Kind: OutcomeExit, ExitCode: &zero}, "stdout", "ok", "success"},
		{"nonzero", Outcome{Kind: OutcomeExit, ExitCode: &seven}, "stderr", "failed", "nonzero_exit"},
		{"signal", Outcome{Kind: OutcomeSignal, Signal: "terminated"}, "", "", "signal"},
		{"tool_error", Outcome{Kind: OutcomeToolError, Error: "start failed"}, "", "", "tool_error"},
		{"transport_error", Outcome{Kind: OutcomeTransport, Error: "connection lost"}, "", "", "transport_error"},
		{"timeout", Outcome{Kind: OutcomeTimeout}, "", "", "timeout"},
		{"cancelled", Outcome{Kind: OutcomeCancelled, CancelRequested: true, CancelConfirmed: true}, "", "", "cancelled"},
		{"cancel_requested_exit", Outcome{Kind: OutcomeExit, ExitCode: &seven, CancelRequested: true, CancelConfirmed: false}, "", "", "nonzero_exit"},
		{"unknown", Outcome{Kind: OutcomeUnknown}, "", "", "unknown"},
		{"pty_combined", Outcome{Kind: OutcomeExit, ExitCode: &zero}, "combined", "both streams", "success"},
		{"truncated", Outcome{Kind: OutcomeExit, ExitCode: &zero}, "stdout", "long output", "success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := manager(t)
			id := "case_" + tc.name
			if err := os.Mkdir(m.runPath(id), 0700); err != nil {
				t.Fatal(err)
			}
			refs := map[string]string{}
			if tc.stream != "" {
				refs[tc.stream] = "runner://" + id + "/" + tc.stream
				if err := os.WriteFile(filepath.Join(m.runPath(id), tc.stream), []byte(tc.bytes), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.writeRecord(id, diskRecord{Result: Result{ID: id, Done: true, Outcome: tc.outcome, OutputRef: refs}}); err != nil {
				t.Fatal(err)
			}
			limit := 64
			if tc.name == "truncated" {
				limit = 3
			}
			b, err := m.Envelope(id, limit)
			if err != nil {
				t.Fatal(err)
			}
			validate(t, schema, b)
			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if got["outcome"] != tc.expect {
				t.Fatalf("outcome %v", got["outcome"])
			}
			if tc.outcome.CancelRequested {
				if got["cancel_requested"] != true || got["cancel_confirmed"] != tc.outcome.CancelConfirmed {
					t.Fatalf("cancel state %v", got)
				}
			}
			if tc.stream == "combined" {
				if got["combined_output"] != tc.bytes || got["stdout"] != nil || got["stderr"] != nil {
					t.Fatalf("PTY output %v", got)
				}
			}
			if tc.name == "truncated" && got["truncation"] == nil {
				t.Fatalf("truncation missing: %v", got)
			}
		})
	}
}

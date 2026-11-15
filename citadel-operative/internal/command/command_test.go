package command

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/citadel-operative/internal/fixturehost"
)

const fixture = "../../testdata/operative.md"

func invoke(r *Runner, ctx context.Context, args ...string) (int, string, string) {
	var out, stderr bytes.Buffer
	code := r.Execute(ctx, args, &out, &stderr)
	return code, out.String(), stderr.String()
}

func TestValidateAndExplicitFixtureScenarios(t *testing.T) {
	code, out, stderr := invoke(New(nil), context.Background(), "validate", "--definition", fixture)
	if code != OK || stderr != "" || !strings.Contains(out, `"status":"valid"`) || !strings.Contains(out, `"digest":"sha256:`) {
		t.Fatalf("validate code=%d out=%s err=%s", code, out, stderr)
	}
	for _, test := range []struct {
		mode            string
		code            int
		status          string
		models, effects int64
	}{
		{"allow", OK, "succeeded", 2, 1},
		{"deny", Denied, "failed", 1, 0},
		{"pending", Pending, "pending", 1, 0},
		{"cancel", Cancelled, "cancelled", 1, 0},
	} {
		t.Run(test.mode, func(t *testing.T) {
			code, out, stderr := invoke(New(nil), context.Background(), "run", "--fixture", test.mode, "--definition", fixture, "--run-id", "run-1", "--input", "Read the synthetic value.")
			var result struct {
				Fixture, Status string
				ModelCalls      int64 `json:"model_calls"`
				ToolEffects     int64 `json:"tool_effects"`
				Events          int64 `json:"events"`
				PendingTicket   bool  `json:"pending_ticket"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatalf("JSON result: %s %v", out, err)
			}
			if code != test.code || result.Status != test.status || result.Fixture != test.mode || result.ModelCalls != test.models || result.ToolEffects != test.effects || result.Events == 0 {
				t.Fatalf("code=%d result=%+v stderr=%s", code, result, stderr)
			}
			if test.mode == "pending" && !result.PendingTicket {
				t.Fatal("pending approval ticket lost")
			}
		})
	}
}

func TestNoDefaultFixtureOrArbitraryEndpoint(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--definition", fixture, "--run-id", "r1", "--input", "hello"},
		{"run", "--fixture", "unknown", "--definition", fixture, "--run-id", "r1", "--input", "hello"},
		{"run", "--fixture", "allow", "--endpoint", "https://example.invalid", "--definition", fixture, "--run-id", "r1", "--input", "hello"},
		{"run", "--fixture", "allow", "--definition", fixture, "--input", "hello"},
		{"run", "--fixture", "allow", "--definition", fixture, "--run-id", "r1"},
	} {
		code, out, stderr := invoke(New(nil), context.Background(), args...)
		if code != Usage || out != "" || stderr == "" {
			t.Fatalf("args=%q code=%d out=%s stderr=%s", args, code, out, stderr)
		}
	}
}

func TestMalformedUnsupportedAndUnboundDefinitionsHaveNoEffects(t *testing.T) {
	original, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, data string }{
		{"malformed", "---\nschema_version: \"2\"\nunknown_authority: true\n---\ninvalid"},
		{"unsupported-hook", strings.Replace(string(original), "behavior:\n", "behavior:\n  hooks: [shell]\n", 1)},
		{"unsupported-capability", strings.Replace(string(original), "requires: [approval]", "requires: [network.provision]", 1)},
		{"unbound-permission", strings.Replace(string(original), "profile: fixture", "profile: production", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "definition.md")
			if err := os.WriteFile(file, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			code, out, stderr := invoke(New(nil), context.Background(), "run", "--fixture", "allow", "--definition", file, "--run-id", "r1", "--input", "hello")
			if code != Failure || stderr == "" {
				t.Fatalf("code=%d out=%s stderr=%s", code, out, stderr)
			}
			if out != "" {
				var result struct {
					Models  int64 `json:"model_calls"`
					Effects int64 `json:"tool_effects"`
				}
				if err := json.Unmarshal([]byte(out), &result); err != nil || result.Models != 0 || result.Effects != 0 {
					t.Fatalf("invalid definition caused activity: %s", out)
				}
			}
		})
	}
}

func TestSharedRunIDRefusesAcrossCommandInstances(t *testing.T) {
	ledger := fixturehost.NewLedger()
	args := []string{"run", "--fixture", "allow", "--definition", fixture, "--run-id", "same-input", "--input", "Read the fixture."}
	if code, _, stderr := invoke(New(ledger), context.Background(), args...); code != OK {
		t.Fatalf("first run: %d %s", code, stderr)
	}
	code, out, stderr := invoke(New(ledger), context.Background(), args...)
	if code != Failure || !strings.Contains(stderr, "already reserved") || !strings.Contains(out, `"model_calls":0`) || !strings.Contains(out, `"tool_effects":0`) {
		t.Fatalf("replay code=%d out=%s stderr=%s", code, out, stderr)
	}
}

func TestCallerCancellationAndWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, out, stderr := invoke(New(nil), ctx, "run", "--fixture", "allow", "--definition", fixture, "--run-id", "cancel-input", "--input", "hello")
	if code != Cancelled || !strings.Contains(out, `"tool_effects":0`) || !strings.Contains(stderr, "canceled") {
		t.Fatalf("cancel code=%d out=%s err=%s", code, out, stderr)
	}
	if code := New(nil).Execute(context.Background(), []string{"validate", "--definition", fixture}, brokenWriter{}, &bytes.Buffer{}); code != Failure {
		t.Fatalf("write failure: %d", code)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

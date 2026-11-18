package folio_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/folio"
	"github.com/hollis-labs/tools/folio/service"
)

func renderPreset(t *testing.T, preset string, inputs map[string]any) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "app")
	svc := service.New(service.Options{BundledFS: folio.BundledPresets, BundledRoot: "presets", UserDir: t.TempDir(), FolioVersion: folio.Version, Now: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)})
	if _, err := svc.New(service.NewOptions{PresetID: preset, TargetDir: target, Inputs: inputs}); err != nil {
		t.Fatalf("render %s: %v", preset, err)
	}
	return target
}

// The presets whose apps have an agent render Nanite's directory layout; the others render none.
func TestAgentDir_RenderedOnlyForAgentPresets(t *testing.T) {
	with := map[string]map[string]any{
		"app-ade":      adeInputs("ade-agents"),
		"surface-desk": {"repo_name": "desk-agents"},
	}
	for preset, inputs := range with {
		t.Run(preset, func(t *testing.T) {
			dir := renderPreset(t, preset, inputs)
			name := inputs["repo_name"].(string)
			agent := readFile(t, filepath.Join(dir, "agents/assistant/agent.md"))
			for _, want := range []string{`schema_version: "2"`, "name: assistant", "mode: durable", "definition_id: def:" + name + "-assistant"} {
				if !strings.Contains(agent, want) {
					t.Errorf("agent.md lacks %q", want)
				}
			}
			host := readFile(t, filepath.Join(dir, "agents/assistant/host.yaml"))
			for _, want := range []string{"kind: embedded", "lifetime: application", "application_id: " + name, "runtime:"} {
				if !strings.Contains(host, want) {
					t.Errorf("host.yaml lacks %q", want)
				}
			}
			if !strings.Contains(readFile(t, filepath.Join(dir, "agents/README.md")), "Nothing in the app opens these files yet") {
				t.Error("agents/README.md does not say the files are not live")
			}
			checkWithNanite(t, filepath.Join(dir, "agents/assistant"))
		})
	}
	without := map[string]map[string]any{
		"app-chat":    chatInputs("chat-agents"),
		"surface-app": {"repo_name": "plain-agents"},
	}
	for preset, inputs := range without {
		dir := renderPreset(t, preset, inputs)
		if _, err := os.Stat(filepath.Join(dir, "agents")); !os.IsNotExist(err) {
			t.Errorf("%s rendered an agents directory", preset)
		}
	}
}

// checkWithNanite loads and opens an agent directory with Nanite's own CLI. It is test-only and
// takes the binary from FOLIO_NANITE, so folio never depends on Nanite: build one from the
// rebuild line (go install github.com/hollis-labs/nanite/cmd/nanite@<commit>) and point at it.
func checkWithNanite(t *testing.T, agentDir string) {
	t.Helper()
	bin := os.Getenv("FOLIO_NANITE")
	if bin == "" {
		t.Log("FOLIO_NANITE not set: the Nanite loader check is skipped")
		return
	}
	// A copy: opening the agent writes its state (.nanite/) beside the authored files.
	work := filepath.Join(t.TempDir(), "assistant")
	if err := os.CopyFS(work, os.DirFS(agentDir)); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"validate", work}, {"-C", work, "conversations", "--json"}} {
		out, err := exec.Command(bin, args...).CombinedOutput() //nolint:gosec // the test names the binary
		if err != nil {
			t.Fatalf("nanite %v: %v\n%s", args, err, out)
		}
		t.Logf("nanite %s: %s", args[0], strings.TrimSpace(string(out)))
	}
}

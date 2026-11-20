package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/agents"
	"github.com/hollis-labs/tools/registry/contracts"
	"gopkg.in/yaml.v3"
)

// These synthetic catalog snapshots preserve the source fields without reading
// any live source or invoking an agent. Elevated publication stays closed; the
// published baseline below is fixture setup, not a new approval integration.
func TestSourceFixturesPreserveTeamsProfilesWorkersAndSkillFiles(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	root := filepath.Join(t.TempDir(), "output")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	records := []*Input{
		{Schema: kinds.ConfigSchema, Key: "catalog-policy", Payload: json.RawMessage(`{"schema":"config/1","id":"catalog-policy","name":"Catalog policy fixture","target":{"authority":"external","kind":"service","id":"fixture-tether"},"scope":"service","settings_schema":{"type":"object"},"settings":{"teams":{"policy":{"history":"filtered","authority":{"mode":"enforce"}},"config":{"workers":{"max":2,"temporary":true}}},"identity":{"points":{"teams":"enforce"}},"worker_definitions":[{"id":"fixture-worker","roles":["worker"],"launch_profile":"fixture-profile","skills":["fixture-skill"]}]}}`)},
		{Schema: kinds.LaunchProfileSchema, Key: "fixture-profile", Payload: json.RawMessage(`{"schema":"launch_profile/1","id":"fixture-profile","name":"Fixture profile","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"fixture-profile"},"revision":"source-profile-v1"}}`)},
		{Schema: kinds.AgentSchema, Key: "fixture-worker", Metadata: map[string]any{"agent_files": []SourceFile{{Path: "agent.md", Content: "---\nschema_version: \"2\"\ndefinition_id: def:fixture-worker\nrevision: \"1\"\nname: fixture-worker\n---\nFixture worker charter.\n"}, {Path: "host.yaml", Content: "kind: embedded\nlifetime: application\napplication_id: fixture-app\nruntime:\n  provider: anthropic\n"}}}, Payload: json.RawMessage(`{"schema":"agent/1","id":"fixture-worker","name":"Fixture worker","charter":"Preserve the authored worker fixture","roles":["worker"],"skills":[{"authority":"registry","kind":"skill","id":"skill:fixture-skill"}],"launch_profile":{"reference":{"authority":"tether","kind":"launch_profile","id":"launch_profile:fixture-profile"},"revision":"source-profile-v1"}}`)},
		{Schema: kinds.SkillSchema, Key: "fixture-skill", Payload: json.RawMessage(`{"schema":"skill/1","id":"fixture-skill","name":"Fixture skill","summary":"Preserve auxiliary files","instructions":"Read the bundled fixture resource."}`), Metadata: map[string]any{"skill_files": []SourceFile{{Path: "references/empty.txt", Content: ""}, {Path: "references/guide.md", Content: "# Fixture guide\n"}, {Path: "scripts/example.sh", Content: "printf 'fixture only\\n'\n"}}}},
	}
	request := ImportRequest{Connector: "temporary-catalog-fixture", SourceAuthority: "fixture", MappingVersion: "fixture/1", Checkpoint: "catalog-snapshot-1"}
	for _, in := range records {
		request.Entries = append(request.Entries, ImportEntry{SourceKey: in.Key, SourceRevision: "source-v1", Record: in})
	}
	plan := importPlan(t, s, request)
	if _, err := s.Call(writer, "import_commit", commitArgs(plan, "fixture-import")); err != nil {
		t.Fatal(err)
	}
	// Import creates drafts even when the source represented an in-use catalog.
	empty := exportPlan(t, s, ExportRequest{Target: "fixture"})
	if len(empty.Bundle.Sources) != 0 {
		t.Fatal("draft fixture leaked", empty)
	}
	for _, in := range records {
		prepared, err := prepare(in, "registry")
		if err != nil {
			t.Fatal(err)
		}
		r := callRecord(t, s, writer, "record_get", Args{ID: prepared.ID, Publication: "draft"})
		if string(r.Payload) != string(in.Payload) {
			t.Fatal("authored fixture changed", r.ID)
		}
		if err = s.DB.WriteTx(context.Background(), func(ctx context.Context, tx store.Tx) error {
			r.Publication = "published"
			r.TargetPins = map[string]string{}
			for _, edge := range r.Edges {
				pin := edge.TargetRevision
				if pin == "" {
					pin = r.DependencyPins[edge.ID]
				}
				if pin == "" {
					pin = "fixture-target-v1"
				}
				r.TargetPins[edge.ID] = pin
			}
			r.PublishedBy = "fixture-historical-approval"
			return writePublished(ctx, tx, r)
		}); err != nil {
			t.Fatal(err)
		}
	}
	receipt := exportPlan(t, s, ExportRequest{Target: "fixture"})
	if len(receipt.Bundle.Sources) != 9 {
		t.Fatal("missing source files", receipt.Bundle.Sources)
	}
	v, err := s.Call(writer, "export_apply", exportArgs(receipt))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range receipt.Bundle.Sources {
		content, readErr := os.ReadFile(filepath.Join(root, source.Path)) // #nosec G304 -- isolated fixture output
		if readErr != nil {
			t.Fatal(readErr)
		}
		switch source.ID {
		case "config:catalog-policy":
			var settings map[string]any
			if err = json.Unmarshal(content, &settings); err != nil {
				t.Fatal(err)
			}
			var original kinds.Config
			if err = json.Unmarshal(records[0].Payload, &original); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(settings, original.Settings) {
				t.Fatal("catalog teams/config/workers or identity enforcement lost", settings)
			}
		case "launch_profile:fixture-profile":
			if !strings.HasPrefix(source.Path, "launch_profiles/") {
				t.Fatal(source.Path)
			}
			var profile kinds.LaunchProfile
			if err = yaml.Unmarshal(content, &profile); err != nil {
				t.Fatal(err)
			}
			if profile.Source.Revision != "source-profile-v1" || profile.Source.Reference.Authority != "tether" {
				t.Fatal("launch source changed", profile)
			}
		case "agent:fixture-worker":
			if strings.HasSuffix(source.Path, "agent.md") || strings.HasSuffix(source.Path, "host.yaml") {
				attachments := records[2].Metadata["agent_files"].([]SourceFile)
				found := false
				for _, file := range attachments {
					if strings.HasSuffix(source.Path, "/"+file.Path) && string(content) == file.Content {
						found = true
					}
				}
				if !found {
					t.Fatal("native source bytes changed", source.Path)
				}
				continue
			}
			var agent kinds.Agent
			// Decode exported YAML through generic data: shared kinds has JSON tags,
			// so field-tag semantics are checked after a YAML-to-JSON roundtrip below.
			var data any
			if err = yaml.Unmarshal(content, &data); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(data)
			if err = json.Unmarshal(raw, &agent); err != nil {
				t.Fatal(err)
			}
			if agent.LaunchProfile == nil || len(agent.Skills) != 1 || agent.Charter == "" {
				t.Fatal("worker definition lost", agent)
			}
		case "skill:fixture-skill":
			if strings.HasSuffix(source.Path, "references/empty.txt") && len(content) != 0 {
				t.Fatal("empty source file changed")
			}
			if strings.HasSuffix(source.Path, "references/guide.md") && string(content) != "# Fixture guide\n" {
				t.Fatal("skill resource changed")
			}
			if strings.HasSuffix(source.Path, "scripts/example.sh") && string(content) != "printf 'fixture only\\n'\n" {
				t.Fatal("skill script changed")
			}
		}
	}
	layout, layoutErr := agents.Resolve(map[string]any{"agents": map[string]any{"dir": "agents/"}}, filepath.Join(root, "substrate.yaml"), filepath.Join(t.TempDir(), "state"))
	if layoutErr != nil {
		t.Fatal(layoutErr)
	}
	names, layoutErr := layout.Names()
	if layoutErr != nil || len(names) != 1 {
		t.Fatal("Substrate cannot discover exported agent directory", names, layoutErr)
	}
	authored, layoutErr := layout.AgentDir(names[0])
	if layoutErr != nil {
		t.Fatal(layoutErr)
	}
	for _, name := range []string{"agent.md", "host.yaml", "definition.yaml"} {
		if _, statErr := os.Stat(filepath.Join(authored, name)); statErr != nil {
			t.Fatal(statErr)
		}
	}

	// A second connector run preserves all source fields and emits no revisions.
	request.ExpectedCheckpoint = request.Checkpoint
	rerun := importPlan(t, s, request)
	if rerun.Counts.Unchanged != 4 {
		t.Fatal(rerun)
	}
	if _, err = s.Call(writer, "import_commit", commitArgs(rerun, "fixture-rerun")); err != nil {
		t.Fatal(err)
	}
	refresh := exportPlan(t, s, ExportRequest{Target: "fixture", ExpectedGeneration: v.(ExportOutcome).Handle.Manifest.Generation})
	if refresh.Bundle.Generation != receipt.Bundle.Generation {
		t.Fatal("fixture rerun changed output generation")
	}
}
func TestSkillAuxiliaryPathsRefuseTraversalAndReservedNames(t *testing.T) {
	for _, path := range []string{"../outside", "/outside", "SKILL.md", "skill.md", "nested/../outside"} {
		t.Run(path, func(t *testing.T) {
			r := Record{ID: "skill:fixture", Kind: "skill", Payload: json.RawMessage(`{"schema":"skill/1","id":"fixture","name":"Fixture","instructions":"Body"}`), Metadata: map[string]any{"skill_files": []SourceFile{{Path: path, Content: "inert"}}}}
			_, err := renderExportFiles(r)
			code(t, err, "validation_error")
		})
	}
}

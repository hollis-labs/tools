package main

import (
	"errors"
	"fmt"
	"github.com/hollis-labs/substrate/kinds"
	"github.com/hollis-labs/tools/registry/internal/projectmigration"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const completeDraft = `{"schema":"project/1","id":"example","name":"Example","class":2,"lifecycle":"active","visibility":"public","owner":"user:example","interfaces":[{"type":"cli"}],"metadata":{"type":"service","disposition":"continue"}}`

func TestUnavailableSharedSchemaRefusesCompleteDraft(t *testing.T) {
	if _, err := kinds.Schema("project/2"); err == nil {
		t.Skip("shared project/2 is available")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.json")
	if err := os.WriteFile(source, []byte(completeDraft), 0600); err != nil {
		t.Fatal(err)
	}
	err := migrate(filepath.Join(dir, "output"), []string{source})
	if err == nil || !strings.Contains(err.Error(), "shared project/2 validation failed") {
		t.Fatalf("expected missing-schema refusal: %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "source.json" {
		t.Fatal("failed batch left output")
	}
	raw, readErr := os.ReadFile(source)
	if readErr != nil || string(raw) != completeDraft {
		t.Fatal("source changed")
	}
}

func TestInvalidBatchLeavesSourcesAndNoOutput(t *testing.T) {
	requireProject2(t)
	for _, raw := range []string{
		strings.Replace(completeDraft, `,"disposition":"continue"`, "", 1),
		strings.Replace(completeDraft, `"type":"service"`, `"type":"wrong"`, 1),
		strings.Replace(completeDraft, `"disposition":"continue"`, `"disposition":"wrong"`, 1),
		strings.Replace(completeDraft, `"class":2`, `"class":2,"class":"lib"`, 1),
	} {
		dir := t.TempDir()
		source := filepath.Join(dir, "source.json")
		output := filepath.Join(dir, "output")
		if err := os.WriteFile(source, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := migrate(output, []string{source}); err == nil {
			t.Fatal("invalid batch accepted")
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("failed batch created output")
		}
		unchanged, err := os.ReadFile(source)
		if err != nil || string(unchanged) != raw {
			t.Fatal("source changed")
		}
	}
}

func TestStagingLaterWriteFailureCleansEverything(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.json")
	if err := os.WriteFile(source, []byte(completeDraft), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "output")
	calls := 0
	err := publishDrafts(output, map[string][]byte{"a.json": []byte("first"), "b.json": []byte("second")}, func(path string, data []byte, mode os.FileMode) error {
		calls++
		if calls == 2 {
			return errors.New("injected later-write failure")
		}
		return os.WriteFile(path, data, mode)
	})
	if err == nil || calls != 2 {
		t.Fatalf("want later write failure, got %v (%d calls)", err, calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "source.json" {
		t.Fatalf("output/staging leaked: %v %v", entries, err)
	}
	raw, err := os.ReadFile(source)
	if err != nil || string(raw) != completeDraft {
		t.Fatal("source changed")
	}
}

func TestStagingNeverReplacesDestination(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "created-during-write"}[late], func(t *testing.T) {
			dir := t.TempDir()
			output := filepath.Join(dir, "output")
			if !late {
				if err := os.Mkdir(output, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := publishDrafts(output, map[string][]byte{"a.json": []byte("new")}, func(path string, data []byte, mode os.FileMode) error {
				if err := os.Mkdir(output, 0700); err != nil {
					return err
				}
				return os.WriteFile(path, data, mode)
			})
			if err == nil {
				t.Fatal("replaced destination")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "output" {
				t.Fatalf("staging leaked: %v %v", entries, err)
			}
			entries, err = os.ReadDir(output)
			if err != nil || len(entries) != 0 {
				t.Fatal("existing destination changed")
			}
		})
	}
}

func TestStagingPublishesCompletePrivateDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("atomic publication is supported on Linux")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "output")
	drafts := map[string][]byte{"a.json": []byte("a"), "b.json": []byte("b")}
	if err := publishDrafts(output, drafts, os.WriteFile); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("staging leaked")
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("output directory not private")
	}
	for name, want := range drafts {
		raw, err := os.ReadFile(filepath.Join(output, name))
		if err != nil || string(raw) != string(want) {
			t.Fatal("incomplete published output")
		}
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("draft not private")
		}
	}
}

func requireProject2(t *testing.T) {
	t.Helper()
	if _, err := kinds.Schema("project/2"); errors.Is(err, kinds.ErrUnknownSchema) {
		t.Skip("pending shared project/2 prerequisite; not migration success evidence")
	} else if err != nil {
		t.Fatal(err)
	}
}

func TestCompleteMultiShapeBatchAndFailureIsolation(t *testing.T) {
	requireProject2(t)
	if runtime.GOOS != "linux" {
		t.Skip("atomic publication is supported on Linux")
	}
	dir := t.TempDir()
	paths := []string{}
	inputs := []string{completeDraft, `{"schema":"project/1","key":"example","payload":` + completeDraft + `}`, `{"publication":"draft","id":"project:example","version":"old-token","reason":"upgrade","record":{"schema":"project/1","key":"example","payload":` + completeDraft + `}}`}
	for i, raw := range inputs {
		path := filepath.Join(dir, fmt.Sprintf("source-%d.json", i))
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	output := filepath.Join(dir, "output")
	if err := migrate(output, paths); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil || string(source) != inputs[i] {
			t.Fatal("source mutated")
		}
		next, err := os.ReadFile(filepath.Join(output, filepath.Base(path)))
		if err != nil {
			t.Fatal(err)
		}
		payload, err := projectmigration.Payload(next)
		if err != nil {
			t.Fatal(err)
		}
		if err := kinds.ValidateJSON("project/2", payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(next), "project/1") {
			t.Fatal("old schema retained")
		}
	}
	// A valid first file must pass before a complete invalid second fixture
	// rejects the entire batch, with no output or staging directory published.
	bad := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(bad, []byte(strings.Replace(completeDraft, `"type":"service"`, `"type":"wrong"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	failed := filepath.Join(dir, "failed")
	err := migrate(failed, []string{paths[0], bad})
	if err == nil || !strings.HasPrefix(err.Error(), "input 2:") {
		t.Fatalf("wrong failure isolation: %v", err)
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("failed output created")
	}
	staging, _ := filepath.Glob(filepath.Join(dir, ".project2-staging-*"))
	if len(staging) != 0 {
		t.Fatal("staging leaked")
	}
}

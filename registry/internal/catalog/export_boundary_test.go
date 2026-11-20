package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize/artifact"
)

type exportBoundaryContext struct {
	context.Context
	count  int
	at     int
	change func()
}

func (c *exportBoundaryContext) Err() error {
	c.count++
	if c.count == c.at {
		c.change()
	}
	return nil
}

// The context checkpoint after planning injects an external edit before the
// next filesystem mutation, without scheduling-dependent sleeps.
func TestExportBoundaryDriftAndControlConfinement(t *testing.T) {
	for _, mutation := range []string{"removed-file", "control-symlink", "control-content"} {
		t.Run(mutation, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "output")
			entry := func(path string) artifact.Entry {
				return artifact.Entry{Path: path, Kind: artifact.EntryFile, Mode: 0600, Bytes: []byte(path), Ownership: artifact.Ownership{EntryID: path, GroupID: "fixture"}}
			}
			req := materialize.Request{Operation: materialize.OperationCreate, TargetRoot: root, Generation: "one", Artifacts: artifact.Tree{Entries: []artifact.Entry{entry("keep")}}}
			if mutation == "removed-file" {
				req.Artifacts.Entries = append(req.Artifacts.Entries, entry("remove"))
			}
			first, err := exportEngine().Apply(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			req.Operation = materialize.OperationReconcile
			req.CurrentManifest = &first.Manifest
			req.ExpectedGeneration = first.Manifest.Generation
			req.Reconcile = materialize.ReconcilePolicy{Conflict: materialize.ConflictReport, RemoveOwned: true}
			req.Artifacts.Entries = req.Artifacts.Entries[:1]
			_, baseline, err := exportBaseline(root)
			if err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			changed := false
			checkpoint := 2 // no-op manifest admission follows its plan checkpoint
			if mutation == "removed-file" {
				checkpoint = 3
			} // removal admission checks context too
			ctx := &exportBoundaryContext{Context: context.Background(), at: checkpoint, change: func() {
				changed = true
				switch mutation {
				case "removed-file":
					if e := os.WriteFile(filepath.Join(root, "remove"), []byte("external edit"), 0600); e != nil {
						t.Fatal(e)
					}
				case "control-content":
					m := first.Manifest
					m.Generation = "external"
					if e := materialize.SaveManifest(root, m); e != nil {
						t.Fatal(e)
					}
				case "control-symlink":
					if e := os.Rename(filepath.Join(root, ".materialize"), filepath.Join(root, "saved-control")); e != nil {
						t.Fatal(e)
					}
					if e := os.Symlink(outside, filepath.Join(root, ".materialize")); e != nil {
						t.Fatal(e)
					}
				}
			}}
			_, err = guardedExportApply(ctx, req, baseline)
			if !changed || err == nil {
				t.Fatalf("unguarded mutation=%s checkpoints=%d err=%v", mutation, ctx.count, err)
			}
			if mutation == "removed-file" {
				raw, e := os.ReadFile(filepath.Join(root, "remove")) // #nosec G304 -- isolated fixture output
				if e != nil || string(raw) != "external edit" {
					t.Fatal("removed external edit", e)
				}
			}
			if mutation == "control-content" {
				m, e := materialize.LoadManifest(root)
				if e != nil || m.Generation != "external" {
					t.Fatal("overwrote external manifest", e)
				}
			}
			files, e := os.ReadDir(outside)
			if e != nil || len(files) != 0 {
				t.Fatal("escaped output root", files, e)
			}
		})
	}
}

func TestExportRejectsReplacedDirectoryWithCopiedManifest(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	parent := t.TempDir()
	root := filepath.Join(parent, "out")
	if err := s.ConfigureExportTargets(map[string]string{"fixture": root}); err != nil {
		t.Fatal(err)
	}
	first := exportPlan(t, s, ExportRequest{Target: "fixture"})
	if _, err := s.Call(writer, "export_apply", exportArgs(first)); err != nil {
		t.Fatal(err)
	}
	next := exportPlan(t, s, ExportRequest{Target: "fixture"})
	saved := filepath.Join(parent, "saved")
	if err := os.Rename(root, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(root, os.DirFS(saved)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Call(writer, "export_apply", exportArgs(next))
	code(t, err, "version_conflict")
}

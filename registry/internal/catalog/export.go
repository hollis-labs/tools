package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize/artifact"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/kinds"
	"gopkg.in/yaml.v3"
)

type ExportRequest struct {
	Target             string   `json:"target"`
	IDs                []string `json:"ids,omitempty"`
	ExpectedGeneration string   `json:"expected_generation,omitempty"`
}
type ExportSource struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
	Digest   string `json:"digest"`
}
type ExportBundle struct {
	Generation          string         `json:"generation"`
	PublicationBoundary int64          `json:"publication_boundary"`
	Schema              string         `json:"schema"`
	Target              string         `json:"target"`
	ExpectedGeneration  string         `json:"expected_generation"`
	Sources             []ExportSource `json:"sources"`
	Artifacts           artifact.Tree  `json:"artifacts"`
	Digest              string         `json:"digest"`
}
type ExportReceipt struct {
	ID        string       `json:"receipt_id"`
	Caller    string       `json:"caller"`
	Authority string       `json:"authority"`
	Action    string       `json:"action"`
	Bundle    ExportBundle `json:"bundle"`
}

// ConfigureExportTargets binds logical names to operator-owned isolated output
// directories. Request input can never supply a filesystem destination. Empty
// configuration disables materialization. Configure before serving requests.
func (s *Service) ConfigureExportTargets(targets map[string]string) error {
	bindings := map[string]string{}
	for name, root := range targets {
		if name == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
			return failure("validation_error", "export_targets", "named absolute output directories required")
		}
		bindings[name] = root
	}
	s.exportTargets = bindings
	return nil
}

// exportMu serializes shared materializer planning/apply within this service.
// Deploy a single materializer process per configured output root.
var exportMu sync.Mutex

func renderExport(r Record) (string, []byte, error) {
	// Opaque IDs are encoded, never interpreted as paths. Key renames cannot move
	// or collide with another identity's owned output.
	name := hex.EncodeToString([]byte(r.ID))
	switch r.Kind {
	case "project":
		var value any
		if err := json.Unmarshal(r.Payload, &value); err != nil {
			return "", nil, err
		}
		raw, err := yaml.Marshal(value)
		return "projects/" + name + "/project.yaml", raw, err
	case "agent", "launch_profile":
		var value any
		if err := json.Unmarshal(r.Payload, &value); err != nil {
			return "", nil, err
		}
		raw, err := yaml.Marshal(value)
		dir := "agents/"
		if r.Kind == "launch_profile" {
			dir = "launch_profiles/"
		}
		if r.Kind == "agent" {
			return dir + name + "/definition.yaml", raw, err
		}
		return dir + name + ".yaml", raw, err
	case "skill":
		var v kinds.Skill
		if err := json.Unmarshal(r.Payload, &v); err != nil {
			return "", nil, err
		}
		meta, err := yaml.Marshal(map[string]string{"name": v.Name, "description": v.Summary})
		if err != nil {
			return "", nil, err
		}
		return "skills/" + name + "/SKILL.md", []byte("---\n" + string(meta) + "---\n\n" + v.Instructions + "\n"), nil
	case "document", "knowledge":
		var v struct {
			Body   string `json:"body"`
			Format string `json:"format"`
		}
		if err := json.Unmarshal(r.Payload, &v); err != nil {
			return "", nil, err
		}
		ext := ".txt"
		if v.Format == "markdown" {
			ext = ".md"
		}
		return "docs/" + name + ext, []byte(v.Body), nil
	case "config":
		var v kinds.Config
		if err := json.Unmarshal(r.Payload, &v); err != nil {
			return "", nil, err
		}
		raw, err := json.MarshalIndent(v.Settings, "", "  ")
		return "config/" + name + ".json", append(raw, '\n'), err
	default:
		return "", nil, failure("validation_error", "export.ids", "selected kind has no approved export mapping")
	}
}

// SourceFile preserves inert authored attachment bytes. The materializer never
// opens a source path or grants an executable mode from these metadata entries.
type SourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type renderedExportFile struct {
	Path    string
	Content []byte
}

func renderExportFiles(r Record) ([]renderedExportFile, error) {
	path, raw, err := renderExport(r)
	if err != nil {
		return nil, err
	}
	files := []renderedExportFile{{Path: path, Content: raw}}
	field, reserved := "", ""
	switch r.Kind {
	case "skill":
		field = "skill_files"
		reserved = "SKILL.md"
	case "agent":
		field = "agent_files"
		reserved = "definition.yaml"
	default:
		return files, nil
	}
	if r.Metadata[field] == nil {
		return files, nil
	}
	encoded, err := json.Marshal(r.Metadata[field])
	if err != nil {
		return nil, invalid("metadata."+field, err)
	}
	var attachments []SourceFile
	if err = Decode(encoded, &attachments); err != nil {
		return nil, err
	}
	if len(attachments) > 200 {
		return nil, failure("validation_error", "metadata."+field, "at most 200 source files allowed")
	}
	seen := map[string]bool{strings.ToLower(reserved): true}
	prefix := strings.TrimSuffix(path, reserved)
	for _, file := range attachments {
		if err = artifact.ValidateRelPath(file.Path); err != nil {
			return nil, failure("validation_error", "metadata."+field, "confined relative auxiliary paths required")
		}
		key := strings.ToLower(file.Path)
		if seen[key] {
			return nil, failure("validation_error", "metadata."+field, "duplicate or reserved auxiliary path")
		}
		seen[key] = true
		files = append(files, renderedExportFile{Path: prefix + file.Path, Content: []byte(file.Content)})
	}
	if r.Kind == "agent" && (!seen["agent.md"] || !seen["host.yaml"]) {
		return nil, failure("validation_error", "metadata.agent_files", "native agent bundles require agent.md and host.yaml")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}
func exportable(kind string) bool {
	switch kind {
	case "project", "agent", "skill", "document", "knowledge", "config", "launch_profile":
		return true
	}
	return false
}
func artifactDigest(raw []byte) artifact.Digest {
	h := sha256.Sum256(raw)
	return artifact.Digest{Algorithm: "sha256", Hex: hex.EncodeToString(h[:])}
}
func bundleDigest(b ExportBundle) (string, error) { b.Digest = ""; return canonicalDigest(b) }

func (s *Service) exportCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	exportMu.Lock()
	defer exportMu.Unlock()
	var out any
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		if op == "export_plan" {
			if a.Export == nil || s.exportTargets[a.Export.Target] == "" {
				return failure("forbidden", "export.target", "configured output binding required")
			}
			request := *a.Export
			bundle := ExportBundle{Schema: "registry-export/1", Target: request.Target, ExpectedGeneration: request.ExpectedGeneration, Sources: []ExportSource{}, Artifacts: artifact.Tree{Entries: []artifact.Entry{}}}
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM publication_events`).Scan(&bundle.PublicationBoundary); err != nil {
				return err
			}
			ids := append([]string(nil), request.IDs...)
			sort.Strings(ids)
			if len(ids) == 0 {
				rows, err := tx.QueryContext(ctx, `SELECT e.id FROM published p JOIN publication_events e ON p.seq=e.seq ORDER BY e.id`)
				if err != nil {
					return err
				}
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						_ = rows.Close()
						return err
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				_ = rows.Close()
				if err != nil {
					return err
				}
			}
			for i, id := range ids {
				if i > 0 && id == ids[i-1] {
					return failure("validation_error", "export.ids", "duplicate identity")
				}
				r, err := readPublished(ctx, tx, id, -1)
				if err != nil {
					return err
				}
				if r.Lifecycle == "deleted" || r.Lifecycle == "archived" {
					if len(request.IDs) > 0 {
						return failure("version_conflict", "export.ids", "selected publication unavailable")
					}
					continue
				}
				if !exportable(r.Kind) && len(request.IDs) == 0 {
					continue
				}
				files, err := renderExportFiles(r)
				if err != nil {
					return err
				}
				for _, file := range files {
					raw := file.Content
					if raw == nil {
						raw = []byte{}
					}
					hash := artifactDigest(raw)
					bundle.Sources = append(bundle.Sources, ExportSource{ID: r.ID, Revision: r.RevisionID, Path: file.Path, Digest: "sha256:" + hash.Hex})
					bundle.Artifacts.Entries = append(bundle.Artifacts.Entries, artifact.Entry{Path: file.Path, Kind: artifact.EntryFile, Mode: 0o600, Bytes: raw, Digest: hash, Ownership: artifact.Ownership{EntryID: r.ID + "#" + file.Path, GroupID: "registry-export", Generation: r.RevisionID}, Provenance: artifact.Provenance{Source: "registry", SourcePath: r.ID, Revision: r.RevisionID}})
				}
			}
			// The manifest is itself an owned artifact, enabling empty published sets.
			raw, err := json.MarshalIndent(bundle.Sources, "", "  ")
			if err != nil {
				return err
			}
			bundle.Artifacts.Entries = append(bundle.Artifacts.Entries, artifact.Entry{Path: "registry-sources.json", Kind: artifact.EntryFile, Mode: 0o600, Bytes: append(raw, '\n'), Ownership: artifact.Ownership{EntryID: "registry-export-manifest", GroupID: "registry-export"}, Provenance: artifact.Provenance{Source: "registry"}})
			sort.Slice(bundle.Artifacts.Entries, func(i, j int) bool { return bundle.Artifacts.Entries[i].Path < bundle.Artifacts.Entries[j].Path })
			generationInput := bundle
			generationInput.ExpectedGeneration = ""
			bundle.Generation, err = bundleDigest(generationInput)
			if err != nil {
				return err
			}
			bundle.Digest, err = bundleDigest(bundle)
			if err != nil {
				return err
			}
			receipt := ExportReceipt{ID: "export:" + newID(), Caller: p.ID, Authority: s.authorities[p.ID], Action: "export", Bundle: bundle}
			raw, err = json.Marshal(receipt)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO export_receipts(id,caller,authority,body) VALUES(?,?,?,?)`, receipt.ID, p.ID, receipt.Authority, raw)
			out = receipt
			return err
		}
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT body FROM export_receipts WHERE id=? AND caller=?`, a.ReceiptID, p.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("not_found", "receipt_id", "caller-owned export receipt required")
		}
		if err != nil {
			return err
		}
		var receipt ExportReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		b := receipt.Bundle
		hash, err := bundleDigest(b)
		if err != nil {
			return err
		}
		if receipt.Action != "export" || hash != b.Digest || a.Hash != hash || receipt.Authority != s.authorities[p.ID] {
			return failure("forbidden", "receipt_id", "receipt binding or content mismatch")
		}
		root := s.exportTargets[b.Target]
		if root == "" {
			return failure("forbidden", "target", "output binding unavailable")
		}
		if op == "export_apply" && receipt.Authority == "" {
			return failure("forbidden", "authority", "configured authority required to apply")
		}
		var boundary int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM publication_events`).Scan(&boundary); err != nil {
			return err
		}
		if boundary != b.PublicationBoundary {
			return failure("version_conflict", "receipt_id", "publication set changed after authorization")
		}
		// Verify the server-issued authorization at the materializer boundary, under
		// the publication transaction. Draft-only edits do not invalidate the export.
		for _, source := range b.Sources {
			var r Record
			r, err = readPublished(ctx, tx, source.ID, -1)
			if err != nil {
				return err
			}
			if r.RevisionID != source.Revision || r.Lifecycle == "archived" || r.Lifecycle == "deleted" {
				return failure("version_conflict", "receipt_id", "publication changed after authorization")
			}
			files, renderErr := renderExportFiles(r)
			if renderErr != nil {
				return renderErr
			}
			matched := false
			for _, file := range files {
				if file.Path == source.Path && "sha256:"+artifactDigest(file.Content).Hex == source.Digest {
					matched = true
					break
				}
			}
			if !matched {
				return failure("version_conflict", "receipt_id", "authorized rendered content changed")
			}
		}
		req := materialize.Request{Operation: materialize.OperationCreate, TargetRoot: root, Artifacts: b.Artifacts, Generation: b.Generation, ExistingTarget: materialize.ExistingTargetRefuse, Reconcile: materialize.ReconcilePolicy{Conflict: materialize.ConflictReport, RemoveOwned: true}}
		current, currentErr := materialize.LoadManifest(root)
		if currentErr == nil && current.Generation == b.Generation {
			req.Operation = materialize.OperationReconcile
			req.CurrentManifest = &current
			req.ExpectedGeneration = b.Generation
		} else if b.ExpectedGeneration != "" {
			manifest := current
			if currentErr != nil {
				return failure("version_conflict", "expected_generation", "current materializer manifest unavailable")
			}
			if manifest.Generation != b.ExpectedGeneration {
				return failure("version_conflict", "expected_generation", "output generation changed")
			}
			req.Operation = materialize.OperationReconcile
			req.CurrentManifest = &manifest
			req.ExpectedGeneration = b.ExpectedGeneration
		}
		// Stable clock makes plans and manifests deterministic. The source manifest
		// carries immutable revisions; wall-clock creation is not an identity input.
		engine := materialize.NewEngine(materialize.EngineOptions{Now: func() time.Time { return time.Unix(0, 0).UTC() }})
		if op == "export_diff" {
			plan, planErr := engine.Plan(ctx, req)
			out = plan
			return planErr
		}
		handle, err := engine.Apply(ctx, req)
		out = handle
		return err
	})
	if err != nil {
		var ce *Error
		if !errors.As(err, &ce) {
			if strings.Contains(err.Error(), "materialize:") {
				return nil, failure("version_conflict", "materializer", "output drift or unsafe output refused")
			}
		}
	}
	return out, err
}

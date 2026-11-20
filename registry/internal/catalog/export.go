package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize/artifact"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tools/registry/contracts"
	"gopkg.in/yaml.v3"
)

type ExportRequest struct {
	Target             string   `json:"target"`
	IDs                []string `json:"ids,omitempty"`
	ExpectedGeneration string   `json:"expected_generation,omitempty"`
}
type ExportSource struct {
	EdgeSetRevision string `json:"edge_set_revision"`
	ID              string `json:"id"`
	Revision        string `json:"revision"`
	Path            string `json:"path"`
	Digest          string `json:"digest"`
}
type ExportBundle struct {
	BaselineManifest   string               `json:"baseline_manifest"`
	IDs                []string             `json:"ids"`
	Subjects           []PublicationSubject `json:"subjects"`
	Generation         string               `json:"generation"`
	Schema             string               `json:"schema"`
	Target             string               `json:"target"`
	ExpectedGeneration string               `json:"expected_generation"`
	Sources            []ExportSource       `json:"sources"`
	Artifacts          artifact.Tree        `json:"artifacts"`
	Digest             string               `json:"digest"`
}
type ExportReceipt struct {
	AuthorizedAt      string       `json:"authorized_at"`
	PolicyRevision    string       `json:"policy_revision"`
	Deadline          string       `json:"deadline"`
	DestinationDigest string       `json:"destination_digest"`
	ID                string       `json:"receipt_id"`
	Caller            string       `json:"caller"`
	Authority         string       `json:"authority"`
	Action            string       `json:"action"`
	Bundle            ExportBundle `json:"bundle"`
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
		if r.Kind == "agent" {
			return nil, failure("non_materializable", "metadata.agent_files", "native agent bundle required")
		}
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
	var definition *agentdef.Definition
	var hostContent string
	nativeAgent, nativeHost := false, false
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
		if r.Kind == "agent" && file.Path == "agent.md" {
			definition, err = agentdef.Parse([]byte(file.Content))
			if err != nil {
				return nil, failure("non_materializable", "metadata.agent_files", "invalid native agent definition")
			}
			nativeAgent = true
		}
		if r.Kind == "agent" && file.Path == "host.yaml" {
			nativeHost = true
			hostContent = file.Content
		}
		files = append(files, renderedExportFile{Path: prefix + file.Path, Content: []byte(file.Content)})
	}
	if r.Kind == "agent" && (!nativeAgent || !nativeHost) {
		return nil, failure("validation_error", "metadata.agent_files", "native agent bundles require agent.md and host.yaml")
	}
	if r.Kind == "agent" {
		decoder := yaml.NewDecoder(bytes.NewBufferString(hostContent))
		decoder.KnownFields(true)
		var profile agentdir.Profile
		var extra any
		if decoder.Decode(&profile) != nil || !errors.Is(decoder.Decode(&extra), io.EOF) || profile.Validate(definition) != nil {
			return nil, failure("non_materializable", "metadata.agent_files", "invalid native host profile")
		}
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

type ExportPlan struct {
	Bundle          ExportBundle     `json:"bundle"`
	Materialization materialize.Plan `json:"materialization"`
}
type ExportOutcome struct {
	ReceiptID  string              `json:"receipt_id"`
	State      string              `json:"state"`
	StartedAt  string              `json:"started_at,omitempty"`
	FinishedAt string              `json:"finished_at,omitempty"`
	Handle     *materialize.Handle `json:"handle,omitempty"`
	Failure    *Error              `json:"failure,omitempty"`
}

func (s *Service) planExport(ctx context.Context, q store.Querier, request ExportRequest) (ExportBundle, error) {
	bundle := ExportBundle{Schema: "registry-export/1", Target: request.Target, ExpectedGeneration: request.ExpectedGeneration, IDs: append([]string{}, request.IDs...), Subjects: []PublicationSubject{}, Sources: []ExportSource{}, Artifacts: artifact.Tree{Entries: []artifact.Entry{}}}
	if s.exportTargets[request.Target] == "" {
		return bundle, failure("forbidden", "export.target", "configured isolated output binding required")
	}
	_, baseline, err := exportBaseline(s.exportTargets[request.Target])
	if err != nil {
		return bundle, materializerError(err)
	}
	bundle.BaselineManifest = baseline
	if len(bundle.IDs) > 200 {
		return bundle, failure("validation_error", "export.ids", "at most 200 roots allowed")
	}
	sort.Strings(bundle.IDs)
	ids := append([]string(nil), bundle.IDs...)
	if len(ids) == 0 {
		rows, err := q.QueryContext(ctx, `SELECT e.id FROM published p JOIN publication_events e ON p.seq=e.seq ORDER BY e.id`)
		if err != nil {
			return bundle, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				_ = rows.Close()
				return bundle, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return bundle, err
		}
	}
	pinned := map[string]bool{}
	revisions := map[string]string{}
	var addPins func(Record) error
	addPins = func(r Record) error {
		key := r.ID + "\x00" + r.RevisionID
		if prior := revisions[r.ID]; prior != "" && prior != r.RevisionID {
			return failure("version_conflict", "export.ids", "scope requires conflicting revisions of one identity")
		}
		revisions[r.ID] = r.RevisionID
		if pinned[key] {
			return nil
		}
		if len(pinned) >= 200 {
			return failure("validation_error", "edges", "export dependency closure exceeds bound")
		}
		pinned[key] = true
		if r.Lifecycle == "archived" || r.Lifecycle == "deleted" {
			return failure("reference_unavailable", "edges", "export dependency unavailable")
		}

		subject, err := subjectFor(ctx, q, r, map[string]string{})
		if err != nil {
			return err
		}
		bundle.Subjects = append(bundle.Subjects, subject)
		for _, edge := range subject.Edges {
			var relation Edge
			for _, e := range r.Edges {
				if e.ID == edge.ID {
					relation = e
					break
				}
			}
			if relation.Relation.To.Authority != "registry" {
				continue
			}
			current, err := readPublished(ctx, q, edge.Target.ID, -1)
			if err != nil || current.Lifecycle == "archived" || current.Lifecycle == "deleted" {
				return failure("reference_unavailable", "edges", "pinned dependency is no longer eligible for new authorization")
			}
			var raw []byte
			err = q.QueryRowContext(ctx, `SELECT body FROM publication_events WHERE id=? AND revision=? ORDER BY seq DESC LIMIT 1`, edge.Target.ID, edge.Target.Revision).Scan(&raw)
			if err != nil {
				return failure("reference_unavailable", "edges", "immutable published target unavailable")
			}
			var target Record
			if err = json.Unmarshal(raw, &target); err != nil {
				return err
			}
			if err = addPins(target); err != nil {
				return err
			}
		}
		return nil
	}
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			return bundle, failure("validation_error", "export.ids", "duplicate identity")
		}
		r, err := readPublished(ctx, q, id, -1)
		if err != nil {
			return bundle, err
		}
		if r.Lifecycle == "archived" || r.Lifecycle == "deleted" {
			if len(bundle.IDs) > 0 {
				return bundle, failure("version_conflict", "export.ids", "selected publication unavailable")
			}
			continue
		}
		if !exportable(r.Kind) && len(bundle.IDs) == 0 {
			continue
		}
		if err = addPins(r); err != nil {
			return bundle, err
		}
		files, err := renderExportFiles(r)
		if err != nil {
			return bundle, err
		}
		for _, file := range files {
			raw := file.Content
			if raw == nil {
				raw = []byte{}
			}
			hash := artifactDigest(raw)
			bundle.Sources = append(bundle.Sources, ExportSource{ID: r.ID, Revision: r.RevisionID, EdgeSetRevision: r.RevisionID, Path: file.Path, Digest: "sha256:" + hash.Hex})
			bundle.Artifacts.Entries = append(bundle.Artifacts.Entries, artifact.Entry{Path: file.Path, Kind: artifact.EntryFile, Mode: 0o600, Bytes: raw, Digest: hash, Ownership: artifact.Ownership{EntryID: r.ID + "#" + file.Path, GroupID: "registry-export", Generation: r.RevisionID}, Provenance: artifact.Provenance{Source: "registry", SourcePath: r.ID, Revision: r.RevisionID}})
		}
	}
	sort.Slice(bundle.Subjects, func(i, j int) bool {
		a, b := bundle.Subjects[i].Record, bundle.Subjects[j].Record
		if a.ID == b.ID {
			return a.Revision < b.Revision
		}
		return a.ID < b.ID
	})
	raw, err := json.MarshalIndent(bundle.Sources, "", "  ")
	if err != nil {
		return bundle, err
	}
	bundle.Artifacts.Entries = append(bundle.Artifacts.Entries, artifact.Entry{Path: "registry-sources.json", Kind: artifact.EntryFile, Mode: 0o600, Bytes: append(raw, '\n'), Ownership: artifact.Ownership{EntryID: "registry-export-manifest", GroupID: "registry-export"}, Provenance: artifact.Provenance{Source: "registry"}})
	sort.Slice(bundle.Artifacts.Entries, func(i, j int) bool { return bundle.Artifacts.Entries[i].Path < bundle.Artifacts.Entries[j].Path })
	generationInput := bundle
	generationInput.ExpectedGeneration = ""
	generationInput.BaselineManifest = ""
	bundle.Generation, err = bundleDigest(generationInput)
	if err != nil {
		return bundle, err
	}
	total := 0
	for _, entry := range bundle.Artifacts.Entries {
		total += len(entry.Bytes)
	}
	if total > MaxRequestBytes {
		return bundle, failure("validation_error", "export.ids", "export exceeds 2 MiB")
	}
	bundle.Digest, err = bundleDigest(bundle)
	return bundle, err
}
func (s *Service) verifyCurrentExport(ctx context.Context, q store.Querier, b ExportBundle, hash string) error {
	digest, err := bundleDigest(b)
	if err != nil {
		return err
	}
	if digest != b.Digest || hash != digest {
		return failure("version_conflict", "hash", "manifest bytes disagree with digest")
	}
	current, err := s.planExport(ctx, q, ExportRequest{Target: b.Target, IDs: b.IDs, ExpectedGeneration: b.ExpectedGeneration})
	if err != nil {
		return err
	}
	if current.Digest != digest {
		return failure("version_conflict", "bundle", "selected publications or pinned manifest changed; replan")
	}
	return nil
}
func exportEngine() *materialize.DefaultEngine {
	return materialize.NewEngine(materialize.EngineOptions{Now: func() time.Time { return time.Unix(0, 0).UTC() }})
}
func safeExportPath(root, rel string) error {
	if rel != "" && artifact.ValidateRelPath(rel) != nil {
		return materialize.ErrUnsafeTarget
	}
	full := root
	if rel != "" {
		full = filepath.Join(root, filepath.FromSlash(rel))
	}
	for p := full; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return materialize.ErrUnsafeTarget
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return materialize.ErrUnsafeTarget
			}
			if p != full && !info.IsDir() {
				return materialize.ErrUnsafeTarget
			}
			if p == full && !info.IsDir() && !info.Mode().IsRegular() {
				return materialize.ErrUnsafeTarget
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func exportBaseline(root string) (*materialize.Manifest, string, error) {
	if err := safeExportPath(root, materialize.ManifestRelPath); err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		parent, parentErr := os.Lstat(filepath.Dir(root))
		if parentErr != nil || !parent.IsDir() || materialize.InstalledIdentity(parent) == "" {
			return nil, "", materialize.ErrUnsafeTarget
		}
		return nil, digest(struct{ ParentIdentity string }{materialize.InstalledIdentity(parent)}), nil
	}
	if err != nil || !info.IsDir() {
		return nil, "", materialize.ErrUnsafeTarget
	}
	identity := materialize.InstalledIdentity(info)
	if identity == "" {
		return nil, "", materialize.ErrUnsupportedOperation
	}
	info, err = os.Lstat(materialize.ManifestPath(root))
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxRequestBytes {
		return nil, "", materialize.ErrMissingManifest
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = anchor.Close() }()
	file, err := anchor.Open(filepath.FromSlash(materialize.ManifestRelPath))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, "", materialize.ErrUnsafeTarget
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxRequestBytes+1))
	if err != nil || len(raw) > MaxRequestBytes {
		return nil, "", materialize.ErrMalformedDocument
	}
	var m materialize.Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, "", materialize.ErrMalformedDocument
	}
	seen := map[string]bool{}
	for _, e := range m.Entries {
		if seen[e.Path] || safeExportPath(root, e.Path) != nil {
			return nil, "", materialize.ErrUnsafeTarget
		}
		seen[e.Path] = true
	}
	return &m, digest(struct {
		RootIdentity   string
		ManifestDigest string
	}{identity, artifactDigest(raw).Hex}), nil
}
func materializeRequest(root string, b ExportBundle) (materialize.Request, error) {
	b.Artifacts.Entries = append([]artifact.Entry(nil), b.Artifacts.Entries...)
	empty := artifactDigest([]byte{})
	for i := range b.Artifacts.Entries {
		e := &b.Artifacts.Entries[i]
		if e.Kind == artifact.EntryFile && e.Bytes == nil && e.ContentRef == nil && e.Digest == empty {
			e.Bytes = []byte{}
		}
	}
	req := materialize.Request{Operation: materialize.OperationCreate, TargetRoot: root, Artifacts: b.Artifacts, Generation: b.Generation, ExistingTarget: materialize.ExistingTargetRefuse, Reconcile: materialize.ReconcilePolicy{Conflict: materialize.ConflictReport, RemoveOwned: true}}
	m, base, err := exportBaseline(root)
	if err != nil {
		return req, err
	}
	if base != b.BaselineManifest {
		return req, materialize.ErrStaleGeneration
	}
	for _, e := range b.Artifacts.Entries {
		if err = safeExportPath(root, e.Path); err != nil {
			return req, err
		}
	}
	if m != nil {
		if m.Generation != b.Generation && (b.ExpectedGeneration == "" || m.Generation != b.ExpectedGeneration) {
			return req, materialize.ErrStaleGeneration
		}
		req.Operation = materialize.OperationReconcile
		req.CurrentManifest = m
		req.ExpectedGeneration = m.Generation
	} else if b.ExpectedGeneration != "" {
		return req, materialize.ErrStaleGeneration
	}
	return req, nil
}
func loadExportReceipt(ctx context.Context, q store.Querier, id, caller string) (ExportReceipt, error) {
	var r ExportReceipt
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT body FROM export_receipts WHERE id=? AND caller=?`, id, caller).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return r, failure("not_found", "receipt_id", "caller-owned authorization required")
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(raw, &r)
	return r, err
}
func (s *Service) exportCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	switch op {
	case "export_plan", "export_diff":
		var b ExportBundle
		err := s.DB.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
			if op == "export_plan" {
				if a.Export == nil {
					return failure("validation_error", "export", "export scope required")
				}
				var err error
				b, err = s.planExport(ctx, q, *a.Export)
				return err
			}
			if a.Bundle == nil {
				return failure("validation_error", "bundle", "pinned bundle required")
			}
			b = *a.Bundle
			return s.verifyCurrentExport(ctx, q, b, a.Hash)
		})
		if err != nil {
			return nil, err
		}
		exportMu.Lock()
		defer exportMu.Unlock()
		req, err := materializeRequest(s.exportTargets[b.Target], b)
		if err != nil {
			return nil, err
		}
		plan, err := exportEngine().Plan(ctx, req)
		if err != nil {
			return nil, materializerError(err)
		}
		return ExportPlan{Bundle: b, Materialization: plan}, nil
	case "export_authorize":
		return s.authorizeExport(ctx, p, a)
	case "export_attempt_get":
		var outcome ExportOutcome
		err := s.DB.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
			if _, err := loadExportReceipt(ctx, q, a.ReceiptID, p.ID); err != nil {
				return err
			}
			var raw []byte
			err := q.QueryRowContext(ctx, `SELECT body FROM export_attempts WHERE receipt_id=?`, a.ReceiptID).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				outcome = ExportOutcome{ReceiptID: a.ReceiptID, State: "authorized"}
				return nil
			}
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, &outcome)
		})
		return outcome, err
	default:
		return s.applyExport(ctx, p, a)
	}
}
func (s *Service) authorizeExport(ctx context.Context, p libauth.Principal, a Args) (ExportReceipt, error) {
	exportMu.Lock()
	defer exportMu.Unlock()
	var out ExportReceipt
	authority := s.authorities[p.ID]
	if authority == "" {
		return out, failure("forbidden", "authority", "configured export authority required")
	}
	if a.Bundle == nil || a.IdempotencyKey == "" {
		return out, failure("validation_error", "bundle", "pinned bundle and authorization retry key required")
	}
	requestDigest, digestErr := canonicalDigest(a)
	if digestErr != nil {
		return out, digestErr
	}
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var storedDigest, receiptID string
		lookupErr := tx.QueryRowContext(ctx, `SELECT digest,receipt_id FROM export_authorization_keys WHERE caller=? AND authority=? AND key=?`, p.ID, authority, a.IdempotencyKey).Scan(&storedDigest, &receiptID)
		if lookupErr == nil {
			if storedDigest != requestDigest {
				return failure("version_conflict", "idempotency_key", "authorization key changed request")
			}
			var err error
			out, err = loadExportReceipt(ctx, tx, receiptID, p.ID)
			return err
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return lookupErr
		}
		b := *a.Bundle
		if err := s.verifyCurrentExport(ctx, tx, b, a.Hash); err != nil {
			return err
		}
		policy, err := s.policy(ctx, tx)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		deadline := now.Add(15 * time.Minute)
		if a.ExpiresAt != "" {
			deadline, err = time.Parse(time.RFC3339Nano, a.ExpiresAt)
			if err != nil || !deadline.After(now) || deadline.After(now.Add(24*time.Hour)) {
				return failure("validation_error", "expires_at", "future start deadline within 24 hours required")
			}
		}
		destination, err := canonicalDigest(struct {
			Root string `json:"root"`
		}{s.exportTargets[b.Target]})
		if err != nil {
			return err
		}
		out = ExportReceipt{ID: "export:" + newID(), Caller: p.ID, Authority: authority, Action: "export_apply", Bundle: b, AuthorizedAt: now.Format(time.RFC3339Nano), PolicyRevision: policy.Revision, Deadline: deadline.UTC().Format(time.RFC3339Nano), DestinationDigest: destination}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO export_receipts(id,caller,authority,body) VALUES(?,?,?,?)`, out.ID, p.ID, authority, raw); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO export_authorization_keys(caller,authority,key,digest,receipt_id) VALUES(?,?,?,?,?)`, p.ID, authority, a.IdempotencyKey, requestDigest, out.ID)
		return err
	})
	return out, err
}
func materializerError(err error) *Error {
	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}
	if errors.Is(err, materialize.ErrConflict) || errors.Is(err, materialize.ErrStaleGeneration) || errors.Is(err, materialize.ErrTargetExists) || errors.Is(err, materialize.ErrUnsafeTarget) {
		return failure("version_conflict", "materializer", "owned-output drift/CAS or destination safety check failed")
	}
	return failure("unavailable", "materializer", "materializer attempt failed; inspect recorded outcome and reconcile with fresh authorization")
}
func (s *Service) applyExport(ctx context.Context, p libauth.Principal, a Args) (ExportOutcome, error) {
	var outcome ExportOutcome
	var receipt ExportReceipt
	exportMu.Lock()
	defer exportMu.Unlock()
	// This transaction records START only. It ends before any filesystem access.
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var err error
		receipt, err = loadExportReceipt(ctx, tx, a.ReceiptID, p.ID)
		if err != nil {
			return err
		}
		b := receipt.Bundle
		hash, err := bundleDigest(b)
		if err != nil {
			return err
		}
		destination, err := canonicalDigest(struct {
			Root string `json:"root"`
		}{s.exportTargets[b.Target]})
		if err != nil {
			return err
		}
		if receipt.Action != "export_apply" || receipt.Caller != p.ID || receipt.Authority == "" || receipt.Authority != s.authorities[p.ID] || hash != b.Digest || hash != a.Hash || destination != receipt.DestinationDigest || s.exportTargets[b.Target] == "" {
			return failure("forbidden", "receipt_id", "receipt identity/manifest/destination binding mismatch")
		}
		start := s.now().UTC()
		deadline, err := time.Parse(time.RFC3339Nano, receipt.Deadline)
		if err != nil || !start.Before(deadline) {
			return failure("version_conflict", "deadline", "apply-start authorization expired")
		}
		var state string
		err = tx.QueryRowContext(ctx, `SELECT state FROM export_attempts WHERE receipt_id=?`, receipt.ID).Scan(&state)
		if err == nil {
			return failure("version_conflict", "receipt_id", "authorization already consumed; inspect outcome and obtain fresh authorization for any new attempt")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		outcome = ExportOutcome{ReceiptID: receipt.ID, State: "started", StartedAt: start.Format(time.RFC3339Nano)}
		raw, err := json.Marshal(outcome)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO export_attempts(receipt_id,started_at,state,body) VALUES(?,?,?,?)`, receipt.ID, outcome.StartedAt, outcome.State, raw)
		return err
	})
	if err != nil {
		return outcome, err
	}
	// Do not recheck current publications here. Snapshot authorization survives a
	// subsequent archive. This is not a cross-system revocation fence or a lease.
	req, applyErr := materializeRequest(s.exportTargets[receipt.Bundle.Target], receipt.Bundle)
	var handle materialize.Handle
	if applyErr == nil {
		handle, applyErr = guardedExportApply(ctx, req, receipt.Bundle.BaselineManifest, s.materializerOptions)
	}
	outcome.Handle = &handle
	outcome.FinishedAt = s.now().UTC().Format(time.RFC3339Nano)
	outcome.State = "completed"
	if applyErr != nil {
		outcome.State = "failed"
		outcome.Failure = materializerError(applyErr)
	}
	raw, marshalErr := json.Marshal(outcome)
	if marshalErr != nil {
		return outcome, marshalErr
	}
	// Record even if the caller's request was canceled after filesystem mutation.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, recordErr := s.DB.ExecContext(recordCtx, `UPDATE export_attempts SET state=?,body=? WHERE receipt_id=?`, outcome.State, raw, receipt.ID)
	if recordErr != nil {
		e := failure("unavailable", "outcome", "attempt ended but outcome could not be persisted; receipt remains consumed, inspect output before fresh authorization")
		e.Details = outcome
		return outcome, e
	}
	if applyErr != nil {
		e := *outcome.Failure
		e.Details = outcome
		return outcome, &e
	}
	return outcome, nil
}

// The shared engine owns all file writes. Recheck regular leaves and the control
// manifest after its plan and immediately before each write. These checks detect
// observed external drift; they do not claim an OS-wide lock on external writers.
func guardedExportApply(ctx context.Context, req materialize.Request, baseline string, hooks ...materialize.EngineOptions) (materialize.Handle, error) {
	var hook materialize.EngineOptions
	if len(hooks) > 0 {
		hook = hooks[0]
	}
	before := map[string]string{}
	removals := []string{}
	for _, e := range req.Artifacts.Entries {
		v, err := exportLeaf(req.TargetRoot, e.Path)
		if err != nil {
			return materialize.Handle{}, err
		}
		before[e.Path] = v
	}
	if req.CurrentManifest != nil {
		for _, prior := range req.CurrentManifest.Entries {
			if _, desired := before[prior.Path]; desired || prior.Path == materialize.ManifestRelPath {
				continue
			}
			v, err := exportLeaf(req.TargetRoot, prior.Path)
			if err != nil {
				return materialize.Handle{}, err
			}
			before[prior.Path] = v
			removals = append(removals, prior.Path)
		}
	}
	engine := materialize.NewEngine(materialize.EngineOptions{Now: func() time.Time { return time.Unix(0, 0).UTC() },
		BeforeManifest: func(target string) error {
			_, actual, err := exportBaseline(target)
			if err != nil {
				return err
			}
			if actual != baseline {
				return materialize.ErrStaleGeneration
			}
			return nil
		}, BeforeWrite: func(stage string, e artifact.Entry) error {
			if hook.BeforeWrite != nil {
				if err := hook.BeforeWrite(stage, e); err != nil {
					return err
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			_, actual, err := exportBaseline(req.TargetRoot)
			if err != nil {
				return err
			}
			if actual != baseline {
				return materialize.ErrStaleGeneration
			}
			if e.Path != "" {
				v, err := exportLeaf(req.TargetRoot, e.Path)
				if err != nil {
					return err
				}
				if v != before[e.Path] {
					return materialize.ErrConflict
				}
			} else {
				// Cairn's removal callback has no Entry. Recheck still-present
				// removals; earlier removals from this attempt are now absent.
				for _, path := range removals {
					v, err := exportLeaf(req.TargetRoot, path)
					if err != nil {
						return err
					}
					if v != "absent" && v != before[path] {
						return materialize.ErrConflict
					}
				}
			}
			return nil
		}, BeforeRename: func(stage, target string) error {
			if hook.BeforeRename != nil {
				if err := hook.BeforeRename(stage, target); err != nil {
					return err
				}
			}
			if err := safeExportPath(target, ""); err != nil {
				return err
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				return materialize.ErrTargetExists
			}
			_, actual, err := exportBaseline(target)
			if err != nil {
				return err
			}
			if actual != baseline {
				return materialize.ErrStaleGeneration
			}
			return nil
		}})
	return engine.Apply(ctx, req)
}
func exportLeaf(root, rel string) (string, error) {
	if err := safeExportPath(root, rel); err != nil {
		return "", err
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return fmt.Sprint("directory:", info.Mode().Perm()), nil
	}
	if !info.Mode().IsRegular() || info.Size() > MaxRequestBytes {
		return "", materialize.ErrUnsafeTarget
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = anchor.Close() }()
	f, err := anchor.Open(filepath.FromSlash(rel))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", materialize.ErrUnsafeTarget
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxRequestBytes+1))
	if err != nil || len(raw) > MaxRequestBytes {
		return "", materialize.ErrUnsafeTarget
	}
	return fmt.Sprint(info.Mode().Perm(), ":", artifactDigest(raw).Hex), nil
}

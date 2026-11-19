package catalog

import (
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

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize"
	"github.com/hollis-labs/libs/harness/core/workspace/materialize/artifact"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
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
type ExportPin struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
type ExportBundle struct {
	Pins                []ExportPin    `json:"pins"`
	DestinationScope    string         `json:"destination_scope"`
	BaselineManifest    string         `json:"baseline_manifest"`
	Generation          string         `json:"generation"`
	PublicationBoundary int64          `json:"publication_boundary"`
	Schema              string         `json:"schema"`
	Target              string         `json:"target"`
	ExpectedGeneration  string         `json:"expected_generation"`
	Sources             []ExportSource `json:"sources"`
	Artifacts           artifact.Tree  `json:"artifacts"`
	Digest              string         `json:"digest"`
}
type ExportPlan struct {
	Bundle     ExportBundle       `json:"bundle"`
	Report     materialize.Report `json:"report"`
	WillMutate bool               `json:"will_mutate"`
}
type ExportAttempt struct {
	ReceiptID  string              `json:"receipt_id"`
	State      string              `json:"state"`
	StartedAt  string              `json:"started_at,omitempty"`
	FinishedAt string              `json:"finished_at,omitempty"`
	Handle     *materialize.Handle `json:"handle,omitempty"`
	Failure    string              `json:"failure,omitempty"`
}
type ExportReceipt struct {
	Destination    string       `json:"destination"`
	PolicyRevision string       `json:"policy_revision"`
	AuthorizedAt   string       `json:"authorized_at"`
	StartDeadline  string       `json:"start_deadline"`
	ID             string       `json:"receipt_id"`
	Caller         string       `json:"caller"`
	Authority      string       `json:"authority"`
	Action         string       `json:"action"`
	Bundle         ExportBundle `json:"bundle"`
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
			if _, err := agentdef.Parse([]byte(file.Content)); err != nil {
				return nil, failure("non_materializable", "metadata.agent_files", "invalid native agent definition")
			}
			nativeAgent = true
		}
		if r.Kind == "agent" && file.Path == "host.yaml" {
			nativeHost = true
		}
		files = append(files, renderedExportFile{Path: prefix + file.Path, Content: []byte(file.Content)})
	}
	if r.Kind == "agent" && (!nativeAgent || !nativeHost) {
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

// buildExport runs entirely in a Registry snapshot. Dependencies are followed at
// approved immutable pins, while current eligibility is checked independently.
func buildExport(ctx context.Context, q store.Querier, request ExportRequest, root, baseline string) (ExportBundle, error) {
	b := ExportBundle{Schema: "registry-export/1", Target: request.Target, ExpectedGeneration: request.ExpectedGeneration, DestinationScope: digest(root), BaselineManifest: baseline, Sources: []ExportSource{}, Pins: []ExportPin{}, Artifacts: artifact.Tree{Entries: []artifact.Entry{}}}
	ids := append([]string(nil), request.IDs...)
	sort.Strings(ids)
	if len(ids) > 200 {
		return b, failure("validation_error", "export.ids", "at most 200 roots allowed")
	}
	if len(ids) == 0 {
		rows, err := q.QueryContext(ctx, `SELECT e.body FROM published p JOIN publication_events e ON p.seq=e.seq ORDER BY e.id`)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return b, err
			}
			var r Record
			if err = json.Unmarshal(raw, &r); err != nil {
				rows.Close()
				return b, err
			}
			if exportable(r.Kind) && r.Lifecycle != "archived" && r.Lifecycle != "deleted" {
				ids = append(ids, r.ID)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
	}
	records := map[string]Record{}
	var visit func(string, string) error
	visit = func(id, pin string) error {
		current, err := readPublished(ctx, q, id, -1)
		if err != nil || current.Lifecycle == "archived" || current.Lifecycle == "deleted" {
			return failure("reference_unavailable", "export.ids", "published dependency unavailable")
		}
		r := current
		if pin != "" && pin != r.RevisionID {
			var raw []byte
			if err = q.QueryRowContext(ctx, `SELECT body FROM publication_events WHERE id=? AND revision=? ORDER BY seq LIMIT 1`, id, pin).Scan(&raw); err != nil {
				return failure("reference_unavailable", "export.ids", "published pin unavailable")
			}
			if err = json.Unmarshal(raw, &r); err != nil {
				return err
			}
		}
		if old, ok := records[id]; ok {
			if old.RevisionID != r.RevisionID {
				return failure("version_conflict", "export.ids", "scope requires incompatible revisions of one identity")
			}
			return nil
		}
		if len(records) >= 200 {
			return failure("validation_error", "export.ids", "dependency closure exceeds 200 records")
		}
		records[id] = r
		for _, e := range r.Edges {
			if e.Relation.To.Authority != "registry" {
				continue
			}
			p := r.TargetPins[e.ID]
			if p == "" {
				return failure("reference_unavailable", "export.ids", "published dependency lacks immutable pin")
			}
			if err = visit(e.Relation.To.ID, p); err != nil {
				return err
			}
		}
		return nil
	}
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			return b, failure("validation_error", "export.ids", "duplicate identity")
		}
		if err := visit(id, ""); err != nil {
			return b, err
		}
		if !exportable(records[id].Kind) {
			return b, failure("non_materializable", "export.ids", "selected kind has no export mapping")
		}
	}
	all := make([]Record, 0, len(records))
	for _, r := range records {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	if err := checkPublicationCycles(ctx, q, all); err != nil {
		return b, err
	}
	bytes := 0
	for _, r := range all {
		b.Pins = append(b.Pins, ExportPin{r.ID, r.RevisionID})
		if !exportable(r.Kind) {
			continue
		}
		files, err := renderExportFiles(r)
		if err != nil {
			return b, err
		}
		for _, file := range files {
			raw := file.Content
			if raw == nil {
				raw = []byte{}
			}
			bytes += len(raw)
			if bytes > MaxRequestBytes {
				return b, failure("validation_error", "export.ids", "rendered scope exceeds 2 MiB")
			}
			for _, part := range strings.Split(file.Path, "/") {
				if len(part) > 255 {
					return b, failure("non_materializable", "export.ids", "output path component exceeds filesystem bound")
				}
			}
			hash := artifactDigest(raw)
			b.Sources = append(b.Sources, ExportSource{r.ID, r.RevisionID, file.Path, "sha256:" + hash.Hex})
			b.Artifacts.Entries = append(b.Artifacts.Entries, artifact.Entry{Path: file.Path, Kind: artifact.EntryFile, Mode: 0600, Bytes: raw, Digest: hash, Ownership: artifact.Ownership{EntryID: r.ID + "#" + file.Path, GroupID: "registry-export", Generation: r.RevisionID}, Provenance: artifact.Provenance{Source: "registry", SourcePath: r.ID, Revision: r.RevisionID}})
		}
	}
	raw, err := json.MarshalIndent(struct {
		Sources []ExportSource `json:"sources"`
		Pins    []ExportPin    `json:"pins"`
	}{b.Sources, b.Pins}, "", "  ")
	if err != nil {
		return b, err
	}
	b.Artifacts.Entries = append(b.Artifacts.Entries, artifact.Entry{Path: "registry-sources.json", Kind: artifact.EntryFile, Mode: 0600, Bytes: append(raw, '\n'), Ownership: artifact.Ownership{EntryID: "registry-export-manifest", GroupID: "registry-export"}, Provenance: artifact.Provenance{Source: "registry"}})
	sort.Slice(b.Artifacts.Entries, func(i, j int) bool { return b.Artifacts.Entries[i].Path < b.Artifacts.Entries[j].Path })
	identity := b
	identity.ExpectedGeneration = ""
	identity.BaselineManifest = ""
	b.Generation, err = bundleDigest(identity)
	if err != nil {
		return b, err
	}
	b.Digest, err = bundleDigest(b)
	return b, err
}

// Refuse symlinks and nonregular leaves before any shared pathname reader is
// called, including Cairn's control manifest. No directories are created here.
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
		return nil, "", nil
	}
	if err != nil || !info.IsDir() {
		return nil, "", materialize.ErrUnsafeTarget
	}
	info, err = os.Lstat(materialize.ManifestPath(root))
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxRequestBytes {
		return nil, "", materialize.ErrMissingManifest
	}
	m, err := materialize.LoadManifest(root)
	if err != nil {
		return nil, "", materialize.ErrMalformedDocument
	}
	seen := map[string]bool{}
	for _, e := range m.Entries {
		if seen[e.Path] || safeExportPath(root, e.Path) != nil {
			return nil, "", materialize.ErrUnsafeTarget
		}
		seen[e.Path] = true
	}
	return &m, digest(m), nil
}
func exportDiskRequest(root string, b ExportBundle) (materialize.Request, error) {
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
func exportEngine() *materialize.DefaultEngine {
	return materialize.NewEngine(materialize.EngineOptions{Now: func() time.Time { return time.Unix(0, 0).UTC() }})
}
func exportError(err error) error {
	if err == nil {
		return nil
	}
	var ce *Error
	if errors.As(err, &ce) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return failure("version_conflict", "materializer", "output drift, unsafe output or incomplete apply; inspect attempt")
}

func (s *Service) exportCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	exportMu.Lock()
	defer exportMu.Unlock()
	if op == "export_plan" || op == "export_authorize" || (op == "export_diff" && a.Export != nil) {
		if a.Export == nil || s.exportTargets[a.Export.Target] == "" {
			return nil, failure("forbidden", "export.target", "configured output binding required")
		}
		root := s.exportTargets[a.Export.Target]
		_, baseline, err := exportBaseline(root)
		if err != nil {
			return nil, exportError(err)
		}
		var b ExportBundle
		if op != "export_authorize" {
			err = s.DB.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
				var e error
				b, e = buildExport(ctx, q, *a.Export, root, baseline)
				return e
			})
			if err != nil {
				return nil, err
			}
			req, err := exportDiskRequest(root, b)
			if err != nil {
				return nil, exportError(err)
			}
			plan, err := exportEngine().Plan(ctx, req)
			return ExportPlan{Bundle: b, Report: plan.Report, WillMutate: plan.WillMutate}, exportError(err)
		}
		authority := s.authorities[p.ID]
		if authority == "" {
			return nil, failure("forbidden", "authority", "configured authority required")
		}
		now := time.Now().UTC()
		deadline := now.Add(5 * time.Minute)
		if a.ExpiresAt != "" {
			deadline, err = time.Parse(time.RFC3339Nano, a.ExpiresAt)
			if err != nil || !deadline.After(now) || deadline.After(now.Add(24*time.Hour)) {
				return nil, failure("validation_error", "expires_at", "future start deadline within 24 hours required")
			}
		}
		var receipt ExportReceipt
		err = s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
			var e error
			b, e = buildExport(ctx, tx, *a.Export, root, baseline)
			if e != nil {
				return e
			}
			if a.Hash == "" || a.Hash != b.Digest {
				return failure("version_conflict", "hash", "plan snapshot changed; plan again")
			}
			policy, e := s.policy(ctx, tx)
			if e != nil {
				return e
			}
			receipt = ExportReceipt{ID: "export:" + newID(), Caller: p.ID, Authority: authority, Action: "export", Bundle: b, Destination: root, PolicyRevision: policy.Revision, AuthorizedAt: now.Format(time.RFC3339Nano), StartDeadline: deadline.Format(time.RFC3339Nano)}
			raw, e := json.Marshal(receipt)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO export_receipts(id,caller,authority,body) VALUES(?,?,?,?)`, receipt.ID, p.ID, authority, raw); e != nil {
				return e
			}
			state, _ := json.Marshal(ExportAttempt{ReceiptID: receipt.ID, State: "authorized"})
			_, e = tx.ExecContext(ctx, `INSERT INTO export_attempts(receipt_id,body) VALUES(?,?)`, receipt.ID, state)
			return e
		})
		return receipt, err
	}
	var receipt ExportReceipt
	var attempt ExportAttempt
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT body FROM export_receipts WHERE id=? AND caller=?`, a.ReceiptID, p.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("not_found", "receipt_id", "caller-owned receipt required")
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT body FROM export_attempts WHERE receipt_id=?`, a.ReceiptID).Scan(&raw)
		if err != nil {
			return failure("forbidden", "receipt_id", "receipt has no authorized attempt")
		}
		if err = json.Unmarshal(raw, &attempt); err != nil {
			return err
		}
		if op == "export_attempt_get" {
			return nil
		}
		hash, err := bundleDigest(receipt.Bundle)
		if err != nil {
			return err
		}
		root := s.exportTargets[receipt.Bundle.Target]
		if receipt.Action != "export" || receipt.Caller != p.ID || receipt.Authority != s.authorities[p.ID] || root == "" || root != receipt.Destination || digest(root) != receipt.Bundle.DestinationScope || hash != receipt.Bundle.Digest || hash != a.Hash || receipt.PolicyRevision == "" {
			return failure("forbidden", "receipt_id", "receipt content, principal or destination mismatch")
		}
		if op == "export_diff" {
			return nil
		}
		if op != "export_apply" {
			return failure("not_found", "operation", "unknown export operation")
		}
		if receipt.Authority == "" {
			return failure("forbidden", "authority", "configured authority required")
		}
		deadline, err := time.Parse(time.RFC3339Nano, receipt.StartDeadline)
		if err != nil || !time.Now().Before(deadline) {
			return failure("version_conflict", "receipt_id", "start deadline expired; fresh authorization required")
		}
		if attempt.State != "authorized" {
			return failure("version_conflict", "receipt_id", "attempt already started; inspect outcome and obtain fresh authorization")
		}
		attempt.State = "started"
		attempt.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		raw, _ = json.Marshal(attempt)
		_, err = tx.ExecContext(ctx, `UPDATE export_attempts SET body=? WHERE receipt_id=?`, raw, receipt.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if op == "export_attempt_get" {
		return attempt, nil
	}
	// No Registry transaction spans filesystem operations. Later archive/policy
	// changes do not revoke this previously issued immutable snapshot authorization.
	req, err := exportDiskRequest(receipt.Destination, receipt.Bundle)
	if op == "export_diff" {
		if err != nil {
			return nil, exportError(err)
		}
		plan, e := exportEngine().Plan(ctx, req)
		return plan, exportError(e)
	}
	var handle materialize.Handle
	if err == nil {
		handle, err = guardedExportApply(ctx, req, receipt.Bundle.BaselineManifest)
	}
	attempt.Handle = &handle
	attempt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	attempt.State = "completed"
	if err != nil {
		attempt.State = "failed"
		attempt.Failure = "materializer_incomplete"
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	raw, _ := json.Marshal(attempt)
	if _, recordErr := s.DB.ExecContext(recordCtx, `UPDATE export_attempts SET body=? WHERE receipt_id=?`, raw, receipt.ID); recordErr != nil {
		return nil, failure("unavailable", "attempt", "outcome not recorded; inspect started attempt before recovery")
	}
	return handle, exportError(err)
}

// The shared engine owns all file writes. Recheck regular leaves and the control
// manifest after its plan and immediately before each write. These checks detect
// observed external drift; they do not claim an OS-wide lock on external writers.
func guardedExportApply(ctx context.Context, req materialize.Request, baseline string) (materialize.Handle, error) {
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
	engine := materialize.NewEngine(materialize.EngineOptions{Now: func() time.Time { return time.Unix(0, 0).UTC() }, BeforeWrite: func(_ string, e artifact.Entry) error {
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
	}, BeforeRename: func(_, target string) error {
		if err := safeExportPath(target, ""); err != nil {
			return err
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			return materialize.ErrTargetExists
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
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxRequestBytes+1))
	if err != nil || len(raw) > MaxRequestBytes {
		return "", materialize.ErrUnsafeTarget
	}
	return fmt.Sprint(info.Mode().Perm(), ":", artifactDigest(raw).Hex), nil
}

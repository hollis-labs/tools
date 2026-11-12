package legacy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hollis-labs/tools/conduit/internal/blob"
	"github.com/hollis-labs/tools/conduit/internal/proc"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

// oldSchema is the pre-Substrate layout, as the old build created it (tables, then the columns added later).
const oldSchema = `
CREATE TABLE runs(
  id TEXT PRIMARY KEY, session TEXT NOT NULL DEFAULT '', parent TEXT NOT NULL DEFAULT '',
  principal TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '',
  command TEXT NOT NULL, parsed TEXT NOT NULL, effect TEXT NOT NULL,
  policy_verdict TEXT NOT NULL, policy_reasons TEXT NOT NULL, policy_mode TEXT NOT NULL,
  approval TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
  created_at TEXT NOT NULL, ended_at TEXT NOT NULL DEFAULT '',
  sandbox TEXT NOT NULL DEFAULT '', request TEXT NOT NULL DEFAULT '', approved_by TEXT NOT NULL DEFAULT '', approved_at TEXT NOT NULL DEFAULT '',
  heartbeat_at TEXT NOT NULL DEFAULT '', owner_pid INTEGER NOT NULL DEFAULT 0, owner_start INTEGER NOT NULL DEFAULT 0,
  pgid INTEGER NOT NULL DEFAULT 0, pgid_start INTEGER NOT NULL DEFAULT 0,
  failure_class TEXT NOT NULL DEFAULT '', failure_message TEXT NOT NULL DEFAULT '', panic_ref TEXT NOT NULL DEFAULT '');
CREATE TABLE attempts(
  run_id TEXT NOT NULL REFERENCES runs(id), n INTEGER NOT NULL, retry_of INTEGER NOT NULL DEFAULT 0,
  started TEXT NOT NULL, ended TEXT NOT NULL, exit_code INTEGER NOT NULL, signal TEXT NOT NULL DEFAULT '',
  class TEXT NOT NULL, start_err TEXT NOT NULL DEFAULT '',
  stdout_ref TEXT NOT NULL DEFAULT '', stderr_ref TEXT NOT NULL DEFAULT '',
  stdout_bytes INTEGER NOT NULL DEFAULT 0, stderr_bytes INTEGER NOT NULL DEFAULT 0,
  truncated INTEGER NOT NULL DEFAULT 0, redactions INTEGER NOT NULL DEFAULT 0, scrubbed TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY(run_id, n));
CREATE TABLE tokens(
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL,
  created_at TEXT NOT NULL, revoked_at TEXT NOT NULL DEFAULT '', last_used TEXT NOT NULL DEFAULT '');
CREATE TABLE panics(
  ref TEXT PRIMARY KEY, at TEXT NOT NULL, where_ TEXT NOT NULL, value TEXT NOT NULL, stack TEXT NOT NULL,
  run_id TEXT NOT NULL DEFAULT '');
`

type fixture struct {
	dir, db, blobs string
	hashes         []string
	live           *exec.Cmd // a real process a migrated row claims to own
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// oldInstall builds an old-layout database with runs in every state, one of them "running" with the pid and process
// group of a live process of this test, plus attempts that reference a blob, a panic and a token.
func oldInstall(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir()}
	f.db = filepath.Join(f.dir, "main.db")
	f.blobs = filepath.Join(f.dir, "blobs")
	out := []byte("hello\n")
	h := sum(out)
	if err := os.MkdirAll(filepath.Join(f.blobs, h[:2]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.blobs, h[:2], h), out, 0o600); err != nil {
		t.Fatal(err)
	}
	f.hashes = []string{h}

	f.live = exec.Command("sleep", "60")
	f.live.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := f.live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.live.Process.Kill(); _ = f.live.Wait() })

	pgStart, _ := proc.StartTime(f.live.Process.Pid)
	db, err := sql.Open("sqlite", f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	ins := `INSERT INTO runs(id,principal,host,cwd,command,parsed,effect,policy_verdict,policy_reasons,policy_mode,approval,state,created_at,approved_at,heartbeat_at,owner_pid,owner_start,pgid,pgid_start) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	host, _ := os.Hostname()
	rows := [][]any{
		{"r-done", "token:agent", host, "/w", "echo hello", "{}", "read", "allow", "[]", "enforce", "auto", "done", old, "", "", 0, 0, 0, 0},
		{"r-pending", "token:agent", host, "/w", "touch x", "{}", "write", "ask", "[]", "enforce", "", "pending", old, "", "", 0, 0, 0, 0},
		{"r-approved", "token:agent", host, "/w", "touch y", "{}", "write", "ask", "[]", "enforce", "tty", "approved", old, old, "", 0, 0, 0, 0},
		{"r-running", "token:agent", host, "/w", "sleep 60", "{}", "exec", "allow", "[]", "enforce", "auto", "running", old, "", old, 1, 0, f.live.Process.Pid, pgStart},
	}
	for _, r := range rows {
		if _, err := db.Exec(ins, r...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO attempts(run_id,n,started,ended,exit_code,class,stdout_ref,stdout_bytes) VALUES('r-done',1,?,?,0,'ok',?,6)`, old, old, "cas://sha256/"+h); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO panics(ref,at,where_,value,stack) VALUES('pnc_1',?,'x','boom','stack')`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tokens(id,name,hash,scopes,created_at,last_used) VALUES('tok_1','operator',?, 'exec.run exec.read',?,?)`, sum([]byte("cdt_old-secret")), old, old); err != nil {
		t.Fatal(err)
	}
	return f
}

func newTarget(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "conduit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, filepath.Join(dir, "blobs")
}

func fileSum(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // a test reading its own temp file
	if err != nil {
		t.Fatal(err)
	}
	return sum(b)
}

func TestMigrateCopiesEverythingProvesItAndNeverWritesTheOldFiles(t *testing.T) {
	f := oldInstall(t)
	st, blobsTo := newTarget(t)
	before := fileSum(t, f.db)
	rep, err := Migrate(context.Background(), st, Options{From: f.db, BlobsFrom: f.blobs, BlobsTo: blobsTo})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"runs": 4, "attempts": 1, "panics": 1, "retired_tokens": 1}
	for _, tr := range rep.Tables {
		if tr.Source != want[tr.Table] || tr.Target != want[tr.Table] || tr.Mismatches != 0 {
			t.Errorf("%+v", tr)
		}
	}
	if rep.NonTermRun != 3 || rep.Blobs != 1 {
		t.Errorf("report %+v", rep)
	}
	if got := fileSum(t, f.db); got != before {
		t.Fatal("the old database was written")
	}
	if _, err := os.Stat(f.db + "-wal"); err == nil {
		t.Error("a -wal file appeared beside the old database")
	}
	if b, err := os.ReadFile(filepath.Join(blobsTo, f.hashes[0][:2], f.hashes[0])); err != nil || string(b) != "hello\n" { //nolint:gosec // a test reading its own temp file
		t.Errorf("blob not copied: %v", err)
	}
	// the migrated output is recallable through the new service
	bl, _ := blob.NewLocal(blobsTo)
	svc := service.New(st, service.WithBlobs(bl))
	if out, err := svc.Recall(context.Background(), "r-done", "stdout", 0); err != nil || string(out) != "hello\n" {
		t.Errorf("recall %q %v", out, err)
	}
	// the retired token keeps its hash and gets a retired_at
	var hash, retired string
	if err := st.Framework().Reader().QueryRowContext(context.Background(), `SELECT hash, retired_at FROM retired_tokens WHERE id='tok_1'`).Scan(&hash, &retired); err != nil || hash != sum([]byte("cdt_old-secret")) || retired == "" {
		t.Errorf("retired token: %v %q %q", err, hash, retired)
	}
	// a second migration into the same database is refused
	if _, err := Migrate(context.Background(), st, Options{From: f.db, BlobsFrom: f.blobs, BlobsTo: blobsTo}); err == nil {
		t.Error("migrated into a non-empty database")
	}
}

// The inert-history rule: a migrated run that was running, approved or pending is never acted on, whatever its owner
// and process group say. The old running row here names a LIVE process group and a dead owner and a stale heartbeat:
// exactly what the reaper would signal in the old build.
func TestMigratedRunsAreNeverReapedStartedApprovedCancelledOrSignalled(t *testing.T) {
	f := oldInstall(t)
	st, blobsTo := newTarget(t)
	if _, err := Migrate(context.Background(), st, Options{From: f.db, BlobsFrom: f.blobs, BlobsTo: blobsTo}); err != nil {
		t.Fatal(err)
	}
	bl, _ := blob.NewLocal(blobsTo)
	svc := service.New(st, service.WithBlobs(bl))
	person := service.AsPerson(context.Background(), "user:owner")

	rep, err := svc.Reap(time.Now())
	if err != nil || rep.Orphaned+rep.Killed+rep.Expired != 0 {
		t.Fatalf("reap acted on migrated rows: %+v %v", rep, err)
	}
	if err := f.live.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the process a migrated row names was signaled: %v", err)
	}
	if n, _ := svc.StartApprovedRemote(); n != 0 {
		t.Errorf("started %d migrated runs", n)
	}
	if n, _ := svc.StartApproved("token:agent"); n != 0 {
		t.Errorf("started %d migrated runs", n)
	}
	if p, _ := svc.Pending(); len(p) != 0 {
		t.Errorf("migrated pending runs are listed as work: %d", len(p))
	}
	for _, id := range []string{"r-pending", "r-approved", "r-running"} {
		if _, err := svc.Approve(person, id); !errors.Is(err, service.ErrLegacy) {
			t.Errorf("approve %s: %v", id, err)
		}
		if _, err := svc.Reject(person, id); !errors.Is(err, service.ErrLegacy) {
			t.Errorf("reject %s: %v", id, err)
		}
		if _, err := svc.Cancel(person, id, true); !errors.Is(err, service.ErrLegacy) {
			t.Errorf("cancel %s: %v", id, err)
		}
		if _, err := svc.Execute(context.Background(), id, service.ExecOpts{}); !errors.Is(err, service.ErrLegacy) {
			t.Errorf("execute %s: %v", id, err)
		}
		if err := svc.Start(id, service.ExecOpts{}); !errors.Is(err, service.ErrLegacy) {
			t.Errorf("start %s: %v", id, err)
		}
		r, _ := svc.Get(id)
		if r.State == store.StateDone || !r.Legacy {
			t.Errorf("%s changed: %+v", id, r)
		}
	}
	got := map[string]string{}
	rows, _ := st.ListRuns(store.Filter{Limit: 100})
	for _, r := range rows {
		got[r.ID] = r.State
	}
	if got["r-pending"] != "pending" || got["r-approved"] != "approved" || got["r-running"] != "running" || got["r-done"] != "done" {
		t.Errorf("states changed: %v", got)
	}
	// a legacy run does not count against a principal's active limit
	if n, _ := st.CountActive("token:agent"); n != 0 {
		t.Errorf("legacy runs count as active: %d", n)
	}
}

func TestMigrateRefusesUnsafeSources(t *testing.T) {
	f := oldInstall(t)
	st, blobsTo := newTarget(t)
	// an unmerged WAL would be ignored by an immutable open: refuse
	if err := os.WriteFile(f.db+"-wal", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), st, Options{From: f.db, BlobsFrom: f.blobs, BlobsTo: blobsTo}); err == nil || !strings.Contains(err.Error(), "write-ahead log") {
		t.Errorf("wal: %v", err)
	}
	_ = os.Remove(f.db + "-wal")
	// a referenced blob that is not in the old store is data loss unless the operator says so
	if err := os.RemoveAll(f.blobs); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), st, Options{From: f.db, BlobsFrom: f.blobs, BlobsTo: blobsTo}); err == nil || !strings.Contains(err.Error(), "referenced but not in") {
		t.Errorf("missing blob: %v", err)
	}
	if _, err := Migrate(context.Background(), st, Options{From: filepath.Join(f.dir, "none.db"), BlobsTo: blobsTo}); err == nil {
		t.Error("missing database accepted")
	}
	// nothing was written to the target by the refused attempts
	var n int
	_ = st.Framework().Reader().QueryRowContext(context.Background(), `SELECT count(*) FROM runs`).Scan(&n)
	if n != 0 {
		t.Errorf("a refused migration left %d rows", n)
	}
}

// A tampered blob is refused rather than copied.
func TestMigrateRefusesABlobThatFailsItsHash(t *testing.T) {
	f := oldInstall(t)
	st, blobsTo := newTarget(t)
	if err := os.WriteFile(filepath.Join(f.blobs, f.hashes[0][:2], f.hashes[0]), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), st, Options{From: f.db, BlobsFrom: f.blobs, BlobsTo: blobsTo}); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Errorf("tampered blob: %v", err)
	}
}

// Package store is conduit's SQLite store: CommandRun records and their
// attempts. Output bytes never live here, only refs into the blob store. The
// service layer is the only package that may use it besides the composition
// root: transports go through the service layer (docs/transport-boundary.md).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// ErrNotFound is returned for a missing or ambiguous run id.
var ErrNotFound = errors.New("run not found")

// Run states (brief section 3).
const (
	StatePending   = "pending"
	StateApproved  = "approved"
	StateRunning   = "running"
	StateDone      = "done"
	StateFailed    = "failed"
	StateDenied    = "denied"
	StateCancelled = "canceled"
	StateExpired   = "expired"
)

// Run is the CommandRun record.
type Run struct {
	ID            string
	Session       string
	Parent        string
	Principal     string
	Host          string
	Cwd           string
	Command       string
	Parsed        string // JSON of cmdparse.Parsed
	Effect        string
	PolicyVerdict string
	PolicyReasons string // JSON
	PolicyMode    string // enforce | shadow
	Sandbox       string // profile the run was confined to, "" for none
	Request       string // JSON of the execution parameters (timeout, env passthrough, retries)
	ApprovedBy    string // principal that approved a pending run
	ApprovedAt    time.Time
	// Liveness of the process executing the run, so a crash that leaves a run
	// "running" can be detected and the orphaned process group cleaned up.
	HeartbeatAt time.Time
	OwnerPID    int
	OwnerStart  int64 // owner's process start time (clock ticks), guards against pid reuse
	PGID        int
	PGIDStart   int64
	// FailureClass/FailureMessage are the structured terminal failure of a run
	// that did not simply exit non-zero: internal_error, orphaned, sandbox_failed.
	FailureClass   string
	FailureMessage string
	PanicRef       string
	Approval       string // "", auto, tty, flag, none
	State          string
	CreatedAt      time.Time
	EndedAt        time.Time
	Attempts       []Attempt
}

// Attempt is one execution of a run; a retry is a new linked attempt.
type Attempt struct {
	RunID       string
	N           int
	RetryOf     int // 0 = first attempt
	Started     time.Time
	Ended       time.Time
	ExitCode    int
	Signal      string
	Class       string
	StartErr    string
	StdoutRef   string
	StderrRef   string
	StdoutBytes int64
	StderrBytes int64
	Truncated   bool
	Redactions  int
	Scrubbed    string // JSON list of env names removed
}

// Filter narrows ListRuns.
type Filter struct {
	Principal string // only runs submitted by this principal
	Session   string
	State     string
	Limit     int
}

// Store is the store handle.
type Store struct {
	db   *sql.DB
	path string
}

const schema = `
CREATE TABLE IF NOT EXISTS runs(
  id TEXT PRIMARY KEY, session TEXT NOT NULL DEFAULT '', parent TEXT NOT NULL DEFAULT '',
  principal TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '',
  command TEXT NOT NULL, parsed TEXT NOT NULL, effect TEXT NOT NULL,
  policy_verdict TEXT NOT NULL, policy_reasons TEXT NOT NULL, policy_mode TEXT NOT NULL,
  approval TEXT NOT NULL DEFAULT '', state TEXT NOT NULL,
  created_at TEXT NOT NULL, ended_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS runs_session ON runs(session, created_at);
CREATE INDEX IF NOT EXISTS runs_created ON runs(created_at);
CREATE TABLE IF NOT EXISTS attempts(
  run_id TEXT NOT NULL REFERENCES runs(id), n INTEGER NOT NULL, retry_of INTEGER NOT NULL DEFAULT 0,
  started TEXT NOT NULL, ended TEXT NOT NULL, exit_code INTEGER NOT NULL, signal TEXT NOT NULL DEFAULT '',
  class TEXT NOT NULL, start_err TEXT NOT NULL DEFAULT '',
  stdout_ref TEXT NOT NULL DEFAULT '', stderr_ref TEXT NOT NULL DEFAULT '',
  stdout_bytes INTEGER NOT NULL DEFAULT 0, stderr_bytes INTEGER NOT NULL DEFAULT 0,
  truncated INTEGER NOT NULL DEFAULT 0, redactions INTEGER NOT NULL DEFAULT 0, scrubbed TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY(run_id, n));
`

// Open opens (creating) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// Columns added after the first release; the error for an existing column is expected.
	for _, col := range []string{
		"sandbox TEXT NOT NULL DEFAULT ''", "request TEXT NOT NULL DEFAULT ''", "approved_by TEXT NOT NULL DEFAULT ''", "approved_at TEXT NOT NULL DEFAULT ''",
		"heartbeat_at TEXT NOT NULL DEFAULT ''", "owner_pid INTEGER NOT NULL DEFAULT 0", "owner_start INTEGER NOT NULL DEFAULT 0",
		"pgid INTEGER NOT NULL DEFAULT 0", "pgid_start INTEGER NOT NULL DEFAULT 0",
		"failure_class TEXT NOT NULL DEFAULT ''", "failure_message TEXT NOT NULL DEFAULT ''", "panic_ref TEXT NOT NULL DEFAULT ''",
	} {
		if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN ` + col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			_ = db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	if _, err := db.Exec(tokenSchema + panicSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init token schema: %w", err)
	}
	return &Store{db: db, path: path}, nil
}

// Path returns the database path.
func (s *Store) Path() string { return s.path }

// Close releases the store.
func (s *Store) Close() error { return s.db.Close() }

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// InsertRun records a new run.
func (s *Store) InsertRun(r *Run) error {
	_, err := s.db.Exec(`INSERT INTO runs(id,session,parent,principal,host,cwd,command,parsed,effect,
		policy_verdict,policy_reasons,policy_mode,approval,state,created_at,ended_at,sandbox,request,approved_by,approved_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Session, r.Parent, r.Principal, r.Host, r.Cwd, r.Command, r.Parsed, r.Effect,
		r.PolicyVerdict, r.PolicyReasons, r.PolicyMode, r.Approval, r.State, ts(r.CreatedAt), ts(r.EndedAt), r.Sandbox, r.Request, r.ApprovedBy, ts(r.ApprovedAt))
	return err
}

// UpdateRun sets the mutable fields of a run: approval, state, end time.
func (s *Store) UpdateRun(id, state, approval string, ended time.Time) error {
	_, err := s.db.Exec(`UPDATE runs SET state=?, approval=CASE WHEN ?<>'' THEN ? ELSE approval END, ended_at=? WHERE id=?`,
		state, approval, approval, ts(ended), id)
	return err
}

// InsertAttempt records one attempt.
func (s *Store) InsertAttempt(a *Attempt) error {
	_, err := s.db.Exec(`INSERT INTO attempts(run_id,n,retry_of,started,ended,exit_code,signal,class,start_err,
		stdout_ref,stderr_ref,stdout_bytes,stderr_bytes,truncated,redactions,scrubbed)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.RunID, a.N, a.RetryOf, ts(a.Started), ts(a.Ended), a.ExitCode, a.Signal, a.Class, a.StartErr,
		a.StdoutRef, a.StderrRef, a.StdoutBytes, a.StderrBytes, a.Truncated, a.Redactions, orJSON(a.Scrubbed))
	return err
}

func orJSON(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}

const runCols = `id,session,parent,principal,host,cwd,command,parsed,effect,policy_verdict,policy_reasons,policy_mode,approval,state,created_at,ended_at,sandbox,request,approved_by,approved_at,heartbeat_at,owner_pid,owner_start,pgid,pgid_start,failure_class,failure_message,panic_ref`

func scanRun(sc interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	var created, ended, approvedAt, heartbeat string
	if err := sc.Scan(&r.ID, &r.Session, &r.Parent, &r.Principal, &r.Host, &r.Cwd, &r.Command, &r.Parsed, &r.Effect,
		&r.PolicyVerdict, &r.PolicyReasons, &r.PolicyMode, &r.Approval, &r.State, &created, &ended, &r.Sandbox, &r.Request, &r.ApprovedBy, &approvedAt, &heartbeat, &r.OwnerPID, &r.OwnerStart, &r.PGID, &r.PGIDStart,
		&r.FailureClass, &r.FailureMessage, &r.PanicRef); err != nil {
		return nil, err
	}
	r.CreatedAt, r.EndedAt, r.ApprovedAt, r.HeartbeatAt = parseTS(created), parseTS(ended), parseTS(approvedAt), parseTS(heartbeat)
	return &r, nil
}

// ResolveID expands an id prefix or suffix to a full id. An ambiguous or
// unknown fragment is ErrNotFound.
func (s *Store) ResolveID(frag string) (string, error) {
	if frag == "" {
		return "", ErrNotFound
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(frag)
	rows, err := s.db.Query(`SELECT id FROM runs WHERE id=? OR id LIKE ? ESCAPE '\' OR id LIKE ? ESCAPE '\' LIMIT 2`, frag, esc+"%", "%"+esc)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	for _, id := range ids {
		if id == frag {
			return id, nil
		}
	}
	return "", ErrNotFound
}

// GetRun loads a run with its attempts.
func (s *Store) GetRun(id string) (*Run, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT run_id,n,retry_of,started,ended,exit_code,signal,class,start_err,stdout_ref,stderr_ref,
		stdout_bytes,stderr_bytes,truncated,redactions,scrubbed FROM attempts WHERE run_id=? ORDER BY n`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a Attempt
		var st, en string
		if err := rows.Scan(&a.RunID, &a.N, &a.RetryOf, &st, &en, &a.ExitCode, &a.Signal, &a.Class, &a.StartErr,
			&a.StdoutRef, &a.StderrRef, &a.StdoutBytes, &a.StderrBytes, &a.Truncated, &a.Redactions, &a.Scrubbed); err != nil {
			return nil, err
		}
		a.Started, a.Ended = parseTS(st), parseTS(en)
		r.Attempts = append(r.Attempts, a)
	}
	return r, rows.Err()
}

// ListRuns returns runs newest first, each with its attempts.
func (s *Store) ListRuns(f Filter) ([]*Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE 1=1`
	var args []any
	if f.Session != "" {
		q += ` AND session=?`
		args = append(args, f.Session)
	}
	if f.Principal != "" {
		q += ` AND principal=?`
		args = append(args, f.Principal)
	}
	if f.State != "" {
		q += ` AND state=?`
		args = append(args, f.State)
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var runs []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		runs = append(runs, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, r := range runs {
		full, err := s.GetRun(r.ID)
		if err != nil {
			return nil, err
		}
		r.Attempts = full.Attempts
	}
	return runs, nil
}

// HistoryEntry is one distinct command that has been executed, with how it fared.
type HistoryEntry struct {
	Command string
	Runs    int
	OK      int // runs that finished done
	Last    time.Time
	Effect  string // effect class at the most recent run
}

// History returns distinct executed commands, most recently run first. Runs that
// were denied or refused never executed and are left out.
func (s *Store) History(limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT r.command, COUNT(*), SUM(r.state='done'), MAX(r.created_at),
		(SELECT x.effect FROM runs x WHERE x.command=r.command AND x.state IN('done','failed') ORDER BY x.created_at DESC LIMIT 1)
		FROM runs r WHERE r.state IN('done','failed') GROUP BY r.command ORDER BY MAX(r.created_at) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HistoryEntry
	for rows.Next() {
		var h HistoryEntry
		var last string
		if err := rows.Scan(&h.Command, &h.Runs, &h.OK, &last, &h.Effect); err != nil {
			return nil, err
		}
		h.Last = parseTS(last)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Approve moves a pending run to approved, atomically: it only succeeds when
// the run is still pending, so two approvals cannot both win.
func (s *Store) Approve(id, kind, by string) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET state=?, approval=?, approved_by=?, approved_at=? WHERE id=? AND state=?`,
		StateApproved, kind, by, ts(time.Now()), id, StatePending)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Transition moves a run from one state to another only if it is in the
// expected state, so concurrent callers cannot double-start a run.
func (s *Store) Transition(id, from, to string) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET state=? WHERE id=? AND state=?`, to, id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CountActive returns how many runs a principal has that are pending approval,
// approved and not yet started, or running.
func (s *Store) CountActive(principal string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE principal=? AND state IN(?,?,?)`,
		principal, StatePending, StateApproved, StateRunning).Scan(&n)
	return n, err
}

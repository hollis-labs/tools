package store

import (
	"database/sql"
	"errors"
	"time"
)

// MarkRunning claims an approved run for execution, atomically, recording who
// owns it and when it last proved it is alive. It reports false if the run was
// not approved any more (another process got there first).
func (s *Store) MarkRunning(id string, ownerPID int, ownerStart int64, now time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET state=?, owner_pid=?, owner_start=?, heartbeat_at=? WHERE id=? AND state=?`,
		StateRunning, ownerPID, ownerStart, ts(now), id, StateApproved)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Heartbeat records that the owner of a running run is alive.
func (s *Store) Heartbeat(id string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE runs SET heartbeat_at=? WHERE id=? AND state=?`, ts(now), id, StateRunning)
	return err
}

// SetProcess records the process group of the command once it has started.
func (s *Store) SetProcess(id string, pgid int, start int64) error {
	_, err := s.db.Exec(`UPDATE runs SET pgid=?, pgid_start=? WHERE id=?`, pgid, start, id)
	return err
}

// Fail moves a run from one state to failed with a structured failure, only if
// it is still in the expected state.
func (s *Store) Fail(id, from, class, message, panicRef string, now time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET state=?, failure_class=?, failure_message=?, panic_ref=?, ended_at=? WHERE id=? AND state=?`,
		StateFailed, class, message, panicRef, ts(now), id, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Running returns every run in the running state, with its attempts.
func (s *Store) Running() ([]*Run, error) {
	return s.ListRuns(Filter{State: StateRunning, Limit: 1000})
}

// ApprovedBefore returns approved runs approved or created before t. Timestamps
// are compared as times: their text form does not sort.
func (s *Store) ApprovedBefore(t time.Time) ([]*Run, error) {
	runs, err := s.ListRuns(Filter{State: StateApproved, Limit: 1000})
	if err != nil {
		return nil, err
	}
	var out []*Run
	for _, r := range runs {
		at := r.ApprovedAt
		if at.IsZero() {
			at = r.CreatedAt
		}
		if at.Before(t) {
			out = append(out, r)
		}
	}
	return out, nil
}

const panicSchema = `
CREATE TABLE IF NOT EXISTS panics(
  ref TEXT PRIMARY KEY, at TEXT NOT NULL, where_ TEXT NOT NULL, value TEXT NOT NULL, stack TEXT NOT NULL,
  run_id TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS panics_at ON panics(at);
`

// PanicRow is a recorded panic.
type PanicRow struct {
	Ref   string
	At    time.Time
	Where string
	Value string
	Stack string
	RunID string
}

// RecordPanic stores a panic record. Value and Stack must already be redacted.
func (s *Store) RecordPanic(p PanicRow) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO panics(ref,at,where_,value,stack,run_id) VALUES(?,?,?,?,?,?)`,
		p.Ref, ts(p.At), p.Where, p.Value, p.Stack, p.RunID)
	return err
}

// Panics lists recorded panics, newest first.
func (s *Store) Panics(limit int) ([]PanicRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT ref,at,where_,value,stack,run_id FROM panics ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PanicRow
	for rows.Next() {
		var p PanicRow
		var at string
		if err := rows.Scan(&p.Ref, &at, &p.Where, &p.Value, &p.Stack, &p.RunID); err != nil {
			return nil, err
		}
		p.At = parseTS(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PanicByRef returns one panic by its ref.
func (s *Store) PanicByRef(ref string) (*PanicRow, error) {
	var p PanicRow
	var at string
	err := s.db.QueryRow(`SELECT ref,at,where_,value,stack,run_id FROM panics WHERE ref=?`, ref).Scan(&p.Ref, &at, &p.Where, &p.Value, &p.Stack, &p.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.At = parseTS(at)
	return &p, nil
}

// SetLivenessForTest sets the liveness columns directly. It exists so tests can
// fabricate what a crashed owner leaves behind; production code uses MarkRunning,
// Heartbeat and SetProcess.
func (s *Store) SetLivenessForTest(id string, ownerPID int, ownerStart int64, pgid int, pgidStart int64, beat time.Time) error {
	_, err := s.db.Exec(`UPDATE runs SET owner_pid=?, owner_start=?, pgid=?, pgid_start=?, heartbeat_at=? WHERE id=?`,
		ownerPID, ownerStart, pgid, pgidStart, ts(beat), id)
	return err
}

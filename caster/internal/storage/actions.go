package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hollis-labs/libs/core/store"
)

// ActionRecord is the recorded outcome of an action run, keyed for idempotency.
type ActionRecord struct {
	Key, Action, Source, Outcome string
	At                           time.Time
}

// RecordAction stores an outcome. The first record for a key wins; a second call with the same
// key returns the stored one.
func (s *Storage) RecordAction(ctx context.Context, r ActionRecord) (ActionRecord, error) {
	err := s.db.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO action_log(idem_key,action,source_ref,outcome_ref,created_at) VALUES(?,?,?,?,?)`,
			r.Key, r.Action, r.Source, r.Outcome, ts(time.Now()))
		return err
	})
	if err != nil {
		return ActionRecord{}, err
	}
	return s.LookupAction(ctx, r.Key)
}

// LookupAction returns the recorded outcome for key.
func (s *Storage) LookupAction(ctx context.Context, key string) (ActionRecord, error) {
	var r ActionRecord
	var at string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT idem_key,action,source_ref,outcome_ref,created_at FROM action_log WHERE idem_key=?`, key).
		Scan(&r.Key, &r.Action, &r.Source, &r.Outcome, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionRecord{}, ErrNotFound
	}
	r.At = parseTS(at)
	return r, err
}

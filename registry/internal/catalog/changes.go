package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/hollis-labs/libs/core/store"
)

type Change struct {
	Sequence int64  `json:"sequence"`
	Record   Record `json:"record"`
}
type Changes struct {
	Changes []Change `json:"changes"`
	Cursor  string   `json:"cursor"`
	HasMore bool     `json:"has_more"`
}

// changes returns immutable publication events, including lifecycle removals.
// Terminal cursors remain usable for future polls.
func (s *Service) changes(ctx context.Context, caller string, query Query) (Changes, error) {
	out := Changes{Changes: []Change{}}
	check := query
	check.Cursor = ""
	check.Limit = 0
	if digest(check) != digest(Query{}) {
		return out, failure("validation_error", "query", "changes accepts only cursor and limit")
	}
	limit := query.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return out, failure("validation_error", "query.limit", "limit must be 1..200")
	}
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		var after int64
		if query.Cursor != "" {
			err := tx.QueryRowContext(ctx, `SELECT sequence FROM change_cursors WHERE token=? AND caller=?`, query.Cursor, caller).Scan(&after)
			if errors.Is(err, sql.ErrNoRows) {
				return failure("validation_error", "query.cursor", "cursor not found for caller")
			}
			if err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT seq,body FROM publication_events WHERE seq>? ORDER BY seq LIMIT ?`, after, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var c Change
			var raw []byte
			if err = rows.Scan(&c.Sequence, &raw); err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &c.Record); err != nil {
				return err
			}
			out.Changes = append(out.Changes, c)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		out.HasMore = len(out.Changes) > limit
		if out.HasMore {
			out.Changes = out.Changes[:limit]
		}
		if len(out.Changes) > 0 {
			after = out.Changes[len(out.Changes)-1].Sequence
		}
		if len(out.Changes) == 0 && query.Cursor != "" {
			out.Cursor = query.Cursor
			return nil
		}
		out.Cursor = newID()
		_, err = tx.ExecContext(ctx, `INSERT INTO change_cursors(token,caller,sequence) VALUES(?,?,?)`, out.Cursor, caller, after)
		return err
	})
	return out, err
}

package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/tools/registry/contracts"
)

func (s *Service) list(ctx context.Context, caller string, query Query) (Page, error) {
	out := Page{Records: []Record{}, Schemas: kinds.Schemas()}
	if query.Publication != "" && query.Publication != "published" && query.Publication != "draft" {
		return out, failure("validation_error", "query.publication", "published or draft required")
	}
	table := "publication_events"
	if query.Publication == "draft" {
		table = "revisions"
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 200 {
		return out, failure("validation_error", "query.limit", "limit must be 1..200")
	}
	token := query.Cursor
	query.Cursor = ""
	hash := digest(query)
	after := ""
	// Reserve the continuation under the same writer transaction as its
	// boundary. Retention must not prune between reading a snapshot and
	// recording the cursor that protects it.
	err := s.DB.WriteTx(ctx, func(ctx context.Context, q store.Tx) error {
		if token != "" {
			var storedHash, storedCaller string
			err := q.QueryRowContext(ctx, `SELECT query_hash,caller,boundary,after_id,as_of FROM cursors WHERE token=?`, token).Scan(&storedHash, &storedCaller, &out.Boundary, &after, &out.AsOf)
			if errors.Is(err, sql.ErrNoRows) {
				return failure("validation_error", "query.cursor", "cursor not found")
			}
			if err != nil {
				return err
			}
			if storedHash != hash || storedCaller != caller {
				return failure("validation_error", "query.cursor", "cursor belongs to another caller or query")
			}
		} else {
			if checkErr := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM `+table).Scan(&out.Boundary); checkErr != nil {
				return checkErr
			}
			out.AsOf = time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
			if out.Boundary > 0 {
				if checkErr := q.QueryRowContext(ctx, `SELECT json_extract(body,'$.updated_at') FROM `+table+` WHERE seq=?`, out.Boundary).Scan(&out.AsOf); checkErr != nil {
					return checkErr
				}
			}
		}
		statement := `SELECT body FROM ` + table + ` WHERE seq IN (SELECT MAX(seq) FROM ` + table + ` WHERE seq<=? GROUP BY id) AND id>?`
		params := []any{out.Boundary, after}
		if strings.TrimSpace(query.Search) != "" {
			index := "publication_fts"
			if table == "revisions" {
				index = "revision_fts"
			}
			// Treat operators and punctuation as literal phrase text.
			phrase := `"` + strings.ReplaceAll(query.Search, `"`, `""`) + `"`
			statement += ` AND seq IN (SELECT rowid FROM ` + index + ` WHERE ` + index + ` MATCH ?)`
			params = append(params, phrase)
		}
		rows, err := q.QueryContext(ctx, statement+` ORDER BY id`, params...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var raw []byte
			var r Record
			if checkErr := rows.Scan(&raw); checkErr != nil {
				return checkErr
			}
			if checkErr := json.Unmarshal(raw, &r); checkErr != nil {
				return checkErr
			}
			if r.Publication == "" {
				r.Publication = "draft"
			}
			if !matches(r, query) {
				continue
			}
			out.Records = append(out.Records, r)
			if len(out.Records) > query.Limit {
				break
			}
		}
		if checkErr := rows.Err(); checkErr != nil {
			return checkErr
		}
		if checkErr := rows.Close(); checkErr != nil {
			return checkErr
		}
		out.HasMore = len(out.Records) > query.Limit
		if out.HasMore {
			out.Records = out.Records[:query.Limit]
		}
		for i := range out.Records {
			r := &out.Records[i]
			if checkErr := resolveEdges(ctx, q, r, out.Boundary); checkErr != nil {
				return checkErr
			}
			for j := range r.Edges {
				r.Edges[j].CheckedAt = out.AsOf
			}
			if !query.Full {
				r.Payload = nil
				r.Metadata = nil
			}
		}
		if out.HasMore {
			out.Cursor = newID()
			after = out.Records[len(out.Records)-1].ID
			_, err = q.ExecContext(ctx, `INSERT INTO cursors(token,query_hash,caller,boundary,after_id,as_of) VALUES(?,?,?,?,?,?)`, out.Cursor, hash, caller, out.Boundary, after, out.AsOf)
			return err
		}
		return nil
	})
	return out, err
}
func matches(r Record, q Query) bool {
	if q.Lifecycle == "" && (r.Lifecycle == "archived" || r.Lifecycle == "deleted") {
		return false
	}
	if q.Lifecycle != "" && q.Lifecycle != "*" && r.Lifecycle != q.Lifecycle {
		return false
	}
	for _, p := range [][2]string{{q.Kind, r.Kind}, {q.Subtype, r.Subtype}, {q.Authority, r.Authority}, {q.Owner, r.Owner}, {q.Key, r.Key}} {
		if p[0] != "" && p[0] != p[1] {
			return false
		}
	}
	if q.ReviewState != "" {
		if r.Review == nil {
			return false
		}
		if q.ReviewState == "pending" {
			if r.Review.QueueState == "closed" {
				return false
			}
		} else if r.Review.State != q.ReviewState {
			return false
		}
	}
	if q.Relation != "" {
		found := false
		for _, e := range r.Edges {
			if string(e.Relation.Type) == q.Relation {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

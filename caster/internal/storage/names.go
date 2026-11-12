package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hollis-labs/libs/core/store"
)

// Name is a named pointer at one revision.
type Name struct {
	NS, Name  string
	Rev       int
	Hash      string
	Title     string
	Tags      string
	CreatedAt time.Time
}

const nameCols = `ns,name,rev,hash,title,tags,created_at`

// Link records a new revision of ns/name pointing at hash. Linking the hash the name already
// points at returns the current revision unchanged. The read of the current revision and the
// insert are one write transaction, so two processes linking the same name get consecutive
// revisions instead of a key conflict.
func (s *Storage) Link(ctx context.Context, ns, name, hash, title, tags string) (Name, error) {
	var out Name
	err := s.db.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		cur, err := scanName(tx.QueryRowContext(ctx, `SELECT `+nameCols+` FROM name_revs WHERE ns=? AND name=? ORDER BY rev DESC LIMIT 1`, ns, name))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && cur.Hash == hash {
			out = cur
			return nil
		}
		rev := cur.Rev + 1
		if err != nil { // first revision: nothing to inherit
			cur = Name{}
		}
		if title == "" {
			title = cur.Title
		}
		if tags == "" {
			tags = cur.Tags
		}
		now := time.Now().UTC()
		if _, err = tx.ExecContext(ctx, `INSERT INTO name_revs(`+nameCols+`) VALUES(?,?,?,?,?,?,?)`,
			ns, name, rev, hash, title, tags, ts(now)); err != nil {
			return err
		}
		out = Name{NS: ns, Name: name, Rev: rev, Hash: hash, Title: title, Tags: tags, CreatedAt: parseTS(ts(now))}
		return nil
	})
	return out, err
}

// Lookup returns a name at rev (0 = latest).
func (s *Storage) Lookup(ctx context.Context, ns, name string, rev int) (Name, error) {
	q := `SELECT ` + nameCols + ` FROM name_revs WHERE ns=? AND name=?`
	args := []any{ns, name}
	if rev > 0 {
		q += ` AND rev=?`
		args = append(args, rev)
	} else {
		q += ` ORDER BY rev DESC LIMIT 1`
	}
	return scanName(s.db.Reader().QueryRowContext(ctx, q, args...))
}

// History lists every revision of a name, newest first.
func (s *Storage) History(ctx context.Context, ns, name string) ([]Name, error) {
	return s.names(ctx, `SELECT `+nameCols+` FROM name_revs WHERE ns=? AND name=? ORDER BY rev DESC`, ns, name)
}

// List returns the latest revision of every name in ns ("" = all), optionally filtered to those
// whose tags contain tag.
func (s *Storage) List(ctx context.Context, ns, tag string) ([]Name, error) {
	q := `SELECT r.ns,r.name,r.rev,r.hash,r.title,r.tags,r.created_at FROM name_revs r
	  JOIN (SELECT ns,name,MAX(rev) m FROM name_revs GROUP BY ns,name) l ON l.ns=r.ns AND l.name=r.name AND l.m=r.rev
	  WHERE (?1='' OR r.ns=?1) AND (?2='' OR (' '||r.tags||' ') LIKE '% '||?2||' %') ORDER BY r.created_at DESC`
	return s.names(ctx, q, ns, tag)
}

// Hit is one search result.
type Hit struct {
	Hash    string
	Snippet string
	Names   []string
}

// Search runs a full-text query over text blobs and reports the names that currently point at each
// hit. The query is FTS5 syntax, handed to the engine as given (a malformed query is an error).
func (s *Storage) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	var hits []Hit
	err := s.db.ReadTx(ctx, func(ctx context.Context, q store.Querier) error {
		rows, err := q.QueryContext(ctx, `SELECT hash, snippet(blob_fts, 1, '[', ']', '…', 12) FROM blob_fts WHERE blob_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var h Hit
			if err = rows.Scan(&h.Hash, &h.Snippet); err != nil {
				_ = rows.Close()
				return err
			}
			hits = append(hits, h)
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for i := range hits {
			nr, err := q.QueryContext(ctx, `SELECT ns||'/'||name FROM name_revs r WHERE hash=? AND rev=(SELECT MAX(rev) FROM name_revs WHERE ns=r.ns AND name=r.name)`, hits[i].Hash)
			if err != nil {
				return err
			}
			for nr.Next() {
				var n string
				if err = nr.Scan(&n); err != nil {
					_ = nr.Close()
					return err
				}
				hits[i].Names = append(hits[i].Names, n)
			}
			if err = nr.Close(); err != nil {
				return err
			}
		}
		return nil
	})
	return hits, err
}

func (s *Storage) names(ctx context.Context, q string, args ...any) ([]Name, error) {
	rows, err := s.db.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Name
	for rows.Next() {
		n, err := scanName(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanName(sc scanner) (Name, error) {
	var n Name
	var at string
	err := sc.Scan(&n.NS, &n.Name, &n.Rev, &n.Hash, &n.Title, &n.Tags, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Name{}, ErrNotFound
	}
	n.CreatedAt = parseTS(at)
	return n, err
}

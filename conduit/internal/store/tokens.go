package store

import (
	"database/sql"
	"errors"
	"time"
)

const tokenSchema = `
CREATE TABLE IF NOT EXISTS tokens(
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL,
  created_at TEXT NOT NULL, revoked_at TEXT NOT NULL DEFAULT '', last_used TEXT NOT NULL DEFAULT '');
`

// Token is an API credential record. The secret itself is never stored, only its hash.
type Token struct {
	ID        string
	Name      string
	Hash      string
	Scopes    string // space-separated
	CreatedAt time.Time
	RevokedAt time.Time
	LastUsed  time.Time
}

// InsertToken stores a token record.
func (s *Store) InsertToken(t *Token) error {
	_, err := s.db.Exec(`INSERT INTO tokens(id,name,hash,scopes,created_at) VALUES(?,?,?,?,?)`,
		t.ID, t.Name, t.Hash, t.Scopes, ts(t.CreatedAt))
	return err
}

func scanToken(sc interface{ Scan(...any) error }) (*Token, error) {
	var t Token
	var c, r, l string
	if err := sc.Scan(&t.ID, &t.Name, &t.Hash, &t.Scopes, &c, &r, &l); err != nil {
		return nil, err
	}
	t.CreatedAt, t.RevokedAt, t.LastUsed = parseTS(c), parseTS(r), parseTS(l)
	return &t, nil
}

const tokenCols = `id,name,hash,scopes,created_at,revoked_at,last_used` //nolint:gosec // column list, not a credential

// TokenByHash finds a live (not revoked) token by the hash of its secret.
func (s *Store) TokenByHash(hash string) (*Token, error) {
	t, err := scanToken(s.db.QueryRow(`SELECT `+tokenCols+` FROM tokens WHERE hash=? AND revoked_at=''`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// TouchToken records a use.
func (s *Store) TouchToken(id string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE tokens SET last_used=? WHERE id=?`, ts(at), id)
	return err
}

// ListTokens returns all tokens, newest first.
func (s *Store) ListTokens() ([]*Token, error) {
	rows, err := s.db.Query(`SELECT ` + tokenCols + ` FROM tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken revokes by id or name.
func (s *Store) RevokeToken(idOrName string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE tokens SET revoked_at=? WHERE (id=? OR name=?) AND revoked_at=''`, ts(at), idOrName, idOrName)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

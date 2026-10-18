package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Grant is a one-use pairing grant. Only the hash of its code is stored.
type Grant struct {
	ID, Label string
	Scopes    []string
	ExpiresAt time.Time
}

// Device is a paired client. Only the hash of its token is stored.
type Device struct {
	ID, Label  string
	Scopes     []string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	Revoked    bool
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }

func joinScopes(s []string) string { return strings.Join(s, " ") }

func splitScopes(s string) []string { return strings.Fields(s) }

// CreateGrant stores a new grant keyed by the hash of its code.
func (s *Store) CreateGrant(g Grant, codeHash string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO grants(id,code_hash,scopes,label,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		g.ID, codeHash, joinScopes(g.Scopes), g.Label, ts(now), ts(g.ExpiresAt))
	return err
}

// ErrGrantRefused is every way a code can fail to exchange: unknown, expired,
// revoked, already used, or asking for scopes the grant does not cover. The
// caller cannot tell which, so a failed guess teaches nothing.
var ErrGrantRefused = errors.New("grant refused")

// ExchangeGrant atomically consumes the grant for codeHash and mints a device
// with the requested scopes (nil = the grant's scopes), in one transaction: a
// failure to mint leaves the grant unconsumed. want must be a subset of the
// grant's scopes.
func (s *Store) ExchangeGrant(codeHash, deviceID, tokenHash, label string, want []string, expires time.Time, now time.Time) (Device, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Device{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var grantID, scopes, glabel string
	err = tx.QueryRow(`UPDATE grants SET consumed_at=? WHERE code_hash=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>?
	  RETURNING id, scopes, label`, ts(now), codeHash, ts(now)).Scan(&grantID, &scopes, &glabel)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrGrantRefused
	}
	if err != nil {
		return Device{}, err
	}
	granted := splitScopes(scopes)
	if want == nil {
		want = granted
	}
	if !subset(want, granted) || len(want) == 0 {
		return Device{}, ErrGrantRefused // rolls back: the grant stays unconsumed
	}
	if label == "" {
		label = glabel
	}
	if _, err := tx.Exec(`INSERT INTO devices(id,token_hash,label,scopes,grant_id,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
		deviceID, tokenHash, label, joinScopes(want), grantID, ts(now), ts(expires)); err != nil {
		return Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, err
	}
	return Device{ID: deviceID, Label: label, Scopes: want, CreatedAt: now, ExpiresAt: expires}, nil
}

func subset(a, of []string) bool {
	have := map[string]bool{}
	for _, s := range of {
		have[s] = true
	}
	for _, s := range a {
		if !have[s] {
			return false
		}
	}
	return true
}

// DeviceByToken returns the live device for a token hash, or ErrNotFound for
// anything unknown, revoked or expired. It records use at most once a minute.
func (s *Store) DeviceByToken(tokenHash string, now time.Time) (Device, error) {
	d, err := scanDevice(s.db.QueryRow(`SELECT id,label,scopes,created_at,expires_at,COALESCE(last_used_at,''),revoked_at IS NOT NULL
	  FROM devices WHERE token_hash=?`, tokenHash))
	if err != nil {
		return Device{}, err
	}
	if d.Revoked || !now.Before(d.ExpiresAt) {
		return Device{}, ErrNotFound
	}
	if d.LastUsedAt.IsZero() || now.Sub(d.LastUsedAt) > time.Minute {
		_, _ = s.db.Exec(`UPDATE devices SET last_used_at=? WHERE id=?`, ts(now), d.ID)
	}
	return d, nil
}

func scanDevice(sc scanner) (Device, error) {
	var d Device
	var scopes, created, expires, used string
	if err := sc.Scan(&d.ID, &d.Label, &scopes, &created, &expires, &used, &d.Revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Device{}, ErrNotFound
		}
		return Device{}, err
	}
	d.Scopes, d.CreatedAt, d.ExpiresAt, d.LastUsedAt = splitScopes(scopes), parseTS(created), parseTS(expires), parseTS(used)
	return d, nil
}

// Devices lists every device, newest first.
func (s *Store) Devices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id,label,scopes,created_at,expires_at,COALESCE(last_used_at,''),revoked_at IS NOT NULL FROM devices ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice revokes a device; revoking twice is not an error.
func (s *Store) RevokeDevice(id string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE devices SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, ts(now), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RenewDevice extends a live device to expires, optionally narrowing its scopes
// (never widening them). It returns the updated device.
func (s *Store) RenewDevice(id string, narrow []string, expires, now time.Time) (Device, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Device{}, err
	}
	defer func() { _ = tx.Rollback() }()
	d, err := scanDevice(tx.QueryRow(`SELECT id,label,scopes,created_at,expires_at,COALESCE(last_used_at,''),revoked_at IS NOT NULL FROM devices WHERE id=?`, id))
	if err != nil {
		return Device{}, err
	}
	if d.Revoked || !now.Before(d.ExpiresAt) {
		return Device{}, ErrNotFound
	}
	scopes := d.Scopes
	if narrow != nil {
		if !subset(narrow, d.Scopes) || len(narrow) == 0 {
			return Device{}, ErrGrantRefused
		}
		scopes = narrow
	}
	if _, err := tx.Exec(`UPDATE devices SET expires_at=?, scopes=? WHERE id=?`, ts(expires), joinScopes(scopes), id); err != nil {
		return Device{}, err
	}
	d.Scopes, d.ExpiresAt = scopes, expires
	return d, tx.Commit()
}

// RevokeGrant revokes an unconsumed grant.
func (s *Store) RevokeGrant(id string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE grants SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, ts(now), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Package store is caster's storage: immutable blobs addressed by sha256 on
// disk, and a SQLite index holding blob metadata, named pointers with revision
// history, and full-text search over text blobs. Mutable things never live in
// the blob tree; a name is a pointer to a hash at a revision.
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // registers the database/sql driver
)

// ErrNotFound is returned for any missing blob, name or revision.
var ErrNotFound = errors.New("not found")

// MaxIndexBytes bounds the text that is full-text indexed per blob.
const MaxIndexBytes = 1 << 20

// Store is the store handle.
type Store struct {
	db       *sql.DB
	blobRoot string
	path     string
}

// Blob is the metadata for one immutable blob.
type Blob struct {
	Hash      string
	Size      int64
	Mime      string
	SourceRef string
	CreatedAt time.Time
}

// Name is a named pointer at one revision.
type Name struct {
	NS, Name  string
	Rev       int
	Hash      string
	Title     string
	Tags      string
	CreatedAt time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS blobs(
  hash TEXT PRIMARY KEY, size INTEGER NOT NULL, mime TEXT NOT NULL,
  source_ref TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS name_revs(
  ns TEXT NOT NULL, name TEXT NOT NULL, rev INTEGER NOT NULL, hash TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
  PRIMARY KEY(ns, name, rev));
CREATE TABLE IF NOT EXISTS action_log(
  idem_key TEXT PRIMARY KEY, action TEXT NOT NULL, source_ref TEXT NOT NULL,
  outcome_ref TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS observations(
  id INTEGER PRIMARY KEY AUTOINCREMENT, text_hash TEXT NOT NULL, generation INTEGER NOT NULL,
  first_source TEXT NOT NULL DEFAULT '', first_at TEXT NOT NULL, last_at TEXT NOT NULL, seen INTEGER NOT NULL DEFAULT 1,
  candidates INTEGER NOT NULL, resolved INTEGER NOT NULL, would_show INTEGER NOT NULL,
  UNIQUE(text_hash, generation));
CREATE TABLE IF NOT EXISTS detections(
  id INTEGER PRIMARY KEY AUTOINCREMENT, observation_id INTEGER NOT NULL REFERENCES observations(id),
  extractor TEXT NOT NULL, confidence TEXT NOT NULL, ref TEXT NOT NULL, scheme TEXT NOT NULL,
  state TEXT NOT NULL, verdict TEXT NOT NULL, would_show INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '',
  label TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS detections_ref ON detections(ref);
CREATE TABLE IF NOT EXISTS grants(
  id TEXT PRIMARY KEY, code_hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL, label TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, expires_at TEXT NOT NULL, consumed_at TEXT, revoked_at TEXT);
CREATE TABLE IF NOT EXISTS devices(
  id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, label TEXT NOT NULL DEFAULT '', scopes TEXT NOT NULL,
  grant_id TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, last_used_at TEXT);
CREATE VIRTUAL TABLE IF NOT EXISTS blob_fts USING fts5(hash UNINDEXED, body);
`

// Open opens (creating if needed) the index at dbPath and the blob tree next to it.
func Open(dbPath string) (*Store, error) {
	root := filepath.Join(filepath.Dir(dbPath), "blobs")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db, blobRoot: root, path: dbPath}, nil
}

// Path returns the database path.
func (s *Store) Path() string { return s.path }

// BlobRoot returns the blob tree root.
func (s *Store) BlobRoot() string { return s.blobRoot }

// Close releases the store.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) blobPath(hash string) string {
	return filepath.Join(s.blobRoot, "sha256", hash[:2], hash[2:])
}

// PutBlob streams r into the CAS, verifying the digest as it goes, and records
// metadata. Putting the same bytes again is a no-op that returns the existing blob.
func (s *Store) PutBlob(r io.Reader, mime, sourceRef string) (Blob, error) {
	tmpDir := filepath.Join(s.blobRoot, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return Blob{}, err
	}
	tmp, err := os.CreateTemp(tmpDir, "put-*")
	if err != nil {
		return Blob{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	head := make([]byte, 0, 512)
	n, err := io.Copy(io.MultiWriter(tmp, h, &capture{buf: &head}), r)
	if err != nil {
		_ = tmp.Close()
		return Blob{}, err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Blob{}, err
	}
	if err = tmp.Close(); err != nil {
		return Blob{}, err
	}
	hash := hex.EncodeToString(h.Sum(nil))
	dst := s.blobPath(hash)
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return Blob{}, err
	}
	if _, err = os.Stat(dst); err != nil {
		if err = os.Chmod(tmp.Name(), 0o400); err != nil {
			return Blob{}, err
		}
		if err = os.Rename(tmp.Name(), dst); err != nil {
			return Blob{}, err
		}
	}
	if mime == "" {
		mime = "text/plain; charset=utf-8"
		if !utf8.Valid(head) || strings.IndexByte(string(head), 0) >= 0 {
			mime = "application/octet-stream"
		}
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT OR IGNORE INTO blobs(hash,size,mime,source_ref,created_at) VALUES(?,?,?,?,?)`,
		hash, n, mime, sourceRef, now.Format(time.RFC3339Nano))
	if err != nil {
		return Blob{}, err
	}
	if added, _ := res.RowsAffected(); added > 0 && strings.HasPrefix(mime, "text/") && n <= MaxIndexBytes {
		if body, err := os.ReadFile(dst); err == nil { //nolint:gosec // dst is the store's own path derived from the content hash
			_, _ = s.db.Exec(`INSERT INTO blob_fts(hash, body) VALUES(?,?)`, hash, string(body))
		}
	}
	return s.HeadBlob(hash)
}

type capture struct{ buf *[]byte }

func (c *capture) Write(p []byte) (int, error) {
	if room := cap(*c.buf) - len(*c.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		*c.buf = append(*c.buf, p[:room]...)
	}
	return len(p), nil
}

// HeadBlob returns blob metadata.
func (s *Store) HeadBlob(hash string) (Blob, error) {
	var b Blob
	var ts string
	err := s.db.QueryRow(`SELECT hash,size,mime,source_ref,created_at FROM blobs WHERE hash=?`, hash).
		Scan(&b.Hash, &b.Size, &b.Mime, &b.SourceRef, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return Blob{}, ErrNotFound
	}
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	return b, err
}

// OpenBlob opens blob bytes and verifies the digest before returning them.
func (s *Store) OpenBlob(hash string) ([]byte, Blob, error) {
	b, err := s.HeadBlob(hash)
	if err != nil {
		return nil, Blob{}, err
	}
	data, err := os.ReadFile(s.blobPath(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, Blob{}, ErrNotFound
	}
	if err != nil {
		return nil, Blob{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, Blob{}, fmt.Errorf("integrity failure: blob %s does not match its digest", hash)
	}
	return data, b, nil
}

// Link records a new revision of ns/name pointing at hash. Linking the hash the
// name already points at returns the current revision unchanged.
func (s *Store) Link(ns, name, hash, title, tags string) (Name, error) {
	cur, err := s.Lookup(ns, name, 0)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Name{}, err
	}
	if err == nil && cur.Hash == hash {
		return cur, nil
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
	_, err = s.db.Exec(`INSERT INTO name_revs(ns,name,rev,hash,title,tags,created_at) VALUES(?,?,?,?,?,?,?)`,
		ns, name, rev, hash, title, tags, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Name{}, err
	}
	return s.Lookup(ns, name, rev)
}

// Lookup returns a name at rev (0 = latest).
func (s *Store) Lookup(ns, name string, rev int) (Name, error) {
	q := `SELECT ns,name,rev,hash,title,tags,created_at FROM name_revs WHERE ns=? AND name=?`
	args := []any{ns, name}
	if rev > 0 {
		q += ` AND rev=?`
		args = append(args, rev)
	} else {
		q += ` ORDER BY rev DESC LIMIT 1`
	}
	return scanName(s.db.QueryRow(q, args...))
}

// History lists every revision of a name, newest first.
func (s *Store) History(ns, name string) ([]Name, error) {
	return s.names(`SELECT ns,name,rev,hash,title,tags,created_at FROM name_revs WHERE ns=? AND name=? ORDER BY rev DESC`, ns, name)
}

// List returns the latest revision of every name in ns ("" = all), optionally
// filtered to those whose tags contain tag.
func (s *Store) List(ns, tag string) ([]Name, error) {
	q := `SELECT r.ns,r.name,r.rev,r.hash,r.title,r.tags,r.created_at FROM name_revs r
	  JOIN (SELECT ns,name,MAX(rev) m FROM name_revs GROUP BY ns,name) l ON l.ns=r.ns AND l.name=r.name AND l.m=r.rev
	  WHERE (?1='' OR r.ns=?1) AND (?2='' OR (' '||r.tags||' ') LIKE '% '||?2||' %') ORDER BY r.created_at DESC`
	return s.names(q, ns, tag)
}

// Hit is one search result.
type Hit struct {
	Hash    string
	Snippet string
	Names   []string
}

// Search runs a full-text query over text blobs and reports the names that
// currently point at each hit.
func (s *Store) Search(query string, limit int) ([]Hit, error) {
	rows, err := s.db.Query(`SELECT hash, snippet(blob_fts, 1, '[', ']', '…', 12) FROM blob_fts WHERE blob_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Hash, &h.Snippet); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range hits {
		nr, err := s.db.Query(`SELECT ns||'/'||name FROM name_revs r WHERE hash=? AND rev=(SELECT MAX(rev) FROM name_revs WHERE ns=r.ns AND name=r.name)`, hits[i].Hash)
		if err != nil {
			return nil, err
		}
		for nr.Next() {
			var n string
			_ = nr.Scan(&n)
			hits[i].Names = append(hits[i].Names, n)
		}
		_ = nr.Close()
	}
	return hits, nil
}

func (s *Store) names(q string, args ...any) ([]Name, error) {
	rows, err := s.db.Query(q, args...)
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
	var ts string
	err := sc.Scan(&n.NS, &n.Name, &n.Rev, &n.Hash, &n.Title, &n.Tags, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return Name{}, ErrNotFound
	}
	n.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	return n, err
}

// ActionRecord is the recorded outcome of an action run, keyed for idempotency.
type ActionRecord struct {
	Key, Action, Source, Outcome string
	At                           time.Time
}

// RecordAction stores an outcome. The first record for a key wins; a second
// call with the same key returns the stored one.
func (s *Store) RecordAction(r ActionRecord) (ActionRecord, error) {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO action_log(idem_key,action,source_ref,outcome_ref,created_at) VALUES(?,?,?,?,?)`,
		r.Key, r.Action, r.Source, r.Outcome, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ActionRecord{}, err
	}
	return s.LookupAction(r.Key)
}

// LookupAction returns the recorded outcome for key.
func (s *Store) LookupAction(key string) (ActionRecord, error) {
	var r ActionRecord
	var ts string
	err := s.db.QueryRow(`SELECT idem_key,action,source_ref,outcome_ref,created_at FROM action_log WHERE idem_key=?`, key).
		Scan(&r.Key, &r.Action, &r.Source, &r.Outcome, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return ActionRecord{}, ErrNotFound
	}
	r.At, _ = time.Parse(time.RFC3339Nano, ts)
	return r, err
}

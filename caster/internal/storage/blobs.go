package storage

import (
	"context"
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

	"github.com/hollis-labs/libs/core/store"
)

// Blob is the metadata for one immutable blob.
type Blob struct {
	Hash      string
	Size      int64
	Mime      string
	SourceRef string
	CreatedAt time.Time
}

// BlobPath is where the bytes of hash live in the tree.
func (s *Storage) BlobPath(hash string) string {
	return filepath.Join(s.root, "sha256", hash[:2], hash[2:])
}

// PutBlob streams r into the CAS, verifying the digest as it goes, and records metadata. Putting
// the same bytes again is a no-op that returns the existing blob.
func (s *Storage) PutBlob(ctx context.Context, r io.Reader, mime, sourceRef string) (Blob, error) {
	tmpDir := filepath.Join(s.root, "tmp")
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
	dst := s.BlobPath(hash)
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
	// The text is read before the transaction: the callback may be retried and must have no side
	// effects but its tx.
	var body []byte
	if strings.HasPrefix(mime, "text/") && n <= MaxIndexBytes {
		if body, err = os.ReadFile(dst); err != nil { //nolint:gosec // dst is the store's own path derived from the content hash
			body = nil
		}
	}
	err = s.db.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		res, txErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO blobs(hash,size,mime,source_ref,created_at) VALUES(?,?,?,?,?)`,
			hash, n, mime, sourceRef, ts(time.Now()))
		if txErr != nil {
			return txErr
		}
		if added, _ := res.RowsAffected(); added > 0 && body != nil {
			_, txErr = tx.ExecContext(ctx, `INSERT INTO blob_fts(hash, body) VALUES(?,?)`, hash, string(body))
		}
		return txErr
	})
	if err != nil {
		return Blob{}, err
	}
	return s.HeadBlob(ctx, hash)
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
func (s *Storage) HeadBlob(ctx context.Context, hash string) (Blob, error) {
	var b Blob
	var at string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT hash,size,mime,source_ref,created_at FROM blobs WHERE hash=?`, hash).
		Scan(&b.Hash, &b.Size, &b.Mime, &b.SourceRef, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Blob{}, ErrNotFound
	}
	b.CreatedAt = parseTS(at)
	return b, err
}

// OpenBlob returns the blob bytes after verifying the digest.
func (s *Storage) OpenBlob(ctx context.Context, hash string) ([]byte, Blob, error) {
	b, err := s.HeadBlob(ctx, hash)
	if err != nil {
		return nil, Blob{}, err
	}
	data, err := os.ReadFile(s.BlobPath(hash))
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

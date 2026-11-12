package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hollis-labs/libs/core/store"
)

// rowQuerier is what both the old *sql.DB and the new store's reader offer.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// tableDigest counts the rows and hashes every value of every row in primary-key order. Each value is
// written with its type and length, so two tables digest equal only when every cell is equal.
func tableDigest(ctx context.Context, q rowQuerier, name string, t table) (count int, digest string, err error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s`, strings.Join(t.cols, ", "), name, t.pk))
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = rows.Close() }()
	h := sha256.New()
	for rows.Next() {
		vals := make([]any, len(t.cols))
		ptrs := make([]any, len(t.cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return 0, "", err
		}
		for _, v := range vals {
			writeValue(h, v)
		}
		count++
	}
	return count, hex.EncodeToString(h.Sum(nil)), rows.Err()
}

func writeValue(h hash.Hash, v any) {
	put := func(tag byte, b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		_, _ = h.Write([]byte{tag})
		_, _ = h.Write(n[:])
		_, _ = h.Write(b)
	}
	switch x := v.(type) {
	case nil:
		put('n', nil)
	case int64:
		put('i', []byte(fmt.Sprint(x)))
	case float64:
		put('f', []byte(fmt.Sprintf("%v", x)))
	case string:
		put('s', []byte(x))
	case []byte:
		put('b', x)
	case bool:
		put('i', []byte(fmt.Sprint(map[bool]int{true: 1, false: 0}[x])))
	default:
		put('?', []byte(fmt.Sprint(x)))
	}
}

func compareTable(ctx context.Context, src *sql.DB, dst *store.Store, t table) (TableReport, error) {
	tr := TableReport{Source: t.from, Target: t.to}
	if _, ok, err := columns(ctx, src, t.from); err != nil {
		return tr, err
	} else if !ok {
		// An older database without this table: it is empty, and the digest of nothing is what the
		// new table must have.
		tr.Note = "absent in the old database"
		h := sha256.New()
		tr.SourceDigest = hex.EncodeToString(h.Sum(nil))
	} else if tr.SourceRows, tr.SourceDigest, err = tableDigest(ctx, src, t.from, t); err != nil {
		return tr, fmt.Errorf("digest %s: %w", t.from, err)
	}
	var err error
	if tr.TargetRows, tr.TargetDigest, err = tableDigest(ctx, dst.Reader(), t.to, t); err != nil {
		return tr, fmt.Errorf("digest %s: %w", t.to, err)
	}
	tr.Equal = tr.SourceRows == tr.TargetRows && tr.SourceDigest == tr.TargetDigest
	return tr, nil
}

var (
	hexName  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	shardDir = regexp.MustCompile(`^[0-9a-f]{2}$`)
)

// oldFile is one file of the old blob tree.
type oldFile struct {
	hash string
	path string
}

// walkOldBlobs lists blobs/sha256/<aa>/<rest>. The tmp directory is partial writes and is counted,
// not copied. Anything else in the tree is a problem: data this migration would silently leave behind.
func walkOldBlobs(oldDir string, rep *Report) ([]oldFile, error) {
	root := filepath.Join(oldDir, "blobs")
	var files []oldFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && p == root {
				return fs.SkipAll // no blob tree: a store that never held a blob
			}
			return err
		}
		rel, _ := filepath.Rel(root, p)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		switch {
		case rel == ".":
			return nil
		case parts[0] == "tmp":
			if !d.IsDir() {
				rep.Blobs.TmpIgnored++
			}
			return nil
		case parts[0] != "sha256":
			rep.Problems = append(rep.Problems, "the old blob tree holds something that is not a blob: blobs/"+rel)
			return nil
		case d.IsDir():
			if len(parts) <= 2 && (len(parts) == 1 || shardDir.MatchString(parts[1])) {
				return nil
			}
			rep.Problems = append(rep.Problems, "unexpected directory in the old blob tree: blobs/"+rel)
			return fs.SkipDir
		case len(parts) == 3 && shardDir.MatchString(parts[1]) && hexName.MatchString(parts[1]+parts[2]):
			files = append(files, oldFile{hash: parts[1] + parts[2], path: p})
		default:
			rep.Problems = append(rep.Problems, "the old blob tree holds something that is not a blob: blobs/"+rel)
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].hash < files[j].hash })
	return files, err
}

// blobRow is the old blobs table, which the file checks need.
func blobRows(ctx context.Context, src *sql.DB) (map[string]int64, error) {
	if _, ok, err := columns(ctx, src, "blobs"); err != nil || !ok {
		return map[string]int64{}, err
	}
	rows, err := src.QueryContext(ctx, `SELECT hash, size FROM blobs`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var h string
		var n int64
		if err := rows.Scan(&h, &n); err != nil {
			return nil, err
		}
		out[h] = n
	}
	return out, rows.Err()
}

// copyBlobs copies every file of the old tree into the new one through a hash and checks it against
// its name, the row and the row's size.
func copyBlobs(ctx context.Context, src *sql.DB, o Options, rep *Report) error {
	return checkBlobs(ctx, src, o, rep, true)
}

// verifyBlobs is the same check against a new tree that already exists, without writing it: each
// old file must have an identical file in the new tree.
func verifyBlobs(ctx context.Context, src *sql.DB, o Options, rep *Report) error {
	return checkBlobs(ctx, src, o, rep, false)
}

func checkBlobs(ctx context.Context, src *sql.DB, o Options, rep *Report, write bool) error {
	rows, err := blobRows(ctx, src)
	if err != nil {
		return err
	}
	rep.Blobs.Rows = len(rows)
	files, err := walkOldBlobs(o.OldDir, rep)
	if err != nil {
		return err
	}
	rep.Blobs.Files = len(files)
	seen := map[string]bool{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		seen[f.hash] = true
		dst := filepath.Join(o.CasRoot, "sha256", f.hash[:2], f.hash[2:])
		sum, n, err := copyThroughHash(f.path, dst, write)
		if err != nil {
			return fmt.Errorf("blob %s: %w", f.hash, err)
		}
		size, inTable := rows[f.hash]
		switch {
		case sum != f.hash:
			rep.Blobs.Corrupt = append(rep.Blobs.Corrupt, f.hash+" (content hashes to "+sum[:12]+")")
		case inTable && size != n:
			rep.Blobs.Corrupt = append(rep.Blobs.Corrupt, fmt.Sprintf("%s (row says %d bytes, file has %d)", f.hash, size, n))
		default:
			rep.Blobs.Verified++
			rep.Blobs.Bytes += n
		}
		if write {
			rep.Blobs.Copied++
		}
		if !inTable {
			rep.Blobs.Orphans = append(rep.Blobs.Orphans, f.hash)
		}
	}
	for h := range rows {
		if !seen[h] {
			rep.Blobs.Missing = append(rep.Blobs.Missing, h)
		}
	}
	sort.Strings(rep.Blobs.Missing)
	return nil
}

// copyThroughHash reads from, returning its sha256 and size. With write set it also writes the bytes
// to dst (mode 0400, directories 0700, through a temp file and a rename, like the store does); without
// it, it compares from with the existing dst byte for byte and reports a difference as a different sum.
func copyThroughHash(from, dst string, write bool) (sum string, n int64, err error) {
	in, err := os.Open(from) //nolint:gosec // the old data directory the operator named
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()
	h := sha256.New()
	if !write {
		n, err = io.Copy(h, in)
		if err != nil {
			return "", 0, err
		}
		sum = hex.EncodeToString(h.Sum(nil))
		got, hashErr := hashFile(dst)
		if hashErr != nil {
			return "", 0, fmt.Errorf("the new tree has no readable copy: %w", hashErr)
		}
		if got != sum {
			return "different from the new copy", n, nil
		}
		return sum, n, nil
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err = io.Copy(io.MultiWriter(tmp, h), in)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	if err = os.Chmod(tmp.Name(), 0o400); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, os.Rename(tmp.Name(), dst)
}

// rebuildFTS indexes the text blobs of the new store: the storage package's rule (text/* up to
// MaxIndexBytes) applied to the copied files. The old index is not copied; it is compared.
func rebuildFTS(ctx context.Context, o Options) error {
	rows, err := o.DB.Reader().QueryContext(ctx, `SELECT hash, size, mime FROM blobs ORDER BY hash`)
	if err != nil {
		return err
	}
	type item struct{ hash string }
	var todo []item
	for rows.Next() {
		var h, mime string
		var size int64
		if err := rows.Scan(&h, &size, &mime); err != nil {
			_ = rows.Close()
			return err
		}
		if strings.HasPrefix(mime, "text/") && size <= MaxIndexBytes {
			todo = append(todo, item{h})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return o.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		for _, it := range todo {
			body, err := os.ReadFile(filepath.Join(o.CasRoot, "sha256", it.hash[:2], it.hash[2:])) //nolint:gosec // the new blob tree, named by digest
			if err != nil {
				continue // a missing file is already a problem of the blob check; it has no index row
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO blob_fts(hash, body) VALUES(?,?)`, it.hash, string(body)); err != nil {
				return err
			}
		}
		return nil
	})
}

func ftsRows(ctx context.Context, q rowQuerier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT hash, body FROM blob_fts`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var h, b string
		if err := rows.Scan(&h, &b); err != nil {
			return nil, err
		}
		out[h] = b
	}
	return out, rows.Err()
}

// compareFTS compares the old index's (hash, body) rows with the rebuilt ones as sets.
func compareFTS(ctx context.Context, src *sql.DB, dst *store.Store, rep *Report) error {
	var have int
	if err := src.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'blob_fts'`).Scan(&have); err != nil {
		return err
	}
	old := map[string]string{}
	if have > 0 {
		var err error
		if old, err = ftsRows(ctx, src); err != nil {
			return fmt.Errorf("read the old index: %w", err)
		}
	}
	neu, err := ftsRows(ctx, dst.Reader())
	if err != nil {
		return fmt.Errorf("read the rebuilt index: %w", err)
	}
	rep.FTS.SourceRows, rep.FTS.RebuiltRows = len(old), len(neu)
	for h, b := range old {
		nb, ok := neu[h]
		switch {
		case !ok:
			rep.FTS.OnlyInSource++
		case nb != b:
			rep.FTS.BodyDiffers++
		}
	}
	for h := range neu {
		if _, ok := old[h]; !ok {
			rep.FTS.OnlyInRebuilt++
		}
	}
	return nil
}

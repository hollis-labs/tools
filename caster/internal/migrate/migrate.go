// Package migrate copies a pre-Substrate Caster data directory (main.db and blobs/) into the new
// store (caster.db and cas/) and proves the copy. It is one way: the old files are never written.
//
// The old files are read as plain files. The database and its write-ahead log are first copied to a
// scratch directory (and hashed before, during and after, so a copy taken while a writer was still
// running is refused), and SQLite only ever opens the copy. So not even a read-only SQLite
// connection touches the old directory (a reader would otherwise create or rewrite its -shm file).
// After the copy the old files are hashed again and the report says whether they are unchanged.
//
// What is proved:
//   - every table: row count and an ordered digest over every value of every row, source against
//     the new database read back (blobs, name_revs, action_log, observations, detections; grants and
//     devices into legacy_grants and legacy_devices);
//   - every blob: the file is copied through a hash, its digest must equal its name and the blobs
//     row, its size must equal the row's size;
//   - the full-text index is derived, so it is rebuilt from the copied blobs and its (hash, body)
//     rows are compared with the source index's, not just counted;
//   - nothing is left that the migration does not cover: a table, index, trigger or view it does not
//     know, a file in the blob tree that is not a blob, a blob row without a file, all fail the run.
package migrate

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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	_ "modernc.org/sqlite" // registers the database/sql driver
)

// MaxIndexBytes bounds the text that is full-text indexed per blob (the storage package's rule).
const MaxIndexBytes = 1 << 20

// Options say what to copy where.
type Options struct {
	OldDir  string       // the old data directory: main.db and blobs/
	Scratch string       // where the old database is copied before it is opened; created, removed after
	DB      *store.Store // the new, empty database with the schema applied
	CasRoot string       // the new blob tree; created
	Now     func() time.Time
}

// Report is the proof. OK is true only when Problems is empty.
type Report struct {
	OldDir      string       `json:"old_dir"`
	StartedAt   string       `json:"started_at"`
	FinishedAt  string       `json:"finished_at"`
	SourceFiles []FileDigest `json:"source_files"`
	// OldUnchanged: the old files hash and stat the same after the run as before it.
	OldUnchanged bool          `json:"old_unchanged"`
	Tables       []TableReport `json:"tables"`
	Blobs        BlobReport    `json:"blobs"`
	FTS          FTSReport     `json:"fts"`
	Problems     []string      `json:"problems"`
	OK           bool          `json:"ok"`
}

// FileDigest identifies one old file.
type FileDigest struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	MTime  string `json:"mtime"`
}

// TableReport compares one table.
type TableReport struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceRows   int    `json:"source_rows"`
	TargetRows   int    `json:"target_rows"`
	SourceDigest string `json:"source_digest"`
	TargetDigest string `json:"target_digest"`
	Equal        bool   `json:"equal"`
	Note         string `json:"note,omitempty"`
}

// BlobReport compares the blob tree.
type BlobReport struct {
	Rows       int      `json:"rows"`
	Files      int      `json:"files"`
	Copied     int      `json:"copied"`
	Bytes      int64    `json:"bytes"`
	Verified   int      `json:"verified"` // files whose digest equals their name, the row and the size
	Orphans    []string `json:"orphans,omitempty"`
	Missing    []string `json:"missing,omitempty"`
	Corrupt    []string `json:"corrupt,omitempty"`
	TmpIgnored int      `json:"tmp_ignored"`
}

// FTSReport compares the derived full-text index.
type FTSReport struct {
	SourceRows    int `json:"source_rows"`
	RebuiltRows   int `json:"rebuilt_rows"`
	OnlyInSource  int `json:"only_in_source"`
	OnlyInRebuilt int `json:"only_in_rebuilt"`
	BodyDiffers   int `json:"body_differs"`
}

// table pairs an old table with the new one; cols are the columns, pk the order for the digest.
type table struct {
	from, to string
	cols     []string
	pk       string
}

var tables = []table{
	{"blobs", "blobs", []string{"hash", "size", "mime", "source_ref", "created_at"}, "hash"},
	{"name_revs", "name_revs", []string{"ns", "name", "rev", "hash", "title", "tags", "created_at"}, "ns, name, rev"},
	{"action_log", "action_log", []string{"idem_key", "action", "source_ref", "outcome_ref", "created_at"}, "idem_key"},
	{"observations", "observations", []string{"id", "text_hash", "generation", "first_source", "first_at", "last_at", "seen", "candidates", "resolved", "would_show"}, "id"},
	{"detections", "detections", []string{"id", "observation_id", "extractor", "confidence", "ref", "scheme", "state", "verdict", "would_show", "error", "label"}, "id"},
	{"grants", "legacy_grants", []string{"id", "code_hash", "scopes", "label", "created_at", "expires_at", "consumed_at", "revoked_at"}, "id"},
	{"devices", "legacy_devices", []string{"id", "token_hash", "label", "scopes", "grant_id", "created_at", "expires_at", "revoked_at", "last_used_at"}, "id"},
}

// known are the names the old schema can hold: its tables, the FTS5 shadow tables and SQLite's own.
var known = func() map[string]bool {
	m := map[string]bool{"blob_fts": true, "sqlite_sequence": true}
	for _, t := range tables {
		m[t.from] = true
	}
	for _, s := range []string{"data", "idx", "content", "docsize", "config"} {
		m["blob_fts_"+s] = true
	}
	return m
}()

// Run copies and proves. A non-nil error is a failure to do the work; a report with problems is a
// copy that was made but does not prove equal (the caller discards it).
func Run(ctx context.Context, o Options) (Report, error) {
	return run(ctx, o, true)
}

// Verify compares an existing new store with the old directory and writes nothing to either.
func Verify(ctx context.Context, o Options) (Report, error) {
	return run(ctx, o, false)
}

func run(ctx context.Context, o Options, copyData bool) (Report, error) {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	rep := Report{OldDir: o.OldDir, StartedAt: now().UTC().Format(time.RFC3339), Tables: []TableReport{}, Problems: []string{}}
	problem := func(format string, a ...any) { rep.Problems = append(rep.Problems, fmt.Sprintf(format, a...)) }

	before, err := digestFiles(o.OldDir)
	if err != nil {
		return rep, err
	}
	rep.SourceFiles = before
	src, err := openCopy(o)
	if err != nil {
		return rep, err
	}
	defer func() { _ = src.Close(); _ = os.RemoveAll(o.Scratch) }()

	if err = checkSource(ctx, src); err != nil {
		return rep, err
	}
	if copyData {
		for _, t := range tables {
			if err = copyTable(ctx, src, o.DB, t); err != nil {
				return rep, fmt.Errorf("copy %s: %w", t.from, err)
			}
		}
		if err = copyBlobs(ctx, src, o, &rep); err != nil {
			return rep, err
		}
		if err = rebuildFTS(ctx, o); err != nil {
			return rep, err
		}
	} else if err = verifyBlobs(ctx, src, o, &rep); err != nil {
		return rep, err
	}
	for _, t := range tables {
		tr, cmpErr := compareTable(ctx, src, o.DB, t)
		if cmpErr != nil {
			return rep, cmpErr
		}
		rep.Tables = append(rep.Tables, tr)
		if !tr.Equal {
			problem("table %s -> %s: source %d rows (%s), new %d rows (%s)", t.from, t.to, tr.SourceRows, tr.SourceDigest[:12], tr.TargetRows, tr.TargetDigest[:12])
		}
	}
	if err = compareFTS(ctx, src, o.DB, &rep); err != nil {
		return rep, err
	}
	if f := rep.FTS; f.OnlyInSource+f.OnlyInRebuilt+f.BodyDiffers > 0 {
		problem("full-text index: %d only in the source, %d only rebuilt, %d with a different body", f.OnlyInSource, f.OnlyInRebuilt, f.BodyDiffers)
	}
	b := rep.Blobs
	if len(b.Missing)+len(b.Corrupt) > 0 {
		problem("blobs: %d rows without a file, %d files that do not match their digest, row or size", len(b.Missing), len(b.Corrupt))
	}

	after, err := digestFiles(o.OldDir)
	if err != nil {
		return rep, err
	}
	rep.OldUnchanged = slices.Equal(before, after)
	if !rep.OldUnchanged {
		problem("the old database files changed while they were being read: stop the old service and run again")
	}
	rep.FinishedAt = now().UTC().Format(time.RFC3339)
	rep.OK = len(rep.Problems) == 0
	return rep, nil
}

// digestFiles hashes the old database and its write-ahead log (the only files SQLite state lives in;
// the -shm file is a shared-memory index that readers rewrite and is rebuilt from the log).
func digestFiles(oldDir string) ([]FileDigest, error) {
	var out []FileDigest
	for _, name := range []string{"main.db", "main.db-wal"} {
		p := filepath.Join(oldDir, name)
		fi, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) && name != "main.db" {
			continue
		}
		if err != nil {
			return nil, err
		}
		sum, err := hashFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, FileDigest{Name: name, Size: fi.Size(), SHA256: sum, MTime: fi.ModTime().UTC().Format(time.RFC3339Nano)})
	}
	return out, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p) //nolint:gosec // the old data directory the operator named
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// openCopy copies main.db and main.db-wal to the scratch directory, checks the copy equals what was
// read, and opens the copy read-only.
func openCopy(o Options) (*sql.DB, error) {
	if err := os.MkdirAll(o.Scratch, 0o700); err != nil {
		return nil, err
	}
	for _, name := range []string{"main.db", "main.db-wal"} {
		from := filepath.Join(o.OldDir, name)
		if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) && name != "main.db" {
			continue
		}
		want, err := hashFile(from)
		if err != nil {
			return nil, err
		}
		to := filepath.Join(o.Scratch, name)
		if err = copyFile(from, to); err != nil {
			return nil, err
		}
		got, err := hashFile(to)
		if err != nil {
			return nil, err
		}
		if got != want {
			return nil, fmt.Errorf("%s changed while it was being copied: stop the old service and run again", name)
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(o.Scratch, "main.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from) //nolint:gosec // the old data directory the operator named
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // a path in the scratch directory
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// checkSource refuses a source that is damaged or holds anything the migration does not cover.
func checkSource(ctx context.Context, src *sql.DB) error {
	var res string
	if err := src.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&res); err != nil {
		return fmt.Errorf("the old database fails its integrity check: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("the old database fails its integrity check: %s", res)
	}
	rows, err := src.QueryContext(ctx, `SELECT type, name, tbl_name FROM sqlite_master`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var extra []string
	for rows.Next() {
		var typ, name, tbl string
		if err := rows.Scan(&typ, &name, &tbl); err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(name, "sqlite_autoindex_"):
		case typ == "table" && known[name]:
		case typ == "index" && known[tbl]:
		default:
			extra = append(extra, typ+" "+name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("the old database holds objects this migration does not cover: %s", strings.Join(extra, ", "))
	}
	for _, t := range tables {
		have, ok, err := columns(ctx, src, t.from)
		if err != nil {
			return err
		}
		if !ok {
			continue // grants and devices arrived later: an older database may not have them
		}
		if !slices.Equal(have, t.cols) {
			return fmt.Errorf("the old table %s has columns %v, the migration expects %v", t.from, have, t.cols)
		}
	}
	return nil
}

func columns(ctx context.Context, db *sql.DB, name string) (cols []string, exists bool, err error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, name)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return nil, false, err
		}
		cols = append(cols, c)
	}
	return cols, len(cols) > 0, rows.Err()
}

// copyTable copies every row, with its own id where the table has one, in one transaction.
func copyTable(ctx context.Context, src *sql.DB, dst *store.Store, t table) error {
	if _, ok, err := columns(ctx, src, t.from); err != nil || !ok {
		return err
	}
	rows, err := src.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s ORDER BY %s`, strings.Join(t.cols, ", "), t.from, t.pk))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	ins := fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, t.to, strings.Join(t.cols, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(t.cols)), ", "))
	return dst.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		for rows.Next() {
			vals := make([]any, len(t.cols))
			ptrs := make([]any, len(t.cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, ins, vals...); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

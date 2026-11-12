// Package legacy is the one-way migration from the pre-Substrate Conduit data into the new layout.
//
// It reads the old database and the old output blobs and never writes them (the old database is opened read-only and
// immutable, and refused when it has an unmerged write-ahead log: migrate a copy made with the SQLite online backup).
// It fills an empty new database in one transaction, then proves the copy equal.
//
// The rule that keeps old rows harmless: every migrated run carries legacy=1, and no code path changes the state of,
// starts, cancels, reaps, expires or signals a legacy run, whatever its state, owner_pid or pgid say. A run that was
// pending, approved or running in the old database stays exactly that in the record and is history only.
package legacy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	cstore "github.com/hollis-labs/libs/core/store"

	"github.com/hollis-labs/tools/conduit/internal/store"
)

const refPrefix = "cas://sha256/"

// Options say what to migrate.
type Options struct {
	// From is the old database file (a copy).
	From string
	// BlobsFrom is the old output store directory (<xx>/<sha256> files). Empty when no local blob is referenced.
	BlobsFrom string
	// BlobsTo is the new output store directory.
	BlobsTo string
	// AllowMissingBlobs accepts references whose blob is not in BlobsFrom (an old install that kept output in caster).
	AllowMissingBlobs bool
	// Now stamps retired_at.
	Now time.Time
}

// Report is what the migration did and what the proof found.
type Report struct {
	Tables     []TableReport
	Blobs      int // distinct local blobs copied
	BlobsMiss  int // referenced and absent from BlobsFrom (only with AllowMissingBlobs)
	NonTermRun int // migrated runs that were pending, approved or running: inert history
	Integrity  string
}

// TableReport is one table's before/after.
type TableReport struct {
	Table      string
	Source     int
	Target     int
	Mismatches int
}

func (r Report) String() string {
	var b strings.Builder
	for _, t := range r.Tables {
		fmt.Fprintf(&b, "%-15s source %-5d target %-5d mismatches %d\n", t.Table, t.Source, t.Target, t.Mismatches)
	}
	fmt.Fprintf(&b, "blobs           copied %d, missing %d\n", r.Blobs, r.BlobsMiss)
	fmt.Fprintf(&b, "inert runs      %d (pending, approved or running in the old database; legacy=1, never acted on)\n", r.NonTermRun)
	fmt.Fprintf(&b, "integrity       %s\n", r.Integrity)
	return b.String()
}

// table pairs an old table with its new one.
type table struct{ old, new, key string }

var tables = []table{
	{"runs", "runs", "id"},
	{"attempts", "attempts", "run_id, n"},
	{"panics", "panics", "ref"},
	{"tokens", "retired_tokens", "id"},
}

// Migrate copies everything into dst, which must be empty, and proves the copy. It returns the report; an error means
// nothing was committed (the copy is one transaction) or, for a failed proof, that the target must not be used.
func Migrate(ctx context.Context, dst *store.Store, o Options) (Report, error) {
	var rep Report
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	src, err := openSource(o.From)
	if err != nil {
		return rep, err
	}
	defer func() { _ = src.Close() }()

	// Integrity of the source, before and independent of the copy.
	var integ string
	if ierr := src.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integ); ierr != nil {
		return rep, fmt.Errorf("integrity check of the old database: %w", ierr)
	}
	if integ != "ok" {
		return rep, fmt.Errorf("the old database fails its integrity check: %s", integ)
	}
	rep.Integrity = "old database ok"

	// The target must be empty: this migration never merges.
	for _, t := range tables {
		var n int
		if cerr := dst.Framework().Reader().QueryRowContext(ctx, `SELECT count(*) FROM `+t.new).Scan(&n); cerr != nil {
			return rep, cerr
		}
		if n != 0 {
			return rep, fmt.Errorf("the new database already holds %d row(s) in %s: migrate-legacy only fills an empty database", n, t.new)
		}
	}

	// Read everything first, then write in one transaction.
	data := map[string]*rowSet{}
	for _, t := range tables {
		rs, rerr := readAll(ctx, src, t.old)
		if rerr != nil {
			return rep, rerr
		}
		data[t.old] = rs
	}
	blobs, err := planBlobs(data["attempts"], o)
	if err != nil {
		return rep, err
	}
	for _, r := range data["runs"].rows {
		switch r[data["runs"].col("state")] {
		case store.StatePending, store.StateApproved, store.StateRunning:
			rep.NonTermRun++
		}
	}

	err = dst.Framework().WriteTx(ctx, func(ctx context.Context, tx cstore.Tx) error {
		for _, t := range tables {
			rs := data[t.old]
			cols := append([]string(nil), rs.cols...)
			extra := map[string]any{}
			switch t.old {
			case "runs":
				extra["legacy"] = 1
			case "tokens":
				extra["retired_at"] = o.Now.UTC().Format(time.RFC3339Nano)
			}
			names := append([]string(nil), cols...)
			keys := make([]string, 0, len(extra))
			for k := range extra {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			names = append(names, keys...)
			q := `INSERT INTO ` + t.new + `(` + strings.Join(names, ",") + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(names)), ",") + `)`
			for _, row := range rs.rows {
				args := append([]any(nil), row...)
				for _, k := range keys {
					args = append(args, extra[k])
				}
				if _, xerr := tx.ExecContext(ctx, q, args...); xerr != nil {
					return fmt.Errorf("insert into %s: %w", t.new, xerr)
				}
			}
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	if berr := copyBlobs(blobs, o); berr != nil {
		return rep, fmt.Errorf("copy blobs: %w (the database copy was committed; remove the new data directory and retry)", berr)
	}
	rep.Blobs = len(blobs.have)
	rep.BlobsMiss = len(blobs.missing)

	// The proof: row for row, every old column equal, plus the blobs by hash.
	for _, t := range tables {
		tr := TableReport{Table: t.new, Source: len(data[t.old].rows)}
		dstRS, qerr := readAllCols(ctx, dst.Framework().Reader(), t.new, data[t.old].cols, t.key)
		if qerr != nil {
			return rep, qerr
		}
		tr.Target = len(dstRS.rows)
		tr.Mismatches = diff(data[t.old], dstRS, t.key)
		rep.Tables = append(rep.Tables, tr)
	}
	var legacyCount int
	if lerr := dst.Framework().Reader().QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE legacy=1`).Scan(&legacyCount); lerr != nil {
		return rep, lerr
	}
	if legacyCount != len(data["runs"].rows) {
		return rep, fmt.Errorf("proof failed: %d of %d migrated runs carry legacy=1", legacyCount, len(data["runs"].rows))
	}
	var fk int
	rows, err := dst.Framework().Reader().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		fk++
	}
	if err := rows.Close(); err != nil {
		return rep, err
	}
	if fk != 0 {
		return rep, fmt.Errorf("proof failed: %d foreign key violation(s) in the new database", fk)
	}
	rep.Integrity += "; new database foreign keys ok"
	for _, t := range rep.Tables {
		if t.Source != t.Target || t.Mismatches != 0 {
			return rep, fmt.Errorf("proof failed for %s: source %d, target %d, %d mismatching row(s)", t.Table, t.Source, t.Target, t.Mismatches)
		}
	}
	if err := verifyBlobs(blobs, o); err != nil {
		return rep, fmt.Errorf("proof failed: %w", err)
	}
	return rep, nil
}

// openSource opens the old database read-only and immutable. It refuses a database with an unmerged WAL (an immutable
// open would ignore it and lose its rows) and one that does not exist.
func openSource(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("--from is required")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("old database: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("old database %s is not a regular file", path)
	}
	if w, werr := os.Stat(path + "-wal"); werr == nil && w.Size() > 0 {
		return nil, fmt.Errorf("%s has an unmerged write-ahead log (%s-wal, %d bytes): migrate a copy made with the SQLite online backup, which has none", path, path, w.Size())
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+abs+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

type rowSet struct {
	cols []string
	rows [][]any
}

func (r *rowSet) col(name string) int {
	for i, c := range r.cols {
		if c == name {
			return i
		}
	}
	return -1
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readAll reads every row of a source table with every column it has, ordered by rowid.
func readAll(ctx context.Context, q querier, name string) (*rowSet, error) {
	rows, err := q.QueryContext(ctx, `SELECT * FROM `+name+` ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("read old %s: %w", name, err)
	}
	return collect(rows)
}

// readAllCols reads the named columns of a target table in key order.
func readAllCols(ctx context.Context, q querier, name string, cols []string, key string) (*rowSet, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+strings.Join(cols, ",")+` FROM `+name+` ORDER BY `+key)
	if err != nil {
		return nil, fmt.Errorf("read new %s: %w", name, err)
	}
	return collect(rows)
}

func collect(rows *sql.Rows) (*rowSet, error) {
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	rs := &rowSet{cols: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rs.rows = append(rs.rows, vals)
	}
	return rs, rows.Err()
}

// diff counts source rows that are not equal, column for column, to the target row with the same key, plus target rows
// the source does not have.
func diff(srcRS, dstRS *rowSet, key string) int {
	idx := func(rs *rowSet) func(row []any) string {
		var pos []int
		for _, k := range strings.Split(key, ",") {
			pos = append(pos, rs.col(strings.TrimSpace(k)))
		}
		return func(row []any) string {
			var parts []string
			for _, p := range pos {
				parts = append(parts, fmt.Sprint(row[p]))
			}
			return strings.Join(parts, "\x00")
		}
	}
	srcKey, dstKey := idx(srcRS), idx(dstRS)
	dst := map[string][]any{}
	for _, r := range dstRS.rows {
		dst[dstKey(r)] = r
	}
	bad := 0
	for _, r := range srcRS.rows {
		got, ok := dst[srcKey(r)]
		if !ok || !reflect.DeepEqual(r, got) {
			bad++
		}
		delete(dst, srcKey(r))
	}
	return bad + len(dst)
}

type blobPlan struct {
	have    map[string]bool // hash -> present in BlobsFrom
	missing map[string]bool
}

// hashOf returns the hash of a local blob reference. It must be exactly 64 lower-case hex digits: the hash becomes a
// path, so nothing else (a separator, "..") may pass.
func hashOf(ref string) (string, bool) {
	h, ok := strings.CutPrefix(ref, refPrefix)
	if !ok || len(h) != 64 {
		return "", false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return h, true
}

// planBlobs finds every referenced local blob and checks it exists in the old store and matches its hash.
func planBlobs(attempts *rowSet, o Options) (*blobPlan, error) {
	p := &blobPlan{have: map[string]bool{}, missing: map[string]bool{}}
	for _, col := range []string{"stdout_ref", "stderr_ref"} {
		i := attempts.col(col)
		if i < 0 {
			continue
		}
		for _, row := range attempts.rows {
			ref, _ := row[i].(string)
			if ref == "" {
				continue
			}
			h, ok := hashOf(ref)
			if !ok {
				return nil, fmt.Errorf("an attempt references %q, which is not a local blob reference", ref)
			}
			if p.have[h] || p.missing[h] {
				continue
			}
			b, err := os.ReadFile(oldBlobPath(o.BlobsFrom, h))
			switch {
			case err == nil:
				sum := sha256.Sum256(b)
				if hex.EncodeToString(sum[:]) != h {
					return nil, fmt.Errorf("old blob %s fails its hash check", h)
				}
				p.have[h] = true
			case errors.Is(err, fs.ErrNotExist) || o.BlobsFrom == "":
				if !o.AllowMissingBlobs {
					return nil, fmt.Errorf("blob %s is referenced but not in --blobs-from %q (pass --allow-missing-blobs only if the old install kept output elsewhere)", h, o.BlobsFrom)
				}
				p.missing[h] = true
			default:
				return nil, err
			}
		}
	}
	return p, nil
}

func oldBlobPath(dir, h string) string { return filepath.Join(dir, h[:2], h) }

func copyBlobs(p *blobPlan, o Options) error {
	for h := range p.have {
		b, err := os.ReadFile(oldBlobPath(o.BlobsFrom, h))
		if err != nil {
			return err
		}
		dst := oldBlobPath(o.BlobsTo, h)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil { //nolint:gosec // dst is <dir>/<xx>/<64 hex digits>: hashOf admits nothing else
			return err
		}
	}
	return nil
}

// verifyBlobs re-reads every copied blob from the new store and checks its hash.
func verifyBlobs(p *blobPlan, o Options) error {
	for h := range p.have {
		b, err := os.ReadFile(oldBlobPath(o.BlobsTo, h))
		if err != nil {
			return fmt.Errorf("blob %s is not in the new store: %w", h, err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != h {
			return fmt.Errorf("blob %s differs in the new store", h)
		}
	}
	return nil
}

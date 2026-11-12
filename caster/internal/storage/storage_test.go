package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
)

var ctx = context.Background()

// openAt opens the database at dbPath the way the database module does (store.Open, then
// migrate.Ensure with the embedded migrations) and returns a Storage over it.
func openAt(t *testing.T, dbPath string) *Storage {
	t.Helper()
	cfg := store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: dbPath}}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fsys, root := Migrations()
	if err = migrate.Ensure(ctx, cfg, db, migrate.Source{FS: fsys, Root: root}); err != nil {
		t.Fatal(err)
	}
	return New(db, RootFor(dbPath))
}

func open(t *testing.T) *Storage { return openAt(t, filepath.Join(t.TempDir(), "caster.db")) }

func put(t *testing.T, s *Storage, body string) Blob {
	t.Helper()
	b, err := s.PutBlob(ctx, strings.NewReader(body), "", "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPutDedupsAndVerifies(t *testing.T) {
	s := open(t)
	a := put(t, s, "same bytes")
	b := put(t, s, "same bytes")
	if a.Hash != b.Hash {
		t.Fatalf("dedup failed: %v %v", a.Hash, b.Hash)
	}
	if _, _, err := s.OpenBlob(ctx, a.Hash); err != nil {
		t.Fatal(err)
	}
	// Corrupt the blob on disk: reads must refuse it.
	p, _ := s.BlobPath(a.Hash)
	_ = os.Chmod(p, 0o600)
	if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenBlob(ctx, a.Hash); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("want integrity failure, got %v", err)
	}
}

func TestBinaryBlobIsNotIndexed(t *testing.T) {
	s := open(t)
	b, err := s.PutBlob(ctx, strings.NewReader("\x00\x01 walrus"), "", "")
	if err != nil || b.Mime != "application/octet-stream" {
		t.Fatalf("blob %+v err %v", b, err)
	}
	if hits, err := s.Search(ctx, "walrus", 5); err != nil || len(hits) != 0 {
		t.Fatalf("binary blob must not be indexed: %+v %v", hits, err)
	}
}

func TestNameRevisions(t *testing.T) {
	s := open(t)
	h1, h2 := put(t, s, "one"), put(t, s, "two")
	n1, err := s.Link(ctx, "n", "x", h1.Hash, "T", "a b")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.Link(ctx, "n", "x", h1.Hash, "", "")
	if again.Rev != n1.Rev {
		t.Fatalf("relinking the same hash must not add a revision: %d vs %d", again.Rev, n1.Rev)
	}
	n2, _ := s.Link(ctx, "n", "x", h2.Hash, "", "")
	if n2.Rev != 2 || n2.Title != "T" || n2.Tags != "a b" {
		t.Fatalf("rev 2 should inherit title/tags: %+v", n2)
	}
	if old, _ := s.Lookup(ctx, "n", "x", 1); old.Hash != h1.Hash {
		t.Fatal("pinned revision must keep resolving to the old hash")
	}
	if _, err := s.Lookup(ctx, "n", "missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if hist, _ := s.History(ctx, "n", "x"); len(hist) != 2 || hist[0].Rev != 2 {
		t.Fatalf("history newest first: %+v", hist)
	}
	if l, _ := s.List(ctx, "n", "b"); len(l) != 1 || l[0].Rev != 2 {
		t.Fatalf("list by tag: %+v", l)
	}
}

func TestSearchFindsCurrentNames(t *testing.T) {
	s := open(t)
	h := put(t, s, "a note about walruses")
	if _, err := s.Link(ctx, "n", "w", h.Hash, "", ""); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, "walruses", 5)
	if err != nil || len(hits) != 1 || len(hits[0].Names) != 1 || hits[0].Names[0] != "n/w" || !strings.Contains(hits[0].Snippet, "[walruses]") {
		t.Fatalf("unexpected hits %+v err %v", hits, err)
	}
	if _, err = s.Search(ctx, `"unbalanced`, 5); err == nil {
		t.Fatal("a malformed FTS5 query is an error, as before the port")
	}
}

func TestActionRecordFirstWins(t *testing.T) {
	s := open(t)
	first, err := s.RecordAction(ctx, ActionRecord{Key: "k", Action: "a", Source: "s", Outcome: "o1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordAction(ctx, ActionRecord{Key: "k", Action: "a", Source: "s", Outcome: "o2"})
	if err != nil || second.Outcome != first.Outcome || first.At.IsZero() {
		t.Fatalf("first record must win: %+v %+v %v", first, second, err)
	}
	if _, err = s.LookupAction(ctx, "none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestShadowRoundTrip(t *testing.T) {
	s := open(t)
	dets := []Detection{
		{Extractor: "bare", Confidence: "high", Ref: "torque://task/X", Scheme: "torque", State: "resolved", Verdict: "show", WouldShow: true},
		{Extractor: "bare", Confidence: "high", Ref: "torque://task/Y", Scheme: "torque", State: "not_found", Verdict: "hide"},
	}
	o, err := s.RecordObservation(ctx, "h1", 1, "src", dets)
	if err != nil || o.Candidates != 2 || o.Resolved != 1 || o.Shown != 1 || o.ID == 0 {
		t.Fatalf("observation %+v err %v", o, err)
	}
	again, err := s.FindObservation(ctx, "h1", 1)
	if err != nil || again.Seen != 2 || again.ID != o.ID {
		t.Fatalf("find %+v err %v", again, err)
	}
	if _, err = s.FindObservation(ctx, "other", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	n, err := s.LabelRef(ctx, "torque://task/X", "true")
	if err != nil || n != 1 {
		t.Fatalf("label %d %v", n, err)
	}
	tot, stats, top, err := s.ShadowReport(ctx, time.Time{}, 5)
	if err != nil || tot.Observations != 1 || tot.Repeats != 1 || len(stats) != 1 || stats[0].Candidates != 2 || stats[0].LabeledTrue != 1 || len(top) != 1 || top[0].Ref != "torque://task/Y" {
		t.Fatalf("report %+v %+v %+v %v", tot, stats, top, err)
	}
}

// Two handles on one file stand for two processes (the service and a local CLI or MCP child):
// each has its own pools, and they meet only in SQLite's file lock. Linking the same name from
// both must produce consecutive revisions with no conflict, and every blob must be readable.
func TestTwoHandlesOneFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "caster.db")
	a := openAt(t, dbPath)
	b := openAt(t, dbPath)
	const each = 15
	var wg sync.WaitGroup
	errs := make(chan error, 2*each)
	for who, s := range []*Storage{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				blob, err := s.PutBlob(ctx, strings.NewReader(fmt.Sprintf("writer %d note %d", who, i)), "", "")
				if err != nil {
					errs <- err
					continue
				}
				if _, err = s.Link(ctx, "shared", "pad", blob.Hash, "", ""); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	hist, err := a.History(ctx, "shared", "pad")
	if err != nil || len(hist) != 2*each || hist[0].Rev != 2*each {
		t.Fatalf("want %d consecutive revisions, got %d (err %v)", 2*each, len(hist), err)
	}
	for _, n := range hist {
		if _, _, err := b.OpenBlob(ctx, n.Hash); err != nil {
			t.Fatalf("rev %d: %v", n.Rev, err)
		}
	}
}

// A value that is not a digest never reaches the file system: no panic, no path outside the tree.
func TestBlobHashIsValidatedAtTheBoundary(t *testing.T) {
	s := openAt(t, filepath.Join(t.TempDir(), "caster.db"))
	good := strings.Repeat("ab", 32)
	if p, err := s.BlobPath(good); err != nil || !strings.HasPrefix(p, s.root) {
		t.Fatalf("a digest must have a path under the tree: %q %v", p, err)
	}
	for _, bad := range []string{"", "a", "ab", strings.Repeat("a", 63), strings.Repeat("a", 65),
		strings.Repeat("AB", 32), strings.Repeat("g", 64), "../../etc/passwd", "../" + strings.Repeat("a", 61),
		strings.Repeat("a", 2) + "/" + strings.Repeat("b", 61), strings.Repeat("a", 63) + "\x00"} {
		if p, err := s.BlobPath(bad); !errors.Is(err, ErrNotFound) || p != "" {
			t.Fatalf("BlobPath(%q) = %q, %v: want ErrNotFound", bad, p, err)
		}
		if _, err := s.HeadBlob(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Fatalf("HeadBlob(%q): %v", bad, err)
		}
		if _, _, err := s.OpenBlob(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Fatalf("OpenBlob(%q): %v", bad, err)
		}
	}
}

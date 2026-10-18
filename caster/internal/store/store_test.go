package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutDedupsAndVerifies(t *testing.T) {
	s := open(t)
	a, err := s.PutBlob(strings.NewReader("same bytes"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PutBlob(strings.NewReader("same bytes"), "", "")
	if err != nil || a.Hash != b.Hash {
		t.Fatalf("dedup failed: %v %v %v", a.Hash, b.Hash, err)
	}
	if _, _, err := s.OpenBlob(a.Hash); err != nil {
		t.Fatal(err)
	}
	// Corrupt the blob on disk: reads must refuse it.
	p := s.blobPath(a.Hash)
	_ = os.Chmod(p, 0o600)
	if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenBlob(a.Hash); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("want integrity failure, got %v", err)
	}
}

func TestNameRevisions(t *testing.T) {
	s := open(t)
	h1, _ := s.PutBlob(strings.NewReader("one"), "", "")
	h2, _ := s.PutBlob(strings.NewReader("two"), "", "")
	n1, _ := s.Link("n", "x", h1.Hash, "T", "a b")
	again, _ := s.Link("n", "x", h1.Hash, "", "")
	if again.Rev != n1.Rev {
		t.Fatalf("relinking the same hash must not add a revision: %d vs %d", again.Rev, n1.Rev)
	}
	n2, _ := s.Link("n", "x", h2.Hash, "", "")
	if n2.Rev != 2 || n2.Title != "T" || n2.Tags != "a b" {
		t.Fatalf("rev 2 should inherit title/tags: %+v", n2)
	}
	if old, _ := s.Lookup("n", "x", 1); old.Hash != h1.Hash {
		t.Fatal("pinned revision must keep resolving to the old hash")
	}
	if _, err := s.Lookup("n", "missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSearchFindsCurrentNames(t *testing.T) {
	s := open(t)
	h, _ := s.PutBlob(strings.NewReader("a note about walruses"), "", "")
	_, _ = s.Link("n", "w", h.Hash, "", "")
	hits, err := s.Search("walruses", 5)
	if err != nil || len(hits) != 1 || len(hits[0].Names) != 1 || hits[0].Names[0] != "n/w" {
		t.Fatalf("unexpected hits %+v err %v", hits, err)
	}
}

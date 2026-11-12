// Package storage is Caster's storage on Substrate's database module: immutable blobs addressed
// by sha256 on disk (the content-addressed store, CAS), and a SQLite index holding blob metadata,
// named pointers with revision history, the action log, the shadow-mode records and full-text
// search over text blobs. Mutable things never live in the blob tree; a name is a pointer to a
// hash at a revision. Caster is SQLite only (FTS5, SQLite syntax).
//
// Writes go through WriteTx and reads through the read pool, as the store contract in
// docs/adopting-substrate.md requires; blob files are written before the transaction that records
// them, so a failed transaction leaves at most an unreferenced file (content-addressed, so a retry
// reuses it).
package storage

import (
	"embed"
	"errors"
	"path/filepath"
	"time"

	"github.com/hollis-labs/libs/core/store"
)

//go:embed migrations
var migrations embed.FS

// Migrations is the embedded migration tree: root "migrations" holds one directory per dialect.
// Register it with database.UseMigrationFS before boot.
func Migrations() (fsys embed.FS, root string) { return migrations, "migrations" }

// ErrNotFound is returned for any missing blob, name or revision.
var ErrNotFound = errors.New("not found")

// MaxIndexBytes bounds the text that is full-text indexed per blob.
const MaxIndexBytes = 1 << 20

// CASDir is the blob tree's directory name beside the database file. The pre-Substrate tree
// (blobs/) is left alone: `caster migrate` copies it here and proves the copy.
const CASDir = "cas"

// Storage is the handle over the database module's store and the blob tree.
type Storage struct {
	db   *store.Store
	root string
}

// New returns a Storage over db with blobs under root. It does not own db: the database module
// closes it.
func New(db *store.Store, root string) *Storage { return &Storage{db: db, root: root} }

// RootFor is the blob tree beside the database file at dbPath.
func RootFor(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), CASDir) }

// Root returns the blob tree root.
func (s *Storage) Root() string { return s.root }

// DB returns the store, for the migration and the tests.
func (s *Storage) DB() *store.Store { return s.db }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }

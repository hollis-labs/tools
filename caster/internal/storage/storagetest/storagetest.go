// Package storagetest opens a throwaway Storage the way the database module does, for the tests of
// the packages that sit on top of it.
package storagetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/tools/caster/internal/storage"
)

// Open returns a migrated Storage over a fresh database and blob tree in t's temp directory. It is
// closed when the test ends.
func Open(t testing.TB) *storage.Storage {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "caster.db")
	cfg := store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: dbPath}}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fsys, root := storage.Migrations()
	if err = migrate.Ensure(ctx, cfg, db, migrate.Source{FS: fsys, Root: root}); err != nil {
		t.Fatal(err)
	}
	return storage.New(db, storage.RootFor(dbPath))
}

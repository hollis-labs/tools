// Package persistence owns Conduit's durable schema: one goose migration, embedded here and applied by Substrate's
// database module. Conduit is SQLite only.
package persistence

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver the guard uses
)

//go:embed migrations
var migrations embed.FS

// Migrations is the embedded migration tree: Root "migrations" holds one directory per dialect (here only sqlite).
func Migrations() (fsys embed.FS, root string) { return migrations, "migrations" }

// ErrLegacyLayout means the data directory holds the layout used before Conduit moved onto Substrate. It is never
// opened in place: `conduit migrate-legacy` copies it into a new file.
var ErrLegacyLayout = errors.New("the data directory uses the pre-Substrate Conduit layout")

// LegacyDatabase is the old database path relative to the data directory.
var LegacyDatabase = filepath.Join("workspaces", "default", "main.db")

// Open says what a command may do about the database file.
type Open int

const (
	// ReadExisting: the file must already exist in the new layout; nothing is ever created.
	ReadExisting Open = iota
	// Create: the file may be created, unless the directory holds only the old database (migrate first).
	Create
	// CreateForMigration: the file may be created even beside the old database (`migrate-legacy` fills it).
	CreateForMigration
)

// Check is run before the database module opens path, with the data directory that holds it. It never changes
// anything: it returns an error telling the operator what to do instead of letting the module create or migrate a file.
func Check(dataDir, path string, how Open) error {
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		return refuseOldSchema(path)
	}
	old := filepath.Join(dataDir, LegacyDatabase)
	_, oldErr := os.Stat(old)
	switch {
	case oldErr == nil && how != CreateForMigration:
		return fmt.Errorf("%w (%s): there is no %s beside it. Nothing was created. Run `conduit migrate-legacy --from <copy of the old database>` first; see docs/cutover-plan.md",
			ErrLegacyLayout, old, filepath.Base(path))
	case how == ReadExisting:
		return fmt.Errorf("there is no database at %s. Nothing was created. `conduit serve` (or `conduit migrate-legacy`) creates it", path)
	}
	return nil
}

// refuseOldSchema returns ErrLegacyLayout when the file at path has the old tables and no goose version table. It only
// reads, and an empty file is fine.
func refuseOldSchema(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var runs, goose int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='runs'").Scan(&runs); err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&goose); err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if runs > 0 && goose == 0 {
		return fmt.Errorf("%w (%s): this file is not opened in place; point database.sqlite.path at a new file and run `conduit migrate-legacy --from %s`. See docs/cutover-plan.md",
			ErrLegacyLayout, path, path)
	}
	return nil
}

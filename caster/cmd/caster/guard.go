package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/hollis-labs/substrate/module"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
)

// guardLegacy refuses to start on a data directory that still has the pre-Substrate database
// (main.db) and no Substrate one yet: the database module would create an empty caster.db beside
// it and the service would come up looking empty. It runs before the database module opens the
// file (boot.Options.Before["database"]) and reads the path from app.Config, which already carries
// the SUBSTRATE_CONFIG override. It only looks at the files; it never opens or writes main.db.
func guardLegacy(app *module.App, layout paths.Layout) error {
	target := databasePath(app)
	if target == "" {
		return errors.New("no database path configured")
	}
	old := layout.MainDB()
	if _, err := os.Stat(target); err != nil {
		if _, oldErr := os.Stat(old); oldErr == nil {
			return fmt.Errorf("found the pre-Substrate database %s and no %s: run `caster migrate` to copy the data (the old files are not changed), or move the old data directory aside to start empty", old, target)
		}
	}
	return secureDatabase(target) // already migrated, or created by this version
}

// secureDatabase makes the database file private: a missing one is created empty with mode 0600 (SQLite
// opens an empty file as a new database, and gives its -wal and -shm files the same mode), an existing
// one, and any -wal or -shm beside it, is tightened to 0600. It runs before the database module opens the
// file, so the module never creates it with the process umask.
func secureDatabase(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the configured database path
	switch {
	case err == nil:
		if err = f.Close(); err != nil {
			return err
		}
	case !errors.Is(err, os.ErrExist):
		return err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, statErr := os.Stat(p)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if fi.Mode().Perm()&0o077 != 0 {
			if err = os.Chmod(p, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

// databasePath is the sqlite path the database module is about to open.
func databasePath(app *module.App) string {
	db, _ := app.Config["database"].(map[string]any)
	sq, _ := db["sqlite"].(map[string]any)
	path, _ := sq["path"].(string)
	return path
}

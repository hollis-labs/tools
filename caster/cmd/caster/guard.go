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
	if _, err := os.Stat(target); err == nil {
		return nil // already migrated, or created by this version
	}
	if _, err := os.Stat(old); err == nil {
		return fmt.Errorf("found the pre-Substrate database %s and no %s: run `caster migrate` to copy the data (the old files are not changed), or move the old data directory aside to start empty", old, target)
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

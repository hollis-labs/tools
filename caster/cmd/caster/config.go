package main

import (
	"path/filepath"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
	"github.com/hollis-labs/substrate/module"
)

// appName is the apppaths identity: it names the XDG directories and the CASTER_* variables
// paths.Resolve reads (CASTER_DB_PATH, CASTER_WORKSPACE), the data-directory override the guide allows.
const appName = "caster"

// resolvePaths writes the absolute locations of the files that live in the data directory into
// app.Config, after the SUBSTRATE_CONFIG override is applied. A path the operator's override
// already made absolute is left alone; a relative one is placed in the data directory.
//
// The Substrate-era database is caster.db beside the old main.db, which stays untouched as the
// rollback (see `caster migrate`).
func resolvePaths(app *module.App, layout paths.Layout) {
	dbDir := filepath.Dir(layout.MainDB())
	place(app, dbDir, "database", "sqlite", "path")
	place(app, layout.DataDir(), "identity", "key_file")
	place(app, layout.DataDir(), "secrets", "state_dir")
}

// place makes the config value at path absolute under dir, unless it already is.
func place(app *module.App, dir string, path ...string) {
	m := configMap(app, path[:len(path)-1])
	key := path[len(path)-1]
	if p, _ := m[key].(string); !filepath.IsAbs(p) {
		m[key] = filepath.Join(dir, p)
	}
}

// configMap returns the nested map at path, creating the maps on the way.
func configMap(app *module.App, path []string) map[string]any {
	m := app.Config
	for _, k := range path {
		child, _ := m[k].(map[string]any)
		if child == nil {
			child = map[string]any{}
			m[k] = child
		}
		m = child
	}
	return m
}

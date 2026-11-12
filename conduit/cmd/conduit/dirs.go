package main

import (
	"os"

	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/settings"
)

// allowRoots resolves the directories a caller's working directory must sit under: conduit.allow_dirs, or the working
// directory the process was launched in.
func allowRoots(set settings.Settings) ([]string, error) {
	dirs := set.AllowDirs
	if len(dirs) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dirs = []string{wd}
	}
	return service.ResolveRoots(dirs)
}

// Package appdirs decides where Conduit keeps its files. Durable data (the database, the identity signing key, the
// output store) lives under the data directory: XDG data home through the shared apppaths layer, or CONDUIT_DATA_DIR
// when the operator sets it. Resolving creates nothing.
package appdirs

import (
	"fmt"
	"os"
	"path/filepath"

	paths "github.com/hollis-labs/libs/core/util/apppaths"
)

const (
	appName = "conduit"
	// DataDirEnv overrides the whole data directory.
	DataDirEnv = "CONDUIT_DATA_DIR"

	// DatabaseFile, SigningKeyFile, SecretsDir and BlobsDir are the names inside the data directory.
	DatabaseFile   = "conduit.db"
	SigningKeyFile = "identity-signing.key"
	SecretsDir     = "secrets"
	BlobsDir       = "blobs"
)

// Dirs are the resolved directories.
type Dirs struct {
	Data   string
	Config string
}

// Resolve returns the directories. getenv is os.Getenv outside tests. Nothing is created.
func Resolve(getenv func(string) string) (Dirs, error) {
	layout, err := paths.Resolve(appName, paths.WithoutMaterialize())
	if err != nil {
		return Dirs{}, fmt.Errorf("resolve application directories: %w", err)
	}
	d := Dirs{Data: layout.DataDir(), Config: layout.ConfigDir()}
	if v := getenv(DataDirEnv); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return Dirs{}, fmt.Errorf("resolve %s: %w", DataDirEnv, err)
		}
		d.Data = abs
	}
	return d, nil
}

// Database is the SQLite file.
func (d Dirs) Database() string { return filepath.Join(d.Data, DatabaseFile) }

// Prepare creates the data directory (0700) and the database file (0600) when missing and tightens both when they
// exist. The signing key and the secrets store create themselves private.
func (d Dirs) Prepare() error {
	if err := os.MkdirAll(d.Data, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(d.Data, 0o700); err != nil { //nolint:gosec // 0700 on a directory is the point: owner-only
		return err
	}
	f, err := os.OpenFile(d.Database(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

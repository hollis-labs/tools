package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/tools/conduit/internal/appdirs"
	"github.com/hollis-labs/tools/conduit/internal/legacy"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

func migrateLegacyCmd(ctx context.Context, app *module.App, dirs appdirs.Dirs) *cobra.Command {
	var from, blobsFrom string
	var allowMissing bool
	c := &cobra.Command{
		Use:   "migrate-legacy --from <old main.db copy> [--blobs-from <old blobs dir>]",
		Short: "Copy the pre-Substrate database and output blobs into this installation and prove the copy",
		Long: `Copies every run, attempt, panic record and API token row of a pre-Substrate Conduit database, and the output
blobs they reference, into a new, empty database in the data directory, then proves the copy equal row for row and blob
by blob. One way: the old files are opened read-only and immutable and are never written, and the command refuses a
non-empty target. Run it on a copy of the old database made with the SQLite online backup, never on the live file.

Migrated runs are history only (legacy=1): none is ever approved, started, canceled, reaped or signaled, whatever its
state, owner_pid or pgid say. The old API tokens land in retired_tokens, which no authentication path reads; create
credentials with "conduit identity key create".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cs := database.From(app)
			if cs == nil {
				return errors.New("the database module must be started")
			}
			dbPath, _ := dbPathOf(app)
			st := store.New(cs, dbPath)
			rep, err := legacy.Migrate(ctx, st, legacy.Options{From: from, BlobsFrom: blobsFrom, BlobsTo: filepath.Join(dirs.Data, appdirs.BlobsDir), AllowMissingBlobs: allowMissing})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), rep.String())
			fmt.Fprintln(cmd.OutOrStdout(), "migration proven equal; the old files were not written")
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "the old database file (a copy)")
	c.Flags().StringVar(&blobsFrom, "blobs-from", "", "the old output store directory (a copy)")
	c.Flags().BoolVar(&allowMissing, "allow-missing-blobs", false, "accept references whose blob is not in --blobs-from (output kept in caster)")
	return c
}

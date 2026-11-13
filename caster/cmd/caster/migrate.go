package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"

	"github.com/hollis-labs/tools/caster/internal/migrate"
	"github.com/hollis-labs/tools/caster/internal/storage"
)

// migration is the staging state of one `caster migrate` run. The new database and blob tree are
// built under staging names beside their final ones and renamed into place only when the copy is
// proved, so a failed run leaves nothing that looks like a migrated store and the old files are never
// involved. The final database name is also the marker the legacy guard looks for.
type migration struct {
	verifyOnly bool
	finalDB    string // <dir>/caster.db
	stageDB    string // <dir>/caster.db.migrating
	finalCas   string // <dir>/cas
	stageCas   string // <dir>/cas.migrating
}

// prepare runs before the database module opens anything. It records the final and the staging names,
// points the database module at the staging file, and refuses a target that already exists (a store
// is migrated once: step zero of the cutover checks the same thing).
func (m *migration) prepare(app *module.App) error {
	m.finalDB = databasePath(app)
	if m.finalDB == "" {
		return errors.New("no database path configured")
	}
	m.finalCas = storage.RootFor(m.finalDB)
	m.stageDB, m.stageCas = m.finalDB+".migrating", m.finalCas+".migrating"
	if m.verifyOnly {
		if _, err := os.Stat(m.finalDB); err != nil {
			return fmt.Errorf("--verify-only needs the migrated store: %w", err)
		}
		return secureDatabase(m.finalDB)
	}
	// Refuse while a service is listening on the address this configuration serves: the old service
	// (or a new one) still has the data open. Only the configured address is probed.
	if addr := httpAddr(app); addr != "" {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = conn.Close()
			return fmt.Errorf("something is listening on %s, the address this configuration serves: stop the service before migrating. Nothing was changed", addr)
		}
	}
	for _, p := range []string{m.finalDB, m.finalDB + "-wal", m.finalDB + "-shm", m.finalCas} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists: a store is migrated once, into a data directory that has no caster.db and no cas/. Nothing was changed", p)
		}
	}
	// Leftovers of an earlier run that did not finish are this command's own staging names.
	for _, p := range []string{m.stageDB, m.stageDB + "-wal", m.stageDB + "-shm", m.stageCas, filepath.Join(filepath.Dir(m.finalDB), ".migrate-scratch")} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.finalDB), 0o700); err != nil {
		return err
	}
	db, _ := app.Config["database"].(map[string]any)
	sq, _ := db["sqlite"].(map[string]any)
	sq["path"] = m.stageDB
	return secureDatabase(m.stageDB)
}

// httpAddr is the address the http host module would bind, from the merged configuration.
func httpAddr(app *module.App) string {
	h, _ := app.Config["httphost"].(map[string]any)
	a, _ := h["addr"].(string)
	return a
}

// migrateCmd copies a pre-Substrate data directory into the new store and proves the copy.
//
//	caster migrate [--from DIR] [--report FILE]
//	caster migrate --verify-only [--from DIR]
func (c *caster) migrateCmd() *cobra.Command {
	var from, reportPath string
	var verifyOnly bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Copy the pre-Substrate data (main.db, blobs/) into the new store and prove the copy",
		Long: `Copies main.db and blobs/ from the old data directory into caster.db and cas/ beside them, and
proves it: every table's row count and digest, every blob re-hashed, the full-text index rebuilt and
compared row by row. The old files are only read (the database is copied aside first and SQLite opens the
copy), and the report says whether they are unchanged. Stop the old service first.

The target must not exist: a store is migrated once. A failed run leaves no caster.db and no cas/.
--verify-only compares an existing migrated store with the old directory again and writes nothing.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.kind != kindMigrate || c.mig == nil {
				return cmd.Help()
			}
			return c.migrate(cmd.Context(), from, reportPath, verifyOnly)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the old data directory (main.db and blobs/); default: the data directory this build resolves")
	cmd.Flags().StringVar(&reportPath, "report", "", "where to write the JSON report (default: migrate-report.json beside caster.db)")
	cmd.Flags().BoolVar(&verifyOnly, "verify-only", false, "compare the existing migrated store with the old directory; write nothing")
	return cmd
}

func (c *caster) migrate(ctx context.Context, from, reportPath string, verifyOnly bool) error {
	m := c.mig
	if from == "" {
		from = filepath.Dir(c.layout.MainDB())
	}
	if _, err := os.Stat(filepath.Join(from, "main.db")); err != nil {
		return fmt.Errorf("no old database to migrate: %w", err)
	}
	st := database.From(c.app)
	if st == nil {
		return errors.New("the database module must be started")
	}
	casRoot, dir := m.stageCas, filepath.Dir(m.finalDB)
	if verifyOnly {
		casRoot = m.finalCas
	}
	opts := migrate.Options{OldDir: from, Scratch: filepath.Join(dir, ".migrate-scratch"), DB: st, CasRoot: casRoot}
	run := migrate.Run
	if verifyOnly {
		run = migrate.Verify
	}
	rep, err := run(ctx, opts)
	if err != nil {
		c.finish = func(bool) error { return m.cleanup() }
		return err
	}
	if reportPath == "" {
		reportPath = filepath.Join(dir, "migrate-report.json")
		if !rep.OK {
			reportPath = filepath.Join(dir, "migrate-report.failed.json")
		}
	}
	if verifyOnly && reportPath == filepath.Join(dir, "migrate-report.json") {
		reportPath = "" // verifying writes nothing unless a report path was asked for
	}
	c.printReport(rep, verifyOnly)
	if reportPath != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(reportPath, append(b, '\n'), 0o600); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(c.out, "report  %s\n", reportPath)
	}
	if !rep.OK {
		for _, p := range rep.Problems {
			_, _ = fmt.Fprintf(c.errw, "caster: migrate: %s\n", p)
		}
		c.finish = func(bool) error { return m.cleanup() }
		return exitStatus(1)
	}
	if verifyOnly {
		return nil
	}
	// Fold the write-ahead log into the file so a single file is renamed.
	if _, err := st.DB().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		c.finish = func(bool) error { return m.cleanup() }
		return fmt.Errorf("checkpoint: %w", err)
	}
	c.finish = func(ok bool) error {
		if !ok {
			return m.cleanup()
		}
		return m.commit()
	}
	return nil
}

func (m *migration) cleanup() error {
	if m.verifyOnly {
		return nil
	}
	var errs []error
	for _, p := range []string{m.stageDB, m.stageDB + "-wal", m.stageDB + "-shm", m.stageCas, filepath.Join(filepath.Dir(m.finalDB), ".migrate-scratch")} {
		errs = append(errs, os.RemoveAll(p))
	}
	return errors.Join(errs...)
}

// commit moves the staged tree and database into their names. The blob tree goes first: caster.db is
// the marker that the data is migrated, so it appears last.
func (m *migration) commit() error {
	// The -shm file is only a shared-memory index and is rebuilt on open; a -wal with content would
	// be data that is not in the file being renamed.
	if fi, err := os.Stat(m.stageDB + "-wal"); err == nil && fi.Size() > 0 {
		return fmt.Errorf("%s-wal still holds data after the checkpoint; nothing was moved into place", m.stageDB)
	}
	_ = os.Remove(m.stageDB + "-wal")
	_ = os.Remove(m.stageDB + "-shm")
	if err := os.Rename(m.stageCas, m.finalCas); err != nil {
		return err
	}
	if err := os.Rename(m.stageDB, m.finalDB); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(filepath.Dir(m.finalDB), ".migrate-scratch"))
	return nil
}

func (c *caster) printReport(rep migrate.Report, verifyOnly bool) {
	verb := "migrated"
	if verifyOnly {
		verb = "verified"
	}
	_, _ = fmt.Fprintf(c.out, "%s %s\n", verb, rep.OldDir)
	for _, f := range rep.SourceFiles {
		_, _ = fmt.Fprintf(c.out, "  old %-12s %9d bytes  sha256 %s\n", f.Name, f.Size, f.SHA256[:16])
	}
	for _, t := range rep.Tables {
		mark := "ok"
		if !t.Equal {
			mark = "DIFFERENT"
		}
		_, _ = fmt.Fprintf(c.out, "  %-16s -> %-16s %6d rows  digest %s  %s\n", t.Source, t.Target, t.TargetRows, t.TargetDigest[:12], mark)
	}
	b := rep.Blobs
	_, _ = fmt.Fprintf(c.out, "  blobs: %d rows, %d files, %d verified (%d bytes); %d without a row, %d rows without a file, %d corrupt, %d tmp files left alone\n",
		b.Rows, b.Files, b.Verified, b.Bytes, len(b.Orphans), len(b.Missing), len(b.Corrupt), b.TmpIgnored)
	f := rep.FTS
	_, _ = fmt.Fprintf(c.out, "  full-text: %d rows in the old index, %d rebuilt; %d only old, %d only rebuilt, %d with a different body\n",
		f.SourceRows, f.RebuiltRows, f.OnlyInSource, f.OnlyInRebuilt, f.BodyDiffers)
	_, _ = fmt.Fprintf(c.out, "  old files unchanged: %v\n", rep.OldUnchanged)
	res := "PROVED EQUAL"
	if !rep.OK {
		res = "NOT PROVED"
	}
	_, _ = fmt.Fprintln(c.out, strings.Repeat("-", 8), res)
}

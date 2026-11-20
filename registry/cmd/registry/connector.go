package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/tools/registry/internal/catalog"
	"github.com/spf13/cobra"
)

// snapshotFileConnector is a temporary, operator-invoked source adapter. It
// reads no consumer directories and never installs a recurring schedule.
type snapshotFileConnector struct{ id, path string }

func (c snapshotFileConnector) ID() string { return c.id }
func (c snapshotFileConnector) Snapshot(_ context.Context, _ string) (catalog.ImportRequest, error) {
	f, err := os.Open(c.path) // #nosec G304 -- explicit operator-owned CLI snapshot file, never remote input
	if err != nil {
		return catalog.ImportRequest{}, fmt.Errorf("cannot read connector snapshot")
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, catalog.MaxRequestBytes+1))
	if err != nil {
		return catalog.ImportRequest{}, fmt.Errorf("cannot read connector snapshot")
	}
	var a catalog.Args
	if err = catalog.Decode(raw, &a); err != nil {
		return catalog.ImportRequest{}, err
	}
	if a.Import == nil {
		return catalog.ImportRequest{}, fmt.Errorf("snapshot requires an import envelope")
	}
	return *a.Import, nil
}
func connectorCommand() *cobra.Command {
	var endpoint, tokenFile, file, id, preparationKey string
	var jsonOutput bool
	cmd := &cobra.Command{Use: "import-run", Short: "Run a temporary JSON snapshot connector once (scheduler disabled)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
		defer func() {
			if runErr != nil && jsonOutput {
				_ = json.NewEncoder(cmd.ErrOrStderr()).Encode(map[string]any{"error": commandError(runErr)})
				runErr = reportedError{runErr}
			}
		}()
		if file == "" || file == "-" || id == "" || preparationKey == "" {
			return fmt.Errorf("explicit --file, --connector and --run-id are required; snapshot is reread before commit")
		}
		result, err := catalog.RunConnectorWithPreparationKey(cmd.Context(), registryClient(endpoint, tokenFile).Call, snapshotFileConnector{id: id, path: file}, preparationKey)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	cmd.Flags().StringVar(&endpoint, "url", defaultURL(), "Registry service URL")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "Credential file")
	cmd.Flags().StringVar(&file, "file", "", "JSON import snapshot file (read twice)")
	cmd.Flags().StringVar(&preparationKey, "run-id", "", "Stable run identity: reuse for uncertain retries; change for an intended new sync")
	cmd.Flags().StringVar(&id, "connector", "", "Stable temporary connector identity")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Include structured errors; success output is always JSON")
	return cmd
}

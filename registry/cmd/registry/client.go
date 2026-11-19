package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/tools/registry/internal/catalog"
	"github.com/spf13/cobra"
)

func defaultURL() string {
	if v := os.Getenv("REGISTRY_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:18190"
}
func registryClient(endpoint, tokenFile string) *catalog.Client {
	return &catalog.Client{URL: endpoint, HTTP: &http.Client{Timeout: 30 * time.Second}, Token: func() (string, error) {
		file := tokenFile
		if file == "" {
			file = os.Getenv("REGISTRY_TOKEN_FILE")
		}
		if file != "" {
			b, err := os.ReadFile(file) // #nosec G304 G703 -- explicit operator-owned credential path
			if err != nil {
				return "", fmt.Errorf("cannot read credential file")
			}
			return strings.TrimSpace(string(b)), nil
		}
		return os.Getenv("REGISTRY_TOKEN"), nil
	}}
}
func addClientCommands(root *cobra.Command) {
	root.SilenceErrors = true
	root.SilenceUsage = true
	// Human commands and the discoverable exact operation names share one path.
	names := map[string]string{"kinds_list": "kinds", "schema_get": "schema", "record_create": "create", "record_get": "get", "publication_publish": "publish", "record_list": "list", "record_search": "search", "record_update": "update", "record_archive": "archive", "record_delete": "delete", "record_verify": "verify", "revision_list": "history", "revision_get": "revision", "snapshot_get": "snapshot", "diagnostics": "doctor", "backup_create": "backup", "retention_run": "retention", "references_report": "references", "record_migrate": "migrate", "migration_list": "migrations"}
	for _, op := range catalog.Operations {
		name := names[op]
		if name == "" {
			name = strings.ReplaceAll(op, "_", "-")
		}
		var endpoint, tokenFile, file, id, version, key, schema, cursor, kind, search, publication string
		var full, jsonOutput, allow, readable, apply bool
		var limit int
		cmd := &cobra.Command{Use: name + " [id]", Short: "Registry " + strings.ReplaceAll(op, "_", " "), Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, pos []string) (runErr error) {
			defer func() {
				if runErr != nil && jsonOutput {
					_ = json.NewEncoder(cmd.ErrOrStderr()).Encode(map[string]any{"error": commandError(runErr)})
					runErr = reportedError{runErr}
				}
			}()
			a := catalog.Args{}
			if file != "" {
				reader := cmd.InOrStdin()
				if file != "-" {
					f, err := os.Open(file) // #nosec G304 -- explicit CLI input file selected by the caller
					if err != nil {
						return err
					}
					defer func() { _ = f.Close() }()
					reader = f
				}
				b, err := io.ReadAll(io.LimitReader(reader, catalog.MaxRequestBytes+1))
				if err != nil {
					return err
				}
				if checkErr := catalog.Decode(b, &a); checkErr != nil {
					return checkErr
				}
			}
			if len(pos) > 0 {
				id = pos[0]
			}
			if id != "" {
				a.ID = id
			}
			if publication != "" {
				a.Publication = publication
				a.Query.Publication = publication
			}
			if version != "" {
				a.Version = version
			}
			if key != "" {
				a.IdempotencyKey = key
			}
			if schema != "" {
				a.Schema = schema
			}
			if allow {
				a.AllowUnresolved = true
			}
			if cursor != "" {
				a.Query.Cursor = cursor
			}
			if kind != "" {
				a.Query.Kind = kind
			}
			if search != "" {
				a.Query.Search = search
			}
			if full {
				a.Query.Full = true
			}
			if cmd.Flags().Changed("limit") {
				a.Query.Limit = limit
			}
			if cmd.Flags().Changed("apply") {
				a.Apply = apply
			}
			if jsonOutput && readable {
				return fmt.Errorf("choose --json or --readable")
			}
			out, err := registryClient(endpoint, tokenFile).Call(cmd.Context(), op, a)
			if err != nil {
				return err
			}
			if readable {
				return renderReadable(cmd.OutOrStdout(), out)
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(out)
		}}
		cmd.Flags().StringVar(&endpoint, "url", defaultURL(), "Registry service URL")
		cmd.Flags().StringVar(&tokenFile, "token-file", "", "Credential file")
		cmd.Flags().StringVar(&file, "file", "", "JSON operation arguments file; - reads stdin")
		cmd.Flags().StringVar(&id, "id", "", "Exact canonical ID")
		cmd.Flags().StringVar(&version, "version", "", "Current version_token for mutations")
		cmd.Flags().StringVar(&key, "idempotency-key", "", "Creation retry key")
		cmd.Flags().StringVar(&schema, "schema", "", "Versioned schema for discovery")
		cmd.Flags().StringVar(&cursor, "cursor", "", "Continuation cursor")
		cmd.Flags().StringVar(&publication, "publication", "", "Read published (default) or draft revisions")
		cmd.Flags().StringVar(&kind, "kind", "", "Filter kind")
		cmd.Flags().StringVar(&search, "query", "", "Search text")
		cmd.Flags().IntVar(&limit, "limit", 50, "Page limit 1..200")
		cmd.Flags().BoolVar(&full, "full", false, "Include full payloads")
		cmd.Flags().BoolVar(&allow, "allow-unresolved", false, "Allow absent local relation targets")
		cmd.Flags().BoolVar(&readable, "readable", false, "Render human tables and readable details")
		if op == "retention_run" {
			cmd.Flags().BoolVar(&apply, "apply", false, "Apply retention; default previews without changes")
		}
		cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit structured errors and JSON success output")
		root.AddCommand(cmd)
	}
}

// reportedError prevents main from printing a second plain-text copy after a
// command has emitted its machine-readable error envelope.
type reportedError struct{ error }

func (e reportedError) Unwrap() error { return e.error }

func commandError(err error) *catalog.Error {
	var serviceError *catalog.Error
	if errors.As(err, &serviceError) {
		return serviceError
	}
	return &catalog.Error{Code: "validation_error", Field: "input", Message: "unable to read or process command input", Remedy: "Check arguments and local input/credential file access."}
}

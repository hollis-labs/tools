package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/tools/registry/internal/catalog"
	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/mcp"
)

// Help and print-only commands never need credentials or secret-store modules.
func commandModules(args []string) []string {
	if len(args) == 0 || slices.Contains(args, "--help") || slices.Contains(args, "-h") || args[0] != "serve" {
		return []string{"config", "logging", "health", "cli"}
	}
	return substrateModules
}

// configureMCP runs after the operator config is loaded, before auth starts.
// The environment token is optional when the operator has configured another
// Substrate verifier; it grants only the scaffold's endpoint/read scopes.
func configureMCP(_ context.Context, app *module.App) error {
	cfg, _ := app.Config["auth"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
		app.Config["auth"] = cfg
	}
	if mode, _ := cfg["mode"].(string); mode != "" && mode != "enforce" {
		return fmt.Errorf("MCP requires auth.mode enforce")
	}
	cfg["mode"] = "enforce"
	for _, credential := range []struct{ env, id, scope string }{{"REGISTRY_TOKEN", "service", "read"}, {"REGISTRY_WRITE_TOKEN", "writer", "write"}, {"REGISTRY_REVIEW_TOKEN", "reviewer", "review"}} {
		if token := os.Getenv(credential.env); token != "" {
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("MCP token must not be blank")
			}
			keys, _ := cfg["api_keys"].([]any)
			cfg["api_keys"] = append(keys, map[string]any{"id": credential.id, "hash": libauth.HashKey(token), "scopes": []any{"registry.mcp", "registry.read", "registry." + credential.scope}})
		}
	}
	db, _ := app.Config["database"].(map[string]any)
	if db == nil {
		db = map[string]any{}
		app.Config["database"] = db
	}
	if driver, _ := db["driver"].(string); driver != "" && driver != "sqlite" {
		return fmt.Errorf("registry requires SQLite")
	}
	db["driver"] = "sqlite"
	sq, _ := db["sqlite"].(map[string]any)
	if sq == nil {
		sq = map[string]any{}
		db["sqlite"] = sq
	}
	path, _ := sq["path"].(string)
	if path == "" {
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			base = filepath.Join(home, ".local", "share")
		}
		path = filepath.Join(base, "registry", "registry.db")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("database.sqlite.path must be absolute")
	}
	sq["path"] = path
	// A managed secret backend must never resolve a relative directory against the checkout.
	secrets, _ := app.Config["secrets"].(map[string]any)
	if secrets == nil {
		secrets = map[string]any{}
		app.Config["secrets"] = secrets
	}
	if dir, _ := secrets["state_dir"].(string); !filepath.IsAbs(dir) {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			base = filepath.Join(home, ".local", "state")
		}
		base, err := filepath.Abs(base)
		if err != nil {
			return err
		}
		secrets["state_dir"] = filepath.Join(base, app.Name, "secrets", dir)
	}
	return nil
}

func catalogService(app *module.App) (*catalog.Service, error) {
	db := database.From(app)
	if db == nil {
		return nil, fmt.Errorf("registry database module is not started")
	}
	bindings := map[string]string{"writer": "registry"}
	cfg, _ := app.Config["registry"].(map[string]any)
	configured, _ := cfg["authorities"].(map[string]any)
	for id, v := range configured {
		authority, ok := v.(string)
		if !ok || strings.TrimSpace(authority) == "" {
			return nil, fmt.Errorf("registry.authorities requires nonempty authority strings")
		}
		bindings[id] = authority
	}
	reviewers := map[string]string{"reviewer": "ground"}
	configuredReviewers, _ := cfg["reviewers"].(map[string]any)
	for id, v := range configuredReviewers {
		role, ok := v.(string)
		if !ok || (role != "ground" && role != "elevated" && role != "owner") {
			return nil, fmt.Errorf("registry.reviewers values must be ground, elevated or owner")
		}
		reviewers[id] = role
	}
	svc := catalog.New(db, bindings, reviewers)
	maintenance, _ := cfg["maintenance"].(map[string]any)
	for key, dest := range map[string]*time.Duration{"cursor_ttl": &svc.Maintenance.CursorTTL, "receipt_ttl": &svc.Maintenance.ReceiptTTL, "draft_history_ttl": &svc.Maintenance.DraftHistoryTTL} {
		if raw, exists := maintenance[key]; exists {
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("registry.maintenance.%s requires a duration string", key)
			}
			value, err := time.ParseDuration(text)
			if err != nil || value < 0 {
				return nil, fmt.Errorf("registry.maintenance.%s requires a nonnegative duration", key)
			}
			*dest = value
		}
	}
	if raw, exists := maintenance["backup_directory"]; exists {
		dir, ok := raw.(string)
		if !ok || !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("registry.maintenance.backup_directory requires an absolute path")
		}
		svc.Maintenance.BackupDirectory = dir
	}
	return svc, nil
}
func registerMCP(app *module.App) {
	server := mcp.Server(app)
	if server == nil {
		return
	}
	svc, err := catalogService(app)
	if err != nil {
		return
	}
	for _, op := range catalog.Operations {
		auth.ScopeTable(app).Tool("registry_"+op, catalog.Scope(op))
	}
	catalog.RegisterMCP(server, func(ctx context.Context, op string, a catalog.Args) (any, error) {
		if checkErr := mcp.Authorize(ctx, app, "registry_"+op); checkErr != nil {
			return nil, checkErr
		}
		return svc.Call(ctx, op, a)
	})
}
func mcpCmd(ctx context.Context, _ *module.App) *cobra.Command {
	var endpoint, tokenFile string
	cmd := &cobra.Command{Use: "mcp", Short: "Serve stdio MCP as an authenticated Registry HTTP client", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		client := registryClient(endpoint, tokenFile)
		server := gmcp.NewServer("registry", "dev")
		catalog.RegisterMCP(server, client.Call)
		return server.Run(ctx)
	}}
	cmd.Flags().StringVar(&endpoint, "url", defaultURL(), "Registry service URL")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "Credential file (reread on every service call)")
	return cmd
}

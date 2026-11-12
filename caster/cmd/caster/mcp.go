package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	mcpmod "github.com/hollis-labs/substrate/modules/mcp"

	"github.com/hollis-labs/tools/caster/internal/mcptools"
	"github.com/hollis-labs/tools/caster/internal/service"
	"github.com/hollis-labs/tools/caster/internal/storage"
)

// storageRoot is the blob directory that goes with a database path.
func storageRoot(dbPath string) string { return storage.RootFor(dbPath) }

// mcpCmd serves the tools over stdio for an agent that launches caster as a subprocess. As a client of
// a home node (CASTER_REMOTE or a saved profile) every tool call is an HTTP call that carries the API
// key, so the agent holds exactly the scopes the key has. Without one it works on the local store as
// the local principal: read, resolve, write, observe, never act or admin (those need a key).
func (c *caster) mcpCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:          "mcp",
		Short:        "Serve the MCP tools over stdio",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.kind != kindMCP {
				return cmd.Help()
			}
			svc, callCtx, release, err := c.serviceFor(ctx)
			if err != nil {
				return err
			}
			defer release()
			mcptools.Register(c.app, svc, nil) // no auth module in this process: the service enforces the scopes
			if c.profile == nil {
				// The framework's own local-trust process says this on stderr too; stdout is the protocol.
				_, _ = fmt.Fprintf(c.errw, "mcp: stdio under local filesystem trust (no key): principal %s, scopes %s\n", service.LocalPrincipalID, strings.Join(service.LocalScopes, " "))
			}
			return mcpmod.ServeStdio(callCtx, c.app, mcpmod.StdioOptions{})
		},
	}
}

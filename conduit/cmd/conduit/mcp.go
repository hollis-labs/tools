package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/mcp"

	conduitmcp "github.com/hollis-labs/tools/conduit/internal/mcp"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/settings"
)

// stdio are the streams `conduit mcp` speaks on; nil means the process's own. Tests set them.
var stdio struct {
	in  io.ReadCloser
	out io.WriteCloser
}

func mcpCmd(ctx context.Context, app *module.App, svc *service.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the MCP tools on stdio for an agent launched locally",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return serveMCP(ctx, app, svc) },
	}
}

// serveMCP runs the stdio MCP server under local trust, narrowed to read and run: a process the user launched against
// the data file runs without a token, but never holds approve, read.all or admin. A token given in mcp.stdio_token_file
// is verified as always. While it serves, runs of its principal that a person approved elsewhere are started here.
func serveMCP(ctx context.Context, app *module.App, svc *service.Service) error {
	set, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		return err
	}
	table := auth.ScopeTable(app)
	if table == nil {
		return errors.New("the auth module must be started")
	}
	roots, err := allowRoots(set)
	if err != nil {
		return err
	}
	conduitmcp.Register(app, svc, table, conduitmcp.Options{AllowDirs: roots})

	tick, stop := context.WithCancel(ctx)
	defer stop()
	panics.Go("mcp ticker", func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-tick.Done():
				return
			case <-t.C:
				_ = panics.Call("mcp start-approved", func() error { _, err := svc.StartApproved(mcp.LocalPrincipalID); return err })
			}
		}
	}, nil)
	if err := mcp.ServeStdio(ctx, app, mcp.StdioOptions{In: stdio.in, Out: stdio.out, LocalTrust: true, LocalScopes: conduitmcp.LocalScopes, Notice: os.Stderr}); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}

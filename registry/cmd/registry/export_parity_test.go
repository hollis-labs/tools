package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tools/registry/internal/catalog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// Exercise the separate plan/authorization/apply contract across all client
// surfaces. Authentication enforcement itself is covered by the service tests.
func TestExportPlanAuthorizationAndAttemptAcrossTransports(t *testing.T) {
	app := mcpApp(t)
	svc, err := catalogService(app)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "output")
	if err = svc.ConfigureExportTargets(map[string]string{"fixture": output}); err != nil {
		t.Fatal(err)
	}
	principal := libauth.Principal{ID: "writer", Verified: true, Scopes: []string{"registry.read", "registry.write"}}
	handler := catalog.Handler(svc.Call)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(libauth.WithPrincipal(r.Context(), principal)))
	}))
	defer server.Close()
	client := registryClient(server.URL, "")
	request := &catalog.ExportRequest{Target: "fixture"}
	raw, err := client.Call(context.Background(), "export_plan", catalog.Args{Export: request})
	if err != nil {
		t.Fatal(err)
	}
	decode := func(value any, target any) {
		t.Helper()
		b, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if decodeErr := json.Unmarshal(b, target); decodeErr != nil {
			t.Fatal(decodeErr)
		}
	}
	var plan catalog.ExportPlan
	decode(raw, &plan)
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatal("plan wrote output", statErr)
	}
	cli := &cobra.Command{Use: "registry"}
	addClientCommands(cli)
	var stdout bytes.Buffer
	args, err := json.Marshal(catalog.Args{Export: request, Hash: plan.Bundle.Digest})
	if err != nil {
		t.Fatal(err)
	}
	cli.SetIn(bytes.NewReader(args))
	cli.SetOut(&stdout)
	cli.SetArgs([]string{"export-authorize", "--file", "-", "--url", server.URL, "--json"})
	if err = cli.Execute(); err != nil {
		t.Fatal(err)
	}
	var receipt catalog.ExportReceipt
	if err = json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Bundle.Digest != plan.Bundle.Digest || receipt.ID == "" {
		t.Fatal("authorization changed plan")
	}
	adapter := gmcp.NewServer("registry-export-test", "test")
	catalog.RegisterMCP(adapter, client.Call)
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	defer func() { _ = srvIn.Close(); _ = cliOut.Close(); _ = cliIn.Close(); _ = srvOut.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss, err := adapter.SDKServer().Connect(ctx, &mcpsdk.IOTransport{Reader: srvIn, Writer: srvOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcpsdk.IOTransport{Reader: cliIn, Writer: cliOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	applyArgs := catalog.Args{ReceiptID: receipt.ID, Hash: receipt.Bundle.Digest}
	result, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_export_apply", Arguments: applyArgs})
	if err != nil || result.IsError {
		t.Fatal("MCP apply", result, err)
	}
	raw, err = client.Call(ctx, "export_attempt_get", catalog.Args{ReceiptID: receipt.ID})
	if err != nil {
		t.Fatal(err)
	}
	var attempt catalog.ExportAttempt
	decode(raw, &attempt)
	if attempt.State != "completed" {
		t.Fatal("attempt not durable", attempt)
	}
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_export_apply", Arguments: applyArgs})
	if err != nil || !result.IsError {
		t.Fatal("MCP receipt reused", result, err)
	}
}

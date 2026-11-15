package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/mcp"
	"github.com/hollis-labs/tools/registry/internal/catalog"
)

func mcpApp(t *testing.T) *module.App {
	t.Helper()
	t.Setenv("SUBSTRATE_CONFIG", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	database.UseMigrationFS(catalog.Migrations, "migrations")
	t.Setenv("REGISTRY_TOKEN", "test-service-credential")
	run, err := boot.StartWith(context.Background(), substrateApp, substrateModules, substrateConfigJSON, boot.Options{AfterConfig: func(ctx context.Context, app *module.App) error {
		if checkErr := configureMCP(ctx, app); checkErr != nil {
			return checkErr
		}
		cfg := app.Config["auth"].(map[string]any)
		keys, _ := cfg["api_keys"].([]any)
		cfg["api_keys"] = append(keys,
			map[string]any{"id": "configured", "hash": libauth.HashKey("configured-credential"), "scopes": []any{app.Name + ".mcp", app.Name + ".read"}},
			map[string]any{"id": "limited", "hash": libauth.HashKey("limited-credential"), "scopes": []any{app.Name + ".read"}},
		)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Stop(context.Background()) })
	registerMCP(run.App)
	return run.App
}

func TestMCPHTTPRequiresCredentialAndScope(t *testing.T) {
	app := mcpApp(t)
	handler, err := httphost.Handler(app, httphost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong", "test-service-credential", "configured-credential", "limited-credential"} {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"registry_kinds_list","arguments":{}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if token == "test-service-credential" || token == "configured-credential" {
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), "schemas") {
				t.Fatalf("authenticated MCP: %d %s", rec.Code, rec.Body)
			}
		} else if token == "limited-credential" {
			if rec.Code != 403 {
				t.Fatalf("insufficient scope: %d", rec.Code)
			}
		} else if rec.Code != 401 {
			t.Fatalf("invalid credential: %d", rec.Code)
		}
	}
}

func TestMCPRefusesAuthDowngrade(t *testing.T) {
	app := &module.App{Name: substrateApp, Config: map[string]any{"auth": map[string]any{"mode": "off"}}}
	if err := configureMCP(context.Background(), app); err == nil {
		t.Fatal("auth downgrade accepted")
	}
	// Anonymous execution cannot call the registered tool directly either.
	app = mcpApp(t)
	if err := mcp.Authorize(context.Background(), app, "registry_kinds_list"); err == nil {
		t.Fatal("anonymous tool call accepted")
	}
}

func TestMCPManagedServiceListener(t *testing.T) {
	app := mcpApp(t)
	t.Setenv(modeEnv, "")
	app.Config["httphost"] = map[string]any{"addr": "127.0.0.1:0"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- serveWith(ctx, app, func(a net.Addr) { ready <- a }, false, true) }()
	var url string
	select {
	case a := <-ready:
		url = "http://" + a.String() + "/mcp"
	case err := <-done:
		t.Fatalf("managed MCP serve: %v", err)
	case <-ctx.Done():
		t.Fatal("managed MCP did not start")
	}
	for _, token := range []string{"", "wrong", "test-service-credential", "configured-credential", "limited-credential"} {
		req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"registry_kinds_list","arguments":{}}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if token == "test-service-credential" || token == "configured-credential" {
			if resp.StatusCode != 200 || !strings.Contains(string(body), "schemas") {
				t.Fatalf("MCP response: %d %s", resp.StatusCode, body)
			}
		} else if token == "limited-credential" {
			if resp.StatusCode != 403 {
				t.Fatalf("insufficient scope: %d", resp.StatusCode)
			}
		} else if resp.StatusCode != 401 {
			t.Fatalf("invalid credential: %d", resp.StatusCode)
		}
	}
	cancel()
	if checkErr := <-done; checkErr != nil {
		t.Fatal(checkErr)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/tools/registry/internal/catalog"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func testService(t *testing.T) string {
	t.Helper()
	t.Setenv("REGISTRY_WRITE_TOKEN", "test-write-credential")
	t.Setenv("REGISTRY_REVIEW_TOKEN", "test-review-credential")
	app := mcpApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	app.Config["httphost"] = map[string]any{"addr": "127.0.0.1:0"}
	go func() { done <- serveWith(ctx, app, func(a net.Addr) { ready <- a }, false, true) }()
	t.Cleanup(func() {
		cancel()
		if checkErr := <-done; checkErr != nil {
			t.Errorf("serve: %v", checkErr)
		}
	})
	select {
	case a := <-ready:
		return "http://" + a.String()
	case <-ctx.Done():
		t.Fatal("service not ready")
		return ""
	}
}
func responseRecord(t *testing.T, value any) catalog.Record {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var r catalog.Record
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestAPIClientCLIMCPShareDataAndErrors(t *testing.T) {
	endpoint := testService(t)
	client := registryClient(endpoint, "")
	client.Token = func() (string, error) { return "test-write-credential", nil }
	raw := json.RawMessage(`{"schema":"tool/1","id":"tool:mcp/example/..","source":"mcp","origin":"example","lifecycle":"active","definition":{"name":"..","input_schema":{"type":"object"},"annotations":{"title":"Title"}}}`)
	out, err := client.Call(context.Background(), "record_create", catalog.Args{Record: &catalog.Input{Schema: "tool/1", Key: "dot-tool", Payload: raw}})
	if err != nil {
		t.Fatal(err)
	}
	r := responseRecord(t, out)
	if r.ID != "tool:mcp/example/.." {
		t.Fatalf("rewrote ID %s", r.ID)
	}
	// CLI output is machine JSON over the same authenticated HTTP service.
	file := filepath.Join(t.TempDir(), "credential")
	if checkErr := os.WriteFile(file, []byte("test-write-credential"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	root := &cobra.Command{Use: "registry", SilenceUsage: true, SilenceErrors: true}
	addClientCommands(root)
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"get", r.ID, "--publication", "draft", "--url", endpoint, "--token-file", file, "--json"})
	if checkErr := root.Execute(); checkErr != nil {
		t.Fatal(checkErr)
	}
	var fromCLI catalog.Record
	if checkErr := json.Unmarshal(stdout.Bytes(), &fromCLI); checkErr != nil {
		t.Fatal(checkErr)
	}
	if fromCLI.VersionToken != r.VersionToken {
		t.Fatal("CLI read another revision")
	}
	// A stdio MCP adapter is an HTTP client, with no SQLite/config/verifier of its own.
	adapter := gmcp.NewServer("registry-test", "test")
	catalog.RegisterMCP(adapter, client.Call)
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	t.Cleanup(func() { _ = srvIn.Close(); _ = cliOut.Close(); _ = cliIn.Close(); _ = srvOut.Close() })
	serverTransport := &mcpsdk.IOTransport{Reader: srvIn, Writer: srvOut}
	clientTransport := &mcpsdk.IOTransport{Reader: cliIn, Writer: cliOut}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ss, err := adapter.SDKServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_record_get", Arguments: map[string]any{"id": r.ID, "publication": "draft"}})
	if err != nil || result.IsError {
		t.Fatalf("MCP read: %v %v", result, err)
	}
	fromMCP := responseRecord(t, result.StructuredContent)
	if fromMCP.ID != r.ID || fromMCP.VersionToken != r.VersionToken {
		t.Fatal("MCP identity/revision changed")
	}
	// Mutations from MCP are visible to API; bodies and slash IDs are unchanged.
	doc := `{"record":{"schema":"document/1","key":"guide","payload":{"schema":"document/1","id":"guide/../_body","name":"Guide","subtype":"sop","format":"markdown","body":"# Rules\nKeep exactly.\n"}}}`
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_record_create", Arguments: json.RawMessage(doc)})
	if err != nil || result.IsError {
		t.Fatalf("MCP create: %v %v", result, err)
	}
	document := responseRecord(t, result.StructuredContent)
	out, err = client.Call(ctx, "record_get", catalog.Args{Publication: "draft", ID: document.ID})
	if err != nil {
		t.Fatal(err)
	}
	got := responseRecord(t, out)
	var gotDoc struct{ Body string }
	if checkErr := json.Unmarshal(got.Payload, &gotDoc); checkErr != nil {
		t.Fatal(checkErr)
	}
	if gotDoc.Body != "# Rules\nKeep exactly.\n" {
		t.Fatal("body changed")
	}
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_record_archive", Arguments: map[string]any{"id": document.ID, "version": "stale"}})
	if err != nil || !result.IsError {
		t.Fatalf("stale error: %v %v", result, err)
	}
	b, _ := json.Marshal(result.StructuredContent)
	if !bytes.Contains(b, []byte(`"code":"version_conflict"`)) {
		t.Fatalf("lost structured error: %s", b)
	}
	// Raw arguments survive the SDK's generic map decoding before shared numeric
	// validation. Nonzero underflow must not enter the database as zero.
	bad := strings.Replace(doc, `"key":"guide"`, `"key":"bad"`, 1)
	bad = strings.Replace(bad, `"body":"# Rules\nKeep exactly.\n"`, `"body":"Bad","metadata":{"value":1e-400}`, 1)
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_record_create", Arguments: json.RawMessage(bad)})
	if err != nil || !result.IsError {
		t.Fatalf("underflow accepted: %v %v", result, err)
	}
	b, _ = json.Marshal(result.StructuredContent)
	if !bytes.Contains(b, []byte("underflows")) {
		t.Fatalf("wrong numeric rejection: %s", b)
	}
	for _, number := range []string{"9007199254740993", "1e-400", "1e400"} {
		badEnvelope := strings.Replace(doc, `"record":{`, `"record":{"metadata":{"number":`+number+`},`, 1)
		rejected, callErr := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_record_create", Arguments: json.RawMessage(badEnvelope)})
		// Overflow can be refused by the SDK decoder before reaching the handler;
		// either protocol or structured execution rejection must remain a failure.
		if callErr == nil && (rejected == nil || !rejected.IsError) {
			t.Fatalf("MCP envelope metadata admitted %s", number)
		}
		var args catalog.Args
		// UseNumber here mirrors operator input without invoking Registry Decode.
		decoder := json.NewDecoder(strings.NewReader(badEnvelope))
		decoder.UseNumber()
		if decodeErr := decoder.Decode(&args); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if _, callErr = client.Call(ctx, "record_create", args); callErr == nil {
			t.Fatalf("HTTP envelope metadata admitted %s", number)
		}
	}

	// Import prepares over MCP, commits through the CLI HTTP client and is read
	// through the API. No transport can inject publication or source authority.
	snapshot := `{"import":{"connector":"fixture-parity","checkpoint":"snapshot-1","expected_checkpoint":"","entries":[{"source_key":"fixture-project","source_revision":"revision-1","record":{"schema":"project/1","key":"fixture-project","payload":{"schema":"project/1","id":"fixture-project","name":"Fixture","class":2,"lifecycle":"active","visibility":"public","owner":"user:test","interfaces":[{"type":"cli","auth":"none"}]}}}]}}`
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_import_prepare", Arguments: json.RawMessage(snapshot)})
	if err != nil || result.IsError {
		t.Fatalf("MCP import prepare: %v %v", result, err)
	}
	planRaw, _ := json.Marshal(result.StructuredContent)
	var plan catalog.ImportPlan
	if err = json.Unmarshal(planRaw, &plan); err != nil || plan.Counts.Created != 1 {
		t.Fatalf("bad import plan: %s %v", planRaw, err)
	}
	commitRaw, _ := json.Marshal(catalog.Args{PlanID: plan.ID, Hash: plan.Digest, SourceDigest: plan.SourceDigest, IdempotencyKey: "parity-import"})
	importRoot := &cobra.Command{Use: "registry"}
	addClientCommands(importRoot)
	var importOutput bytes.Buffer
	importRoot.SetIn(bytes.NewReader(commitRaw))
	importRoot.SetOut(&importOutput)
	importRoot.SetArgs([]string{"import-commit", "--file", "-", "--url", endpoint, "--token-file", file, "--json"})
	if err = importRoot.Execute(); err != nil {
		t.Fatal(err)
	}
	var imported catalog.ImportResult
	if err = json.Unmarshal(importOutput.Bytes(), &imported); err != nil || imported.Counts.Created != 1 {
		t.Fatal(importOutput.String(), err)
	}
	out, err = client.Call(ctx, "record_get", catalog.Args{ID: "project:fixture-project", Publication: "draft"})
	if err != nil || responseRecord(t, out).Publication != "draft" {
		t.Fatal("import not visible as draft", out, err)
	}
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_changes_list", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatal("MCP publication feed", result, err)
	}
	changesRaw, _ := json.Marshal(result.StructuredContent)
	var changes catalog.Changes
	if err = json.Unmarshal(changesRaw, &changes); err != nil || len(changes.Changes) != 0 || changes.Cursor == "" {
		t.Fatal("draft import leaked", string(changesRaw), err)
	}

}
func TestHTTPMCPWriteScopesAndExactIdentity(t *testing.T) {
	endpoint := testService(t)
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"registry_record_create","arguments":{"record":{"schema":"document/1","key":"http-body","payload":{"schema":"document/1","id":"http/../body","name":"Body","subtype":"procedure","format":"markdown","body":"Exact body"}}}}}`
	for _, tc := range []struct {
		token   string
		success bool
	}{{"test-service-credential", false}, {"test-write-credential", true}} {
		req, err := http.NewRequest("POST", endpoint+"/mcp", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tc.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if tc.success {
			if !bytes.Contains(b, []byte(`document:http/../body`)) || bytes.Contains(b, []byte(`"isError":true`)) {
				t.Fatalf("MCP write: %d %s", resp.StatusCode, b)
			}
		} else if !bytes.Contains(b, []byte(`"isError":true`)) && resp.StatusCode != 403 {
			t.Fatalf("read credential wrote: %d %s", resp.StatusCode, b)
		}
	}
}

func TestPublicationHTTPCLIAndMCPParity(t *testing.T) {
	endpoint := testService(t)
	client := registryClient(endpoint, "")
	client.Token = func() (string, error) { return "test-write-credential", nil }
	ctx := context.Background()
	v, err := client.Call(ctx, "record_create", catalog.Args{Record: &catalog.Input{Schema: "document/1", Key: "published-guide", Payload: json.RawMessage(`{"schema":"document/1","id":"publish/../guide","name":"Guide","subtype":"sop","format":"markdown","body":"Reviewed body"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	r := responseRecord(t, v)
	if _, err = client.Call(ctx, "record_get", catalog.Args{ID: r.ID}); err == nil {
		t.Fatal("HTTP default exposed draft")
	}
	v, err = client.Call(ctx, "publication_approval_prepare", catalog.Args{Candidates: []catalog.Candidate{{ID: r.ID, Revision: r.RevisionID}}})
	if err != nil {
		t.Fatal(err)
	}
	var ap catalog.Approval
	if err = json.Unmarshal(v.(json.RawMessage), &ap); err != nil {
		t.Fatal(err)
	}
	reviewClient := registryClient(endpoint, "")
	reviewClient.Token = func() (string, error) { return "test-review-credential", nil }
	v, err = reviewClient.Call(ctx, "publication_approval_decide", catalog.Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Decision: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(v.(json.RawMessage), &ap); err != nil {
		t.Fatal(err)
	}
	args := catalog.Args{ApprovalID: ap.ID, ApprovalRevision: ap.Revision, Hash: ap.ManifestDigest, Candidates: ap.Candidates, IdempotencyKey: "cli-publish"}
	body, _ := json.Marshal(args)
	t.Setenv("REGISTRY_TOKEN", "test-write-credential")
	root := &cobra.Command{Use: "registry"}
	addClientCommands(root)
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetIn(bytes.NewReader(body))
	root.SetArgs([]string{"publish", "--file", "-", "--url", endpoint, "--json"})
	if err = root.Execute(); err != nil {
		t.Fatal(err)
	}
	var receipt catalog.PublicationResult
	if err = json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Records) != 1 || receipt.Records[0].Publication != "published" {
		t.Fatal("CLI publication failed")
	}
	adapter := gmcp.NewServer("publication-test", "test")
	catalog.RegisterMCP(adapter, client.Call)
	srvIn, cliOut := io.Pipe()
	cliIn, srvOut := io.Pipe()
	defer srvIn.Close()
	defer cliOut.Close()
	defer cliIn.Close()
	defer srvOut.Close()
	ss, err := adapter.SDKServer().Connect(ctx, &mcpsdk.IOTransport{Reader: srvIn, Writer: srvOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcpsdk.IOTransport{Reader: cliIn, Writer: cliOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_publication_publish", Arguments: json.RawMessage(body)})
	if err != nil || result.IsError {
		t.Fatalf("MCP replay: %v %v", result, err)
	}
	b, _ := json.Marshal(result.StructuredContent)
	var replay catalog.PublicationResult
	if err = json.Unmarshal(b, &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.ID != receipt.ID {
		t.Fatal("transport changed replay")
	}
	result, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "registry_record_get", Arguments: map[string]any{"id": r.ID}})
	if err != nil || result.IsError {
		t.Fatalf("MCP published read: %v %v", result, err)
	}
	if responseRecord(t, result.StructuredContent).RevisionID != r.RevisionID {
		t.Fatal("MCP read wrong revision")
	}
}

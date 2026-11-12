package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// signingFiles returns the signing key files under the data directory with a digest of each.
func (s *site) signingFiles() map[string]string {
	out := map[string]string{}
	for _, f := range s.dataFiles() {
		if strings.Contains(f, "identity-signing") {
			b, _ := os.ReadFile(filepath.Join(s.dir, "data", f)) //nolint:gosec // a path in the test's temp directory
			out[f] = fmt.Sprintf("%x", sha256.Sum256(b))
		}
	}
	return out
}

// The service and the CLI are two processes on one store. They write at the same time without losing
// a write, and the CLI never makes a second signing key. Then a machine with its own empty data
// directory uses the service as a client (an API key through the environment, and caster login with an
// identity pairing code) and the service, not the local directory, holds what it writes.
func TestServiceAndCLIAreTwoProcessesOnOneStore(t *testing.T) {
	s := newSite(t)
	agent := s.bootstrap("caster.read,caster.write,caster.resolve,caster.observe")
	admin := s.newKey("admin", "*")
	keys := s.signingFiles()
	if len(keys) != 2 { // the key and its public JWKS
		t.Fatalf("setup should leave one signing key (and its JWKS): %v", keys)
	}
	base, stop := s.serve()

	// 8 CLI processes and 8 HTTP calls at once.
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, e, code := s.run("from the cli", nil, "put", "-n", fmt.Sprintf("w/cli%d", i), "-"); code != 0 {
				errs <- "cli put: " + e
			}
		}()
		go func() {
			defer wg.Done()
			if c, b := httpDo(t, "POST", base+"/api/put", agent, fmt.Sprintf(`{"name":"w/http%d","content":"from http"}`, i)); c != 200 {
				errs <- fmt.Sprintf("http put: %d %s", c, b)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	c, body := httpDo(t, "GET", base+"/api/list?ns=w", agent, "")
	var list struct{ Items []struct{ Ref string } }
	if err := json.Unmarshal([]byte(body), &list); c != 200 || err != nil || len(list.Items) != 16 {
		t.Fatalf("the service sees %d of the 16 writes: %d %s", len(list.Items), c, body)
	}
	if out, _, code := s.run("", nil, "ls", "-ns", "w"); code != 0 || strings.Count(out, "\n") != 16 {
		t.Fatalf("the CLI sees a different set than the service: %d\n%s", code, out)
	}
	if after := s.signingFiles(); fmt.Sprint(after) != fmt.Sprint(keys) {
		t.Fatalf("the signing key changed or was added: %v -> %v", keys, after)
	}

	// Client mode through the environment: the service decides what the key may do.
	remote := []string{"CASTER_REMOTE=" + base, "CASTER_TOKEN=" + agent}
	if out, _, code := s.run("", remote, "whoami"); code != 0 || !strings.Contains(out, "caster.write") || strings.Contains(out, "local:operator") {
		t.Fatalf("remote whoami: %d %s", code, out)
	}
	if _, e, code := s.run("", remote, "shadow", "report"); code != 3 {
		t.Fatalf("the agent key has no caster.admin: want exit 3, got %d %s", code, e)
	}
	if _, e, code := s.run("", []string{"CASTER_REMOTE=" + base, "CASTER_TOKEN=" + admin}, "shadow", "report"); code != 0 {
		t.Fatalf("an admin key reads the report: %d %s", code, e)
	}
	if _, _, code := s.run("", []string{"CASTER_REMOTE=" + base, "CASTER_TOKEN=key_wrong"}, "ls"); code != 3 {
		t.Fatalf("a wrong key is refused (exit 3), got %d", code)
	}

	// A second machine: its own empty data directory, paired with an identity pairing code.
	laptop := newSite(t)
	var pc struct{ Code string }
	if code2, body := httpDo(t, "POST", base+"/api/identity/pairing-codes", admin, `{"name":"laptop","scopes":["caster.read","caster.write"],"ttl_minutes":10}`); code2 != 201 || json.Unmarshal([]byte(body), &pc) != nil || pc.Code == "" {
		t.Fatalf("minting a pairing code: %d %s", code2, body)
	}
	if out, e, rc := laptop.run("", nil, "login", "-ref", "file://caster/test-home", base, pc.Code); rc != 0 || strings.Contains(out+e, "key_") {
		t.Fatalf("login: %d\n%s\n%s", rc, out, e) // and the key is never printed
	}
	if _, _, rc := laptop.run("", nil, "login", "-ref", "file://caster/test-home2", base, pc.Code); rc != 3 {
		t.Fatalf("a pairing code works once: want exit 3 on reuse, got %d", rc)
	}
	if out, _, rc := laptop.run("", nil, "remote"); rc != 0 || !strings.Contains(out, "mode:    remote") || !strings.Contains(out, "caster.write") {
		t.Fatalf("remote: %d %s", rc, out)
	}
	if _, e, rc := laptop.run("over the network", nil, "put", "-n", "laptop/note", "-"); rc != 0 {
		t.Fatalf("put from the laptop: %s", e)
	}
	if c, b := httpDo(t, "GET", base+"/api/get?ref=laptop/note", agent, ""); c != 200 || !strings.Contains(b, "over the network") {
		t.Fatalf("the service does not hold the laptop's write: %d %s", c, b)
	}
	if files := laptop.dataFiles(); len(files) != 0 {
		t.Fatalf("a client machine must not create a local store: %v", files)
	}
	if out, _, rc := laptop.run("", nil, "logout"); rc != 0 || !strings.Contains(out, "forgot") {
		t.Fatalf("logout: %d %s", rc, out)
	}

	// The remote stdio server: the agent holds exactly the key's scopes.
	mc, _ := laptop.mcp("CASTER_REMOTE="+base, "CASTER_TOKEN="+agent)
	if _, isErr, text := mc.tool("caster_put", map[string]any{"content": "via mcp", "name": "w/mcp"}); isErr || !strings.Contains(text, "caster://w/mcp@1") {
		t.Fatalf("remote mcp put: %v %s", isErr, text)
	}
	if _, isErr, text := mc.tool("caster_shadow_report", map[string]any{}); !isErr || !strings.Contains(text, `"code":"denied"`) {
		t.Fatalf("the agent key has no caster.admin: %v %s", isErr, text)
	}

	if err := stop(); err != nil {
		t.Fatalf("SIGTERM must end the server cleanly: %v", err)
	}
}

// /mcp on the service: the same tools for a key that holds caster.mcp, then the per-tool scope.
func TestMCPOverHTTP(t *testing.T) {
	s := newSite(t)
	s.bootstrap("caster.read")
	noMCP := s.newKey("nomcp", "caster.read,caster.write")
	withMCP := s.newKey("withmcp", "caster.mcp,caster.read,caster.write")
	base, _ := s.serve()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	post := func(key, body, session string) (int, string, string) {
		req, _ := http.NewRequestWithContext(context.Background(), "POST", base+"/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key)
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Mcp-Session-Id")
	}
	if c, b, _ := post(noMCP, init, ""); c != 403 {
		t.Fatalf("a key without caster.mcp must be refused at /mcp: %d %s", c, b)
	}
	c, b, session := post(withMCP, init, "")
	if c != 200 || !strings.Contains(b, `"caster"`) {
		t.Fatalf("initialize: %d %s", c, b)
	}
	post(withMCP, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, session)
	call := func(name, args string) string {
		_, b, _ := post(withMCP, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`, session)
		return b
	}
	if b := call("caster_put", `{"content":"over mcp http","name":"h/a"}`); !strings.Contains(b, "caster://h/a@1") {
		t.Fatalf("put over /mcp: %s", b)
	}
	// The key holds read and write but not admin: the tool refuses, as the route would.
	if b := call("caster_shadow_report", `{}`); !strings.Contains(b, `"isError":true`) {
		t.Fatalf("an admin tool over /mcp needs caster.admin: %s", b)
	}
}

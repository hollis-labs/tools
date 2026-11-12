package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tools/caster/internal/mcptools"
)

// stdioClient drives `caster mcp` over its stdin and stdout.
type stdioClient struct {
	t     *testing.T
	in    io.WriteCloser
	lines chan string
	next  int
	cmd   *exec.Cmd
}

// syncBuffer is a buffer a process writes while the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *site) mcp(extraEnv ...string) (*stdioClient, *syncBuffer) {
	s.t.Helper()
	cmd := exec.Command(s.bin, "mcp") //nolint:gosec // the binary this test just built
	cmd.Env = append(append([]string{}, s.env...), extraEnv...)
	in, err := cmd.StdinPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	c := &stdioClient{t: s.t, in: in, lines: make(chan string, 64), cmd: cmd}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 32<<20)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	s.t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	c.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "0"}})
	c.notify("notifications/initialized")
	return c, stderr
}

func (c *stdioClient) send(m map[string]any) {
	b, _ := json.Marshal(m)
	if _, err := fmt.Fprintf(c.in, "%s\n", b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *stdioClient) notify(method string) {
	c.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// call sends a request and returns the result object of its reply.
func (c *stdioClient) call(method string, params any) map[string]any {
	c.t.Helper()
	res, rpcErr := c.try(method, params)
	if rpcErr != nil {
		c.t.Fatalf("%s: rpc error %v", method, rpcErr)
	}
	return res
}

// try sends a request and returns the reply's result or its JSON-RPC error.
func (c *stdioClient) try(method string, params any) (result, rpcErr map[string]any) {
	c.t.Helper()
	c.next++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params})
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				c.t.Fatalf("the server closed stdout before answering %s", method)
			}
			var m struct {
				ID     *int           `json:"id"`
				Result map[string]any `json:"result"`
				Error  map[string]any `json:"error"`
			}
			if json.Unmarshal([]byte(line), &m) != nil || m.ID == nil || *m.ID != c.next {
				continue
			}
			return m.Result, m.Error
		case <-time.After(20 * time.Second):
			c.t.Fatalf("no reply to %s", method)
		}
	}
}

// tool calls a tool and returns its structured result and whether it reported an error.
func (c *stdioClient) tool(name string, args map[string]any) (res map[string]any, isError bool, text string) {
	c.t.Helper()
	r := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	isError, _ = r["isError"].(bool)
	res, _ = r["structuredContent"].(map[string]any)
	if content, ok := r["content"].([]any); ok && len(content) > 0 {
		text, _ = content[0].(map[string]any)["text"].(string)
	}
	return res, isError, text
}

func TestMCPStdioLocal(t *testing.T) {
	s := newSite(t)
	c, stderr := s.mcp()

	// Every tool has its scope in the table (an unmapped tool is refused by the guard on /mcp), and
	// every table entry is a tool the server lists.
	listed := c.call("tools/list", map[string]any{})["tools"].([]any)
	var names []string
	for _, tl := range listed {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	var want []string
	for n := range mcptools.ToolScopes {
		want = append(want, n)
	}
	sort.Strings(names)
	sort.Strings(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tools/list = %v, scope table = %v", names, want)
	}

	if _, isErr, text := c.tool("caster_put", map[string]any{"content": "hello", "name": "t/a"}); isErr || !strings.Contains(text, "caster://t/a@1") {
		t.Fatalf("put: %v %s", isErr, text)
	}
	if res, isErr, _ := c.tool("caster_get", map[string]any{"ref": "t/a"}); isErr || res["content"] != "hello" {
		t.Fatalf("get: %v %v", isErr, res)
	}
	// A missing ref is an outcome, not an error.
	if res, isErr, _ := c.tool("caster_get", map[string]any{"ref": "t/missing"}); isErr || res["state"] != "not_found" {
		t.Fatalf("a missing ref must be an outcome: %v %v", isErr, res)
	}
	// Tags may arrive as a list or as a string.
	c.tool("caster_put", map[string]any{"content": "a", "name": "t/list", "tags": []string{"x", "y"}})
	c.tool("caster_put", map[string]any{"content": "b", "name": "t/str", "tags": "x y"})
	if _, isErr, text := c.tool("caster_ls", map[string]any{"tag": "y"}); isErr || !strings.Contains(text, "t/list") || !strings.Contains(text, "t/str") {
		t.Fatalf("both tag forms should be found by tag: %v %s", isErr, text)
	}
	if _, isErr, text := c.tool("caster_search", map[string]any{"query": `"unterminated`}); !isErr || !strings.Contains(text, `"code":"invalid"`) {
		t.Fatalf("a malformed query is an invalid error: %v %s", isErr, text)
	}
	if res, _, _ := c.tool("caster_whoami", map[string]any{}); res["id"] != "local:operator" {
		t.Fatalf("whoami: %v", res)
	}
	// The local principal never holds act or admin: those tools are refused, not run.
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"caster_act", map[string]any{"action": "save-handoff", "ref": "t/a", "context": map[string]any{}}},
		{"caster_shadow_report", map[string]any{}},
		{"caster_shadow_label", map[string]any{"ref": "t/a", "verdict": "true"}},
	} {
		if _, isErr, text := c.tool(tc.tool, tc.args); !isErr || !strings.Contains(text, `"code":"denied"`) {
			t.Fatalf("%s under local trust must be denied: %v %s", tc.tool, isErr, text)
		}
	}
	if _, rpcErr := c.try("tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}}); rpcErr == nil {
		t.Fatal("an unknown tool must be an error")
	}
	if !strings.Contains(stderr.String(), "local:operator") {
		t.Fatalf("the local-trust notice is missing from stderr: %q", stderr.String())
	}
	for _, f := range s.dataFiles() {
		if strings.Contains(f, "identity-signing") {
			t.Fatalf("the stdio server created the signing key: %v", s.dataFiles())
		}
	}
}

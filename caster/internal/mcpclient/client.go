// Package mcpclient is a minimal MCP stdio client: it launches a server
// process, performs the initialize handshake and calls tools. It exists so
// resolvers can reach services (Tesseract) that only speak MCP, using the
// launch command the operator already configured. One process serves many calls.
package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ToolError is a tool that ran and reported isError.
type ToolError struct {
	Tool string
	Body json.RawMessage
}

func (e *ToolError) Error() string { return fmt.Sprintf("%s: %s", e.Tool, string(e.Body)) }

// Client talks to one MCP server process. Calls are serialised.
type Client struct {
	command string
	args    []string
	env     []string

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan []byte
	next  int
}

// New returns a client; the process starts on first Call.
func New(command string, args []string, env []string) *Client {
	return &Client{command: command, args: args, env: env}
}

type rpcMsg struct {
	ID     *int            `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) start(ctx context.Context) error {
	cmd := exec.Command(c.command, c.args...)
	cmd.Env = append(os.Environ(), c.env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", c.command, err)
	}
	c.cmd, c.stdin, c.lines = cmd, in, make(chan []byte, 16)
	go func() {
		defer close(c.lines)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 32<<20)
		for sc.Scan() {
			if b := sc.Bytes(); len(b) > 0 && b[0] == '{' { // servers may print banners
				c.lines <- append([]byte(nil), b...)
			}
		}
	}()
	if _, err := c.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "caster", "version": "0"},
	}); err != nil {
		c.kill()
		return fmt.Errorf("mcp initialize: %w", err)
	}
	return c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func (c *Client) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.next++
	id := c.next
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case line, ok := <-c.lines:
			if !ok {
				return nil, errors.New("mcp server exited")
			}
			var m rpcMsg
			if json.Unmarshal(line, &m) != nil || m.ID == nil || *m.ID != id {
				continue // notification or unrelated
			}
			if m.Error != nil {
				return nil, fmt.Errorf("mcp %s: %s", method, m.Error.Message)
			}
			return m.Result, nil
		}
	}
}

func (c *Client) kill() {
	if c.cmd != nil {
		_ = c.stdin.Close()
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
		c.cmd = nil
	}
}

// Call invokes a tool and returns its structured content (or the text body when
// the server sent none). A tool-level error is returned as *ToolError.
func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		if err := c.start(ctx); err != nil {
			return nil, err
		}
	}
	res, err := c.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		c.kill() // desynced or dead: next call starts fresh
		return nil, err
	}
	var r struct {
		IsError           bool            `json:"isError"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		Content           []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	body := r.StructuredContent
	if len(body) == 0 && len(r.Content) > 0 {
		body = json.RawMessage(r.Content[0].Text)
	}
	if r.IsError {
		return nil, &ToolError{Tool: tool, Body: body}
	}
	return body, nil
}

// Close stops the server process.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kill()
	return nil
}

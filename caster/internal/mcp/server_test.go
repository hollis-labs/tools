package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/caster/internal/service"
	"github.com/hollis-labs/caster/internal/store"
)

func TestServePutGetRoundTrip(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"caster_put","arguments":{"content":"hello","name":"t/a"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"caster_get","arguments":{"ref":"t/a"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"caster_get","arguments":{"ref":"t/missing"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), service.New(st), strings.NewReader(in), &out, "test"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 { // the notification gets no reply
		t.Fatalf("want 5 replies, got %d:\n%s", len(lines), out.String())
	}
	var get struct {
		Result struct{ StructuredContent map[string]any }
	}
	_ = json.Unmarshal([]byte(lines[2]), &get)
	if get.Result.StructuredContent["content"] != "hello" {
		t.Fatalf("round trip failed: %s", lines[2])
	}
	if !strings.Contains(lines[3], `"not_found"`) || strings.Contains(lines[3], "isError") {
		t.Fatalf("missing ref must be an outcome, not an error: %s", lines[3])
	}
	if !strings.Contains(lines[4], `"isError":true`) {
		t.Fatalf("unknown tool must be an error: %s", lines[4])
	}
}

func TestTagsMayArriveAsAListOrAString(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"caster_put","arguments":{"content":"a","name":"t/list","tags":["x","y"]}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"caster_put","arguments":{"content":"b","name":"t/str","tags":"x y"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"caster_ls","arguments":{"tag":"y"}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := Serve(context.Background(), service.New(st), strings.NewReader(in), &out, "test"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for _, l := range lines[1:3] {
		if strings.Contains(l, `"isError":true`) {
			t.Fatalf("put with tags failed: %s", l)
		}
	}
	if !strings.Contains(lines[3], "t/list") || !strings.Contains(lines[3], "t/str") {
		t.Fatalf("both tag forms should be searchable by tag: %s", lines[3])
	}
}

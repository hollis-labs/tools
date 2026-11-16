package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/localinvocation/entry"
	"github.com/hollis-labs/tools/localinvocation/internal/cli"
	"github.com/hollis-labs/tools/localinvocation/invocation"
)

// This test runs as a wrapped child, then launches another wrapped child.
func TestWrappedHelper(t *testing.T) {
	switch os.Getenv("LOCALINVOCATION_TEST_DEPTH") {
	case "":
		return
	case "1":
		if err := os.Setenv("LOCALINVOCATION_TEST_DEPTH", "2"); err != nil {
			t.Fatal(err)
		}
		argv := []string{os.Args[0], "-test.run=TestWrappedHelper"}
		command, record, err := entry.Terminal(argv, invocation.Options{})
		if err != nil {
			t.Fatal(err)
		}
		code, err := cli.Execute(context.Background(), command, record, os.Getenv(invocation.StoreEnv), os.Stdout, os.Stderr)
		if err != nil || code != 0 {
			t.Fatalf("nested execution: code=%d err=%v", code, err)
		}
		os.Exit(0)
	case "2":
		fmt.Println("nested-child-ok")
		os.Exit(0)
	}
}

func TestWrappedParentChildTree(t *testing.T) {
	store := filepath.Join(t.TempDir(), "journal", "records.jsonl")
	t.Setenv(invocation.StoreEnv, store)
	t.Setenv("LOCALINVOCATION_TEST_DEPTH", "1")
	argv := []string{os.Args[0], "-test.run=TestWrappedHelper"}
	command, record, err := entry.Terminal(argv, invocation.Options{SessionID: "session-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code, err := cli.Execute(context.Background(), command, record, store, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("root execution: code=%d err=%v stderr=%s", code, err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "nested-child-ok") {
		t.Fatalf("child output missing: %q", stdout.String())
	}
	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "LOCALINVOCATION_TEST_DEPTH") {
		t.Fatal("environment value or name leaked into journal")
	}
	var records []invocation.Record
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var rec invocation.Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("want two records, got %d: %s", len(records), data)
	}
	root, child := records[0], records[1]
	if root.IDs.CallID != root.IDs.RootCallID || root.IDs.ParentCallID != "" {
		t.Fatalf("bad root: %+v", root.IDs)
	}
	if child.IDs.ParentCallID != root.IDs.CallID || child.IDs.RootCallID != root.IDs.CallID || child.IDs.SessionID != root.IDs.SessionID {
		t.Fatalf("bad child: %+v", child.IDs)
	}
}

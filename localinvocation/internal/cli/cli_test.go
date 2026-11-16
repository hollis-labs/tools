package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
		code, err := cli.Execute(context.Background(), command, record, os.Getenv(invocation.StoreEnv), os.Stdin, os.Stdout, os.Stderr)
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
	code, err := cli.Execute(context.Background(), command, record, store, os.Stdin, &stdout, &stderr)
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

func TestPipedStdinReachesWrappedCommand(t *testing.T) {
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("cat is unavailable")
	}
	store := filepath.Join(t.TempDir(), "records.jsonl")
	command, record, err := entry.Terminal([]string{cat}, invocation.Options{Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	input := "wrapped input fixture\n"
	var stdout, stderr bytes.Buffer
	code, err := cli.Execute(context.Background(), command, record, store, strings.NewReader(input), &stdout, &stderr)
	if err != nil || code != 0 || stdout.String() != input {
		t.Fatalf("stdin lost: code=%d err=%v stdout=%q stderr=%q", code, err, stdout.String(), stderr.String())
	}
	journal, err := os.ReadFile(store)
	if err != nil || bytes.Contains(journal, []byte(input)) {
		t.Fatalf("stdin leaked to journal: %q %v", journal, err)
	}
}

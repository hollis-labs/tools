package tree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type fixture struct {
	Root            string            `json:"root"`
	Children        map[string]string `json:"children"`
	RetryAttempt    uint32            `json:"retry_attempt"`
	ReconnectFirst  string            `json:"reconnect_first"`
	ReconnectSecond string            `json:"reconnect_second"`
}

func TestWrappedInvocationTree(t *testing.T) {
	root := t.TempDir()
	bin, targets := filepath.Join(root, "bin"), filepath.Join(root, "targets")
	for _, dir := range []string{bin, targets} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	moduleRoot := filepath.Join("..", "..")
	for _, name := range []string{"localinvocation", "localinvocation-shim"} {
		build := exec.Command("go", "build", "-o", filepath.Join(bin, name), "./cmd/"+name)
		build.Dir = moduleRoot
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", name, err, out)
		}
	}
	for name, body := range map[string]string{
		"wrapped-ok":   "#!/bin/sh\nprintf child-ok",
		"wrapped-fail": "#!/bin/sh\nprintf child-fail >&2\nexit 7",
	} {
		if err := os.WriteFile(filepath.Join(targets, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(bin, "localinvocation-shim"), filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(root, "records.jsonl")
	process := exec.Command(filepath.Join(bin, "localinvocation"), "run", "--store", store, "--", "sh", "-c", "wrapped-ok; wrapped-fail; test $? -eq 7")
	process.Env = append(os.Environ(), "PATH="+bin+":"+targets+":"+os.Getenv("PATH"))
	if out, err := process.CombinedOutput(); err != nil {
		t.Fatalf("wrapped root: %v: %s", err, out)
	}

	records := readJournal(t, store)
	if len(records) != 3 {
		t.Fatalf("want root and two shim children, got %d records", len(records))
	}
	var parent invocation.Record
	wrapped := map[string]invocation.Record{}
	for _, record := range records {
		if record.IDs.ParentCallID == "" {
			parent = record
		} else {
			wrapped[filepath.Base(record.Argv[0])] = record
		}
	}
	if parent.IDs.CallID == "" || len(wrapped) != 2 {
		t.Fatalf("invalid wrapped tree: parent=%+v children=%d", parent.IDs, len(wrapped))
	}
	for _, child := range wrapped {
		assertChild(t, parent.IDs, child.IDs)
	}

	runs := filepath.Join(root, "runs")
	m, err := runner.New(runs, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed := fixture{Children: map[string]string{}}
	schema := resultSchema(t)
	observed.Root = outcome(t, m, schema, parent.IDs.AttemptID)
	observed.Children["wrapped_success"] = outcome(t, m, schema, wrapped["wrapped-ok"].IDs.AttemptID)
	observed.Children["wrapped_nonzero"] = outcome(t, m, schema, wrapped["wrapped-fail"].IDs.AttemptID)

	prepare := func(argv []string, callID string, attempt uint32) invocation.Command {
		t.Helper()
		command, record, err := invocation.Prepare(invocation.Options{Argv: argv, CWD: root, Parent: &parent.IDs, CallID: callID, Attempt: attempt})
		if err != nil {
			t.Fatal(err)
		}
		assertChild(t, parent.IDs, record.IDs)
		if err := (invocation.FileRecorder{Path: store}).Append(record); err != nil {
			t.Fatal(err)
		}
		return command
	}
	start := func(command invocation.Command, opts runner.Options) string {
		t.Helper()
		id, err := m.Start(context.Background(), command, opts)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	timeout := prepare([]string{"sh", "-c", "sleep 10"}, "", 1)
	timeoutID := start(timeout, runner.Options{Timeout: 40 * time.Millisecond})
	if _, err := m.Wait(timeoutID); err != nil {
		t.Fatal(err)
	}
	observed.Children["timeout"] = outcome(t, m, schema, timeoutID)

	cancel := prepare([]string{"sh", "-c", "printf first; sleep 10"}, "", 1)
	cancelID := start(cancel, runner.Options{})
	waitForOutput(t, m, cancelID, "first")
	if err := m.Cancel(cancelID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wait(cancelID); err != nil {
		t.Fatal(err)
	}
	observed.Children["cancel"] = outcome(t, m, schema, cancelID)

	lost := prepare([]string{"sh", "-c", "sleep .2; printf maybe"}, "", 1)
	lostID := start(lost, runner.Options{})
	reopened, err := runner.New(runs, nil)
	if err != nil {
		t.Fatal(err)
	}
	observed.Children["lost"] = outcome(t, reopened, schema, lostID)
	if _, err := m.Wait(lostID); err != nil {
		t.Fatal(err)
	}
	retry := prepare([]string{"sh", "-c", "printf retried"}, lost.IDs.CallID, 2)
	if retry.IDs.AttemptID == lost.IDs.AttemptID || retry.IDs.CallID != lost.IDs.CallID {
		t.Fatal("retry did not create a new attempt for the logical call")
	}
	observed.RetryAttempt = retry.IDs.Attempt
	retryID := start(retry, runner.Options{})
	if _, err := m.Wait(retryID); err != nil {
		t.Fatal(err)
	}
	observed.Children["retry"] = outcome(t, m, schema, retryID)

	reconnect := prepare([]string{"sh", "-c", "printf abc; sleep .1; printf def"}, "", 1)
	reconnectID := start(reconnect, runner.Options{})
	first := waitForOutput(t, m, reconnectID, "abc")
	if _, err := m.Wait(reconnectID); err != nil {
		t.Fatal(err)
	}
	reopened, err = runner.New(runs, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reopened.Observe(reconnectID, "stdout", first.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	observed.ReconnectFirst, observed.ReconnectSecond = string(first.Data), string(second.Data)
	observed.Children["reconnect"] = outcome(t, reopened, schema, reconnectID)

	wantBytes, err := os.ReadFile("expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var want fixture
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatal(err)
	}
	gotBytes, _ := json.Marshal(observed)
	wantNormalized, _ := json.Marshal(want)
	if !bytes.Equal(gotBytes, wantNormalized) {
		t.Fatalf("fixture mismatch\n got: %s\nwant: %s", gotBytes, wantNormalized)
	}
}

func readJournal(t *testing.T, path string) []invocation.Record {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []invocation.Record
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var record invocation.Record
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func assertChild(t *testing.T, root, child invocation.IDs) {
	t.Helper()
	if child.RootCallID != root.CallID || child.ParentCallID != root.CallID || child.SessionID != root.SessionID {
		t.Fatalf("child identity %+v does not descend from %+v", child, root)
	}
}

func waitForOutput(t *testing.T, m *runner.Manager, id, expected string) runner.OutputPage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page, err := m.Observe(id, "stdout", 0, len(expected))
		if err != nil {
			t.Fatal(err)
		}
		if string(page.Data) == expected {
			return page
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("output %q did not appear", expected)
	return runner.OutputPage{}
}

func resultSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.NewCompiler().Compile(filepath.Join("..", "..", "runner", "testdata", "resultenv.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func outcome(t *testing.T, m *runner.Manager, schema *jsonschema.Schema, id string) string {
	t.Helper()
	raw, err := m.Envelope(id, 64)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("resultenv schema: %v: %s", err, raw)
	}
	got, ok := value["outcome"].(string)
	if !ok || strings.TrimSpace(got) == "" {
		t.Fatal(fmt.Errorf("missing outcome in %s", raw))
	}
	return got
}

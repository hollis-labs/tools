package invocation_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tools/localinvocation/invocation"
)

type fixtureCase struct {
	Argv           []string `json:"argv"`
	CWD            string   `json:"cwd"`
	EnvironmentRef string   `json:"environment_ref"`
	SessionID      string   `json:"session_id"`
	Attempt        uint32   `json:"attempt"`
}

func TestTreeRetryAndPrivateJournal(t *testing.T) {
	data, err := os.ReadFile("testdata/tree.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]fixtureCase
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	secret := "SECRET_SHOULD_NOT_ENTER_INVOCATION_JOURNAL"
	common := invocation.Options{Argv: fixture["root"].Argv, CWD: fixture["root"].CWD, EnvironmentRef: fixture["root"].EnvironmentRef, SessionID: fixture["root"].SessionID, Environ: []string{"API_KEY=" + secret}, Now: time.Unix(100, 0)}
	root, rootRecord, err := invocation.Prepare(common)
	if err != nil {
		t.Fatal(err)
	}
	childOpts := invocation.Options{Argv: fixture["child"].Argv, CWD: fixture["child"].CWD, Environ: append(invocation.ChildEnv(root.IDs, root.EnvironmentRef), "API_KEY="+secret)}
	child, childRecord, err := invocation.Prepare(childOpts)
	if err != nil {
		t.Fatal(err)
	}
	retryOpts := common
	retryOpts.CallID = root.IDs.CallID
	retryOpts.Attempt = fixture["retry"].Attempt
	retry, retryRecord, err := invocation.Prepare(retryOpts)
	if err != nil {
		t.Fatal(err)
	}
	if root.IDs.RootCallID != root.IDs.CallID || root.IDs.ParentCallID != "" {
		t.Fatalf("bad root: %+v", root.IDs)
	}
	if child.IDs.RootCallID != root.IDs.CallID || child.IDs.ParentCallID != root.IDs.CallID || child.IDs.SessionID != root.IDs.SessionID {
		t.Fatalf("bad child: %+v", child.IDs)
	}
	if child.EnvironmentRef != root.EnvironmentRef {
		t.Fatalf("child lost environment reference: %q", child.EnvironmentRef)
	}
	if retry.IDs.CallID != root.IDs.CallID || retry.IDs.AttemptID == root.IDs.AttemptID || retry.IDs.Attempt != 2 {
		t.Fatalf("bad retry: %+v", retry.IDs)
	}
	if !reflect.DeepEqual(root.Argv, fixture["root"].Argv) || !reflect.DeepEqual(retry.Argv, fixture["retry"].Argv) {
		t.Fatal("argv was altered")
	}
	path := filepath.Join(t.TempDir(), "private", "records.jsonl")
	store := invocation.FileRecorder{Path: path}
	for _, rec := range []invocation.Record{rootRecord, childRecord, retryRecord} {
		if err := store.Append(rec); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), secret) {
		t.Fatal("environment value leaked to journal")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("journal mode: %v %v", info, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	var records []invocation.Record
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
	if len(records) != 3 || !reflect.DeepEqual(records[0].Argv, fixture["root"].Argv) || records[1].IDs.ParentCallID != records[0].IDs.CallID {
		t.Fatalf("journal tree mismatch: %+v", records)
	}
}

func TestConcurrentJournalAppend(t *testing.T) {
	store := invocation.FileRecorder{Path: filepath.Join(t.TempDir(), "records.jsonl")}
	const count = 30
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, rec, err := invocation.Prepare(invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}})
			if err != nil {
				t.Error(err)
				return
			}
			if err := store.Append(rec); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	contents, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(contents), "\n"); got != count {
		t.Fatalf("got %d records, want %d", got, count)
	}
}

func TestJournalRejectsSharedExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.jsonl")
	if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, record, err := invocation.Prepare(invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := (invocation.FileRecorder{Path: path}).Append(record); err == nil {
		t.Fatal("shared journal accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original\n" {
		t.Fatalf("existing data changed: %q, %v", data, err)
	}
}

func TestGateReferencesAreOptionalAndCopied(t *testing.T) {
	_, off, err := invocation.Prepare(invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if off.Gate != nil {
		t.Fatal("gate unexpectedly enabled")
	}
	refs := &invocation.GateReferences{PreparedOperationRef: "prepared:1", DecisionRef: "decision:1", ReceiptRef: "receipt:1"}
	cmd, on, err := invocation.Prepare(invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}, Gate: refs})
	if err != nil {
		t.Fatal(err)
	}
	refs.DecisionRef = "changed"
	if on.Gate.DecisionRef != "decision:1" || cmd.Gate.DecisionRef != "decision:1" {
		t.Fatal("gate reference mutated after preparation")
	}
	cmd.Gate.DecisionRef = "command-changed"
	if on.Gate.DecisionRef != "decision:1" {
		t.Fatal("record shared mutable gate references with runner command")
	}
}

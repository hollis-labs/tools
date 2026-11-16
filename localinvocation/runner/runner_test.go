package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tools/localinvocation/invocation"
)

type request struct {
	Argv    []string
	EnvRef  string
	PTY     bool
	Rows    uint16
	Cols    uint16
	Timeout time.Duration
}

func manager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.grace = 30 * time.Millisecond
	return m
}

func start(t *testing.T, m *Manager, req request) string {
	t.Helper()
	command, _, err := invocation.Prepare(invocation.Options{Argv: req.Argv, EnvironmentRef: req.EnvRef})
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Start(context.Background(), command, Options{PTY: req.PTY, Rows: req.Rows, Cols: req.Cols, Timeout: req.Timeout})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func finished(t *testing.T, m *Manager, id string) Result {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		r, err := m.Result(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Done {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return Result{}
}

func output(t *testing.T, m *Manager, id, stream string) []byte {
	t.Helper()
	var all []byte
	var cursor int64
	for {
		p, err := m.Observe(id, stream, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p.Data...)
		cursor = p.NextCursor
		if !p.Truncated {
			return all
		}
	}
}

func TestExitStreamsAndSignal(t *testing.T) {
	m := manager(t)
	id := start(t, m, request{Argv: []string{"sh", "-c", "printf out; printf err >&2; exit 7"}})
	r := finished(t, m, id)
	if r.Outcome.Kind != OutcomeExit || r.Outcome.ExitCode == nil || *r.Outcome.ExitCode != 7 {
		t.Fatalf("outcome: %+v", r.Outcome)
	}
	if got := string(output(t, m, id, "stdout")); got != "out" {
		t.Fatalf("stdout %q", got)
	}
	if got := string(output(t, m, id, "stderr")); got != "err" {
		t.Fatalf("stderr %q", got)
	}
	id = start(t, m, request{Argv: []string{"sh", "-c", "kill -TERM $$"}})
	r = finished(t, m, id)
	if r.Outcome.Kind != OutcomeSignal || !strings.Contains(strings.ToLower(r.Outcome.Signal), "terminated") {
		t.Fatalf("signal: %+v", r.Outcome)
	}
}

func TestTimeoutAndCancellationDuringOutput(t *testing.T) {
	m := manager(t)
	id := start(t, m, request{Argv: []string{"sh", "-c", "printf before; sleep 10"}, Timeout: 50 * time.Millisecond})
	r := finished(t, m, id)
	if r.Outcome.Kind != OutcomeTimeout {
		t.Fatalf("timeout: %+v", r.Outcome)
	}
	if string(output(t, m, id, "stdout")) != "before" {
		t.Fatal("timeout lost output")
	}
	id = start(t, m, request{Argv: []string{"sh", "-c", "printf first; sleep 10"}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p, err := m.Observe(id, "stdout", 0, 32)
		if err != nil {
			t.Fatal(err)
		}
		if string(p.Data) == "first" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	r = finished(t, m, id)
	if r.Outcome.Kind != OutcomeCancelled || !r.Outcome.CancelRequested || !r.Outcome.CancelConfirmed {
		t.Fatalf("cancel: %+v", r.Outcome)
	}
	if string(output(t, m, id, "stdout")) != "first" {
		t.Fatal("cancel lost output")
	}
}

func TestReconnectFromByteCursor(t *testing.T) {
	m := manager(t)
	id := start(t, m, request{Argv: []string{"sh", "-c", "printf abc; sleep .1; printf def"}})
	var first OutputPage
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var err error
		first, err = m.Observe(id, "stdout", 0, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Data) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if string(first.Data) != "abc" {
		t.Fatalf("first page %q", first.Data)
	}
	_ = finished(t, m, id)
	// A new client keeps only the cursor and reconnects after the old one dies.
	second, err := m.Observe(id, "stdout", first.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(first.Data, second.Data...), []byte("abcdef")) {
		t.Fatalf("replay %q + %q", first.Data, second.Data)
	}
	if second.NextCursor != 6 {
		t.Fatalf("cursor %d", second.NextCursor)
	}
	reopened, err := New(m.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	third, err := reopened.Observe(id, "stdout", first.NextCursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(third.Data, second.Data) {
		t.Fatalf("reopened replay %q", third.Data)
	}
}

func TestRunConvenience(t *testing.T) {
	m := manager(t)
	command, _, err := invocation.Prepare(invocation.Options{Argv: []string{"sh", "-c", "exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := m.Run(context.Background(), command, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Done || r.Outcome.Kind != OutcomeExit || r.Outcome.ExitCode == nil || *r.Outcome.ExitCode != 0 {
		t.Fatalf("run %+v", r)
	}
	envelope, err := m.Envelope(r.ID, 128)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, sharedSchema(t), envelope)
}

func TestPTYResizeAndCombinedLabel(t *testing.T) {
	m := manager(t)
	id := start(t, m, request{Argv: []string{"sh", "-c", "stty size; sleep .2; stty size; printf error >&2"}, PTY: true, Rows: 20, Cols: 40})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p, err := m.Observe(id, "combined", 0, 128)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(p.Data, []byte("20 40")) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.Resize(id, 50, 90); err != nil {
		t.Fatal(err)
	}
	r := finished(t, m, id)
	if r.Outcome.Kind != OutcomeExit {
		t.Fatalf("PTY outcome %+v", r.Outcome)
	}
	p, err := m.Observe(id, "combined", 0, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Combined || !bytes.Contains(p.Data, []byte("20 40")) || !bytes.Contains(p.Data, []byte("50 90")) || !bytes.Contains(p.Data, []byte("error")) {
		t.Fatalf("combined output %q", p.Data)
	}
	if _, err := m.Observe(id, "stderr", 0, 128); err == nil {
		t.Fatal("PTY unexpectedly exposes separate stderr")
	}
}

func TestLostFinalResultIsUnknown(t *testing.T) {
	m := manager(t)
	id := "lost_result"
	if err := os.Mkdir(m.runPath(id), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.writeRecord(id, diskRecord{Command: invocation.Command{IDs: invocation.IDs{AttemptID: id}}, Result: Result{ID: id, OutputRef: map[string]string{}}}); err != nil {
		t.Fatal(err)
	}
	r, err := m.Result(id)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Done || r.Outcome.Kind != OutcomeUnknown {
		t.Fatalf("lost result %+v", r)
	}
	if _, err := os.Stat(filepath.Join(m.runPath(id), "record.json")); err != nil {
		t.Fatal(err)
	}
	// An attempt ID is single-use even when its outcome is unknown.
	command, _, err := invocation.Prepare(invocation.Options{Argv: []string{"sh", "-c", "exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	command.IDs.AttemptID = id
	if _, err := m.Start(context.Background(), command, Options{}); err == nil {
		t.Fatal("lost attempt ran twice")
	}
}

func TestCancelRequestedWithoutConfirmed(t *testing.T) {
	m := manager(t)
	id := start(t, m, request{Argv: []string{"sh", "-c", "trap 'exit 0' TERM; printf ready; sleep 10"}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		p, err := m.Observe(id, "stdout", 0, 32)
		if err != nil {
			t.Fatal(err)
		}
		if string(p.Data) == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	r := finished(t, m, id)
	if !r.Outcome.CancelRequested || r.Outcome.CancelConfirmed {
		t.Fatalf("cancel confirmation %+v", r.Outcome)
	}
}

func TestEnvLeaseValuesNotRecorded(t *testing.T) {
	root := t.TempDir()
	m, err := New(root, func(_ context.Context, ref string) ([]string, error) {
		if ref != "lease_1" {
			t.Fatalf("ref %q", ref)
		}
		return []string{"SECRET=private-value", "PATH=" + os.Getenv("PATH")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	check := fmt.Sprintf("test -n \"$SECRET\" && test \"$%s\" = lease_1", invocation.EnvRefEnv)
	id := start(t, m, request{Argv: []string{"sh", "-c", check}, EnvRef: "lease_1"})
	result := finished(t, m, id)
	if result.Outcome.ExitCode == nil || *result.Outcome.ExitCode != 0 {
		t.Fatalf("environment lease not propagated: %+v", result.Outcome)
	}
	b, err := os.ReadFile(filepath.Join(root, id, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("private-value")) {
		t.Fatal("environment value in record")
	}
	var rec diskRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Command.EnvironmentRef != "lease_1" {
		t.Fatal("lease ref missing")
	}
}

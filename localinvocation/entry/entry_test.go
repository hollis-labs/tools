package entry_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/hollis-labs/tools/localinvocation/entry"
	"github.com/hollis-labs/tools/localinvocation/invocation"
)

func TestThreeEntriesNormalizeIdentically(t *testing.T) {
	argv := []string{"tool", "two words", "quote\"", "雪", ""}
	common := invocation.Options{CWD: "/workspace", EnvironmentRef: "lease:fixture", SessionID: "session", ProviderCallID: "provider-call", CallID: "call", Environ: []string{}, Now: time.Unix(200, 0)}
	_, terminal, err := entry.Terminal(argv, common)
	if err != nil {
		t.Fatal(err)
	}
	_, shim, err := entry.Shim(argv[0], argv[1:], common)
	if err != nil {
		t.Fatal(err)
	}
	hookInput, _ := json.Marshal(map[string]any{"tool_input": map[string]any{"argv": argv, "cwd": common.CWD, "env": map[string]string{"SECRET": "do-not-save"}}, "session_id": common.SessionID, "tool_use_id": common.ProviderCallID, "environment_ref": common.EnvironmentRef})
	_, hook, err := entry.Hook(hookInput, common)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []invocation.Record{shim, hook} {
		if !reflect.DeepEqual(rec.Argv, terminal.Argv) || rec.SourceForm != terminal.SourceForm || rec.CWD != terminal.CWD || rec.EnvironmentRef != terminal.EnvironmentRef || rec.IDs.CallID != terminal.IDs.CallID || rec.IDs.SessionID != terminal.IDs.SessionID || rec.IDs.ProviderCallID != terminal.IDs.ProviderCallID || rec.PreparedAt != terminal.PreparedAt {
			t.Fatalf("entry mismatch: %+v vs %+v", rec, terminal)
		}
	}
}

func TestHookShellStringIsOpaque(t *testing.T) {
	_, rec, err := entry.Hook([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"/workspace","tool_input":{"command":"printf 'a b'"},"session_id":"session","tool_use_id":"provider-call"}`), invocation.Options{Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.Argv, []string{"sh", "-c", "printf 'a b'"}) {
		t.Fatalf("shell script split: %#v", rec.Argv)
	}
	if rec.SourceForm != invocation.FormShellCommand {
		t.Fatalf("shell source form hidden: %q", rec.SourceForm)
	}
	if rec.CWD != "/workspace" || rec.IDs.SessionID != "session" || rec.IDs.ProviderCallID != "provider-call" {
		t.Fatalf("hook fields lost: %+v", rec)
	}
}

func TestPostToolUseIsNotAnotherInvocation(t *testing.T) {
	_, _, err := entry.Hook([]byte(`{"hook_event_name":"PostToolUse","tool_input":{"command":"true"}}`), invocation.Options{CWD: "/tmp", Environ: []string{}})
	if err == nil {
		t.Fatal("post-hook duplicated the pre-hook invocation")
	}
}

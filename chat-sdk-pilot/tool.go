package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/libs/agent/resultenv"
	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

// tool runs fixed demonstration commands. Its preparation journal and runner
// receipts are produced by localinvocation, not inferred from UI text.
func (p *pilot) tool(ctx context.Context, scenario string) (string, any, error) {
	if scenario != "nonzero" && scenario != "lost" {
		return "Use /tool nonzero or /tool lost.", nil, nil
	}
	callID := fmt.Sprintf("tool-%d", time.Now().UnixNano())
	if err := p.message("assistant", callID+"-use", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": callID, "name": "local_command", "input": map[string]any{"scenario": scenario}}}}); err != nil {
		return "", nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	root, rootRecord, err := invocation.Prepare(invocation.Options{Argv: []string{"sh", "-c", "true"}, CWD: cwd, SessionID: string(conversationID), CallID: callID})
	if err != nil {
		return "", nil, err
	}
	journal := invocation.FileRecorder{Path: filepath.Join(p.root, "invocations.jsonl")}
	if err := journal.Append(rootRecord); err != nil {
		return "", nil, err
	}
	if _, err := p.runner.Run(ctx, root, runner.Options{}); err != nil {
		return "", nil, err
	}
	argv := []string{"sh", "-c", "exit 7"}
	if scenario == "lost" {
		argv = []string{"sh", "-c", "sleep 1"}
	}
	child, childRecord, err := invocation.Prepare(invocation.Options{Argv: argv, CWD: cwd, SessionID: string(conversationID), Parent: &root.IDs})
	if err != nil {
		return "", nil, err
	}
	if err := journal.Append(childRecord); err != nil {
		return "", nil, err
	}
	if scenario == "lost" {
		if _, err := p.runner.Start(ctx, child, runner.Options{}); err != nil {
			return "", nil, err
		}
	} else {
		if _, err := p.runner.Run(ctx, child, runner.Options{}); err != nil {
			return "", nil, err
		}
	}
	observed := p.runner
	if scenario == "lost" {
		// An observer that lost the live manager sees an unknown result until it
		// can reconcile. It must not convert that uncertainty to a failed exit.
		observed, err = runner.New(filepath.Join(p.root, "runs"), nil)
		if err != nil {
			return "", nil, err
		}
	}
	raw, err := observed.Envelope(child.IDs.AttemptID, 64<<10)
	if err != nil {
		return "", nil, err
	}
	var envelope resultenv.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", nil, err
	}
	if err := envelope.Validate(); err != nil {
		return "", nil, err
	}
	if err := p.message("user", callID+"-result", map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": callID, "content": string(raw), "is_error": envelope.Outcome != resultenv.Success}}}); err != nil {
		return "", nil, err
	}
	answer := fmt.Sprintf("Tool outcome: %s", envelope.Outcome)
	return answer, map[string]any{"tool_call_id": callID, "attempt_id": child.IDs.AttemptID, "root_call_id": child.IDs.RootCallID, "parent_call_id": child.IDs.ParentCallID, "envelope": envelope}, nil
}

// Package entry normalizes terminal, PATH shim, and provider exec-hook inputs
// into the same invocation contract. Execution is delegated to a runner.
package entry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/tools/localinvocation/invocation"
)

func Terminal(argv []string, common invocation.Options) (invocation.Command, invocation.Record, error) {
	common.Argv = argv
	return invocation.Prepare(common)
}

func Shim(executable string, args []string, common invocation.Options) (invocation.Command, invocation.Record, error) {
	common.Argv = append([]string{executable}, args...)
	return invocation.Prepare(common)
}

type hookCommand struct {
	Argv    []string `json:"argv"`
	Command string   `json:"command"`
	CWD     string   `json:"cwd"`
}

type hookEnvelope struct {
	hookCommand
	ToolInput      hookCommand `json:"tool_input"`
	HookEventName  string      `json:"hook_event_name"`
	SessionID      string      `json:"session_id"`
	ToolUseID      string      `json:"tool_use_id"`
	CallID         string      `json:"call_id"`
	EnvironmentRef string      `json:"environment_ref"`
}

// Hook accepts structured argv when the provider has it. A provider's single
// command string is recorded as a shell script, never guessed into child argv.
// Unknown hook fields (including environment maps) are ignored and not saved.
func Hook(data []byte, common invocation.Options) (invocation.Command, invocation.Record, error) {
	var h hookEnvelope
	if err := json.Unmarshal(data, &h); err != nil {
		return invocation.Command{}, invocation.Record{}, fmt.Errorf("decode exec hook: %w", err)
	}
	if h.HookEventName != "" && h.HookEventName != "PreToolUse" {
		return invocation.Command{}, invocation.Record{}, errors.New("exec hook must run at PreToolUse")
	}
	spec := h.hookCommand
	if len(h.ToolInput.Argv) != 0 || h.ToolInput.Command != "" {
		spec = h.ToolInput
	}
	if len(spec.Argv) != 0 && spec.Command != "" {
		return invocation.Command{}, invocation.Record{}, errors.New("exec hook has both argv and command")
	}
	if len(spec.Argv) == 0 {
		if strings.TrimSpace(spec.Command) == "" {
			return invocation.Command{}, invocation.Record{}, errors.New("exec hook has no command")
		}
		spec.Argv = []string{"sh", "-c", spec.Command}
		common.SourceForm = invocation.FormShellCommand
	}
	common.Argv = spec.Argv
	if common.CWD == "" {
		common.CWD = spec.CWD
		if common.CWD == "" {
			common.CWD = h.CWD
		}
	}
	if common.SessionID == "" {
		common.SessionID = h.SessionID
	}
	if common.ProviderCallID == "" {
		common.ProviderCallID = h.ToolUseID
	}
	if common.CallID == "" {
		common.CallID = h.CallID
	}
	if common.EnvironmentRef == "" {
		common.EnvironmentRef = h.EnvironmentRef
	}
	return invocation.Prepare(common)
}

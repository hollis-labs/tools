package fixturehost

import (
	"context"
	"fmt"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

type provider struct{ h *Host }

func (p provider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{SupportsToolCalling: true, ContextWindowSize: 32000}
}
func (p provider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "", sdk.ErrUnsupported
}
func (p provider) StreamChat(ctx context.Context, request llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.h.models.Add(1)
	if p.h.mode == "cancel" {
		return nil, context.Canceled
	}
	var first llmtypes.StreamEvent
	if len(request.Messages) == 1 {
		first = llmtypes.StreamEvent{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: p.h.callID, Name: "read_value", Input: map[string]any{}}}
	} else {
		last := request.Messages[len(request.Messages)-1]
		if len(last.ContentBlocks) != 1 || last.ContentBlocks[0].ToolUseID != p.h.callID || last.ContentBlocks[0].Content != "42" {
			return nil, fmt.Errorf("fixture model: missing paired synthetic tool result")
		}
		first = llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "The fixture value is 42."}
	}
	stream := make(chan llmtypes.StreamEvent, 3)
	stream <- first
	stream <- llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 10, OutputTokens: 5}}
	stream <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(stream)
	return stream, nil
}

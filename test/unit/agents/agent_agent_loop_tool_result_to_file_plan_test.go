package agent

import (
	"encoding/json"
	"errors"
	"fmt"
)


import (
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

// The guard on MaxProviderRequestBytes and the credit reservation both read
// requestSizeBytes, so Space context has to be counted or large contexts bypass
// the friendly error and are under-billed.
func TestRequestSizeBytesCountsPromptAndSpaceContext(t *testing.T) {
	base := TestingRequestSizeBytes(ModelRequest{})
	withPrompt := TestingRequestSizeBytes(ModelRequest{SystemPrompt: strings.Repeat("p", 100)})
	withCard := TestingRequestSizeBytes(ModelRequest{SpaceCard: strings.Repeat("c", 100)})
	withRecords := TestingRequestSizeBytes(ModelRequest{SpaceRecords: strings.Repeat("r", 100)})

	for name, got := range map[string]int{
		"system prompt": withPrompt,
		"space card":    withCard,
		"space records": withRecords,
	} {
		if got != base+100 {
			t.Fatalf("requestSizeBytes() ignored the %s: got %d, want %d", name, got, base+100)
		}
	}
}

type loopingToolProvider struct{ calls int }

func (provider *loopingToolProvider) Next(ModelRequest) (ModelResponse, error) {
	provider.calls++
	return ModelResponse{ToolRequests: []ToolRequest{{
		ID: fmt.Sprintf("tool-%d", provider.calls), Name: ToolListDirectory, Risk: RiskRead,
	}}}, nil
}

type serverToolProvider struct {
	calls         int
	systemPrompts []string
}

func (provider *serverToolProvider) Next(request ModelRequest) (ModelResponse, error) {
	provider.calls++
	provider.systemPrompts = append(provider.systemPrompts, request.SystemPrompt)
	if provider.calls == 1 {
		return ModelResponse{ToolRequests: []ToolRequest{{ID: "read-1", Name: "workflow.read_content", Risk: RiskRead, Arguments: json.RawMessage(`{"resourceId":"doc-1"}`)}}}, nil
	}
	if len(request.ToolResults) != 1 || !request.ToolResults[0].OK {
		return ModelResponse{}, errors.New("missing tool result")
	}
	return ModelResponse{Text: "Grounded answer"}, nil
}

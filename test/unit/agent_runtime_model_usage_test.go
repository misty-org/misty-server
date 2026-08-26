package unit

import (
	"encoding/json"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestAgentRuntimeModelUsageReadsCompletionAggregate(t *testing.T) {
	usage := api.TestingAgentRuntimeModelUsage(json.RawMessage(`{"inputTokens":1799,"outputTokens":122,"inputTokenDetails":{"cacheReadTokens":400},"outputTokenDetails":{"reasoningTokens":12}}`))
	if usage.Estimated || usage.InputTokens != 1799 || usage.CachedInputTokens != 400 || usage.OutputTokens != 122 || usage.ReasoningTokens != 12 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestAgentRuntimeModelUsageDefersRedactedStepUsage(t *testing.T) {
	usage := api.TestingAgentRuntimeModelUsage(json.RawMessage(`{"usage":{"inputTokens":"[redacted]","outputTokens":"[redacted]"}}`))
	if !usage.Estimated || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("usage = %#v", usage)
	}
}

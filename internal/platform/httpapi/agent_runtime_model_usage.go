package api

import (
	"encoding/json"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func agentRuntimeModelUsage(output json.RawMessage) serveragent.ModelUsage {
	type runtimeUsage struct {
		InputTokens       int64 `json:"inputTokens"`
		OutputTokens      int64 `json:"outputTokens"`
		InputTokenDetails struct {
			CacheReadTokens int64 `json:"cacheReadTokens"`
		} `json:"inputTokenDetails"`
		OutputTokenDetails struct {
			ReasoningTokens int64 `json:"reasoningTokens"`
		} `json:"outputTokenDetails"`
	}
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(output, &envelope) != nil {
		return serveragent.ModelUsage{Estimated: true}
	}
	raw := output
	if len(envelope.Usage) > 0 && string(envelope.Usage) != "null" {
		raw = envelope.Usage
	}
	var value runtimeUsage
	if json.Unmarshal(raw, &value) != nil {
		// Vercel Workflow redacts intermediate per-step token counts. Treat that
		// as unavailable usage; the unredacted aggregate arrives on completion.
		return serveragent.ModelUsage{Estimated: true}
	}
	return serveragent.ModelUsage{InputTokens: value.InputTokens, CachedInputTokens: value.InputTokenDetails.CacheReadTokens,
		OutputTokens: value.OutputTokens, ReasoningTokens: value.OutputTokenDetails.ReasoningTokens,
		Estimated: value.InputTokens == 0 && value.OutputTokens == 0}
}

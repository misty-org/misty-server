package api

import (
	"encoding/json"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func agentRuntimeModelUsage(output json.RawMessage) serveragent.ModelUsage {
	type runtimeUsage struct {
		InputTokens       *int64 `json:"inputTokens"`
		OutputTokens      *int64 `json:"outputTokens"`
		InputTokenDetails struct {
			CacheReadTokens *int64 `json:"cacheReadTokens"`
		} `json:"inputTokenDetails"`
		OutputTokenDetails struct {
			ReasoningTokens *int64 `json:"reasoningTokens"`
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
		// Public lifecycle records redact keys containing "token". Only raw signed
		// callback counters are authoritative; redacted or malformed usage is unavailable.
		return serveragent.ModelUsage{Estimated: true}
	}
	if value.InputTokens == nil || value.OutputTokens == nil || *value.InputTokens < 0 || *value.OutputTokens < 0 || *value.InputTokens > 1_000_000_000 || *value.OutputTokens > 1_000_000_000 {
		return serveragent.ModelUsage{Estimated: true}
	}
	result := serveragent.ModelUsage{InputTokens: *value.InputTokens, OutputTokens: *value.OutputTokens,
		CachedInputTokensMissing: value.InputTokenDetails.CacheReadTokens == nil, ReasoningTokensMissing: value.OutputTokenDetails.ReasoningTokens == nil,
		Estimated: *value.InputTokens == 0 && *value.OutputTokens == 0}
	if value.InputTokenDetails.CacheReadTokens != nil {
		result.CachedInputTokens = *value.InputTokenDetails.CacheReadTokens
	}
	if value.OutputTokenDetails.ReasoningTokens != nil {
		result.ReasoningTokens = *value.OutputTokenDetails.ReasoningTokens
	}
	if result.CachedInputTokens < 0 || result.CachedInputTokens > result.InputTokens || result.ReasoningTokens < 0 || result.ReasoningTokens > result.OutputTokens {
		return serveragent.ModelUsage{Estimated: true}
	}
	return result
}

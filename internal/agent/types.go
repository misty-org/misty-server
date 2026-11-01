package agent

import "encoding/json"

const (
	ModeFull = "full"

	RoleUser = "user"

	RiskRead      = "read"
	RiskWrite     = "write"
	RiskDangerous = "dangerous"
)

const MaxToolResultBytes = 6 << 20

type ToolManifest struct {
	Tools []ToolDefinition `json:"tools"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Risk        string          `json:"risk"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ToolRequest struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Risk             string          `json:"risk"`
	ApprovalRequired bool            `json:"approval_required"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
	Estimated         bool  `json:"estimated"`
	// Runtime aggregates can omit modality details while retaining exact totals.
	CachedInputTokensMissing bool `json:"-"`
	ReasoningTokensMissing   bool `json:"-"`
}

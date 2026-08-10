package agent

import (
	"context"
	"encoding/json"
	"time"
)

const (
	ModeAsk  = "ask"
	ModeAuto = "auto"
	ModeFull = "full"

	EventAgentMessage = "agent_message"
	EventToolRequest  = "tool_request"
	EventFilePlan     = "file_plan"
	EventError        = "error"

	// Pre-rename values. Sessions persisted before the agent rename replay their
	// events and messages with these, so readers accept both until
	// 20260917000000_rename_agent_message_event_type.sql has run everywhere and
	// every retained session has aged out. Only the current values are written.
	EventAgentMessageLegacy = "assistant_message"
	RoleAgentLegacy         = "assistant"

	RoleUser  = "user"
	RoleAgent = "agent"

	// Mirrors db.HostedAIMeterAgent, which the agent package cannot import. The
	// "assistant_ai" value is intentionally unchanged by the agent rename because
	// it is persisted in hosted-AI usage and credit ledger rows.
	hostedAIMeterAgent = "assistant_ai"

	ToolListDirectory    = "list_directory"
	ToolSearchFiles      = "search_files"
	ToolPreviewFile      = "preview_file"
	ToolValidateFilePlan = "validate_file_plan"
	ToolApplyFilePlan    = "apply_file_plan"

	RiskRead      = "read"
	RiskWrite     = "write"
	RiskDangerous = "dangerous"
)

const MaxModelOutputTokens int64 = 2200

const (
	MaxUserMessageBytes      = 32 << 10
	MaxToolResultsPerRequest = 8
	MaxToolResultBytes       = 6 << 20
	MaxProviderRequestBytes  = 8 << 20
	MaxProviderCallsPerTurn  = 3
	MaxDocumentCallsPerTurn  = 26
)

type AgentMessageRequest struct {
	Mode          string       `json:"mode"`
	UserMessage   string       `json:"user_message"`
	PlanOnly      bool         `json:"plan_only,omitempty"`
	ActiveRoot    string       `json:"active_root,omitempty"`
	SelectedPaths []string     `json:"selected_paths,omitempty"`
	ToolScope     string       `json:"tool_scope,omitempty"`
	Capabilities  ToolManifest `json:"capabilities"`

	// SpaceID is accepted only so the decoder, which rejects unknown fields,
	// does not 400 on a client that sends it. It is NOT authoritative: the
	// session's Space comes from the session row via ValidateAgentSessionAccess,
	// because trusting this field would let a member read another Space's
	// context through a session bound elsewhere.
	SpaceID string `json:"space_id,omitempty"`

	// SpaceSection is which Space surface the member is working in. It is
	// per-turn state like ActiveRoot, not part of session identity, so walking
	// between surfaces does not start a new conversation.
	SpaceSection string `json:"space_section,omitempty"`

	// ContextTaskID is a client hint identifying the Task currently open in the
	// dock. The API resolves it inside the session-bound Space and rechecks the
	// member's Task permission before adding any Task detail to the prompt.
	ContextTaskID string `json:"context_task_id,omitempty"`

	// SpaceCard, SpaceRecords and SpaceContextRevision are assembled server-side
	// per turn and are never accepted from the client; json:"-" keeps the decoder
	// from populating them. Empty SpaceRecords means "reuse what the session
	// already has", which is the common case when nothing in the Space changed.
	SpaceCard            string `json:"-"`
	SpaceRecords         string `json:"-"`
	SpaceContextRevision string `json:"-"`
}

// SpaceSections are the surfaces a member can be working in when they talk to an
// agent. Mirrors validSpaceSections in the client's features/spaces/index.tsx.
var SpaceSections = map[string]bool{
	"agents":   true,
	"chat":     true,
	"tasks":    true,
	"notes":    true,
	"library":  true,
	"members":  true,
	"settings": true,
}

type ToolManifest struct {
	Tools []ToolDefinition `json:"tools"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Risk        string          `json:"risk"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type AgentEvent struct {
	Sequence          int64              `json:"sequence"`
	Type              string             `json:"type"`
	RunID             string             `json:"run_id,omitempty"`
	Text              string             `json:"text,omitempty"`
	ToolRequests      []ToolRequest      `json:"tool_requests,omitempty"`
	FilePlan          *FileOperationPlan `json:"file_plan,omitempty"`
	Message           string             `json:"message,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	HostedAIUsedRatio float64            `json:"hosted_ai_used_ratio,omitempty"`
	HostedAIResetAt   *time.Time         `json:"hosted_ai_reset_at,omitempty"`
	CreditsUsed       int64              `json:"-"`
	CreditsRemaining  int64              `json:"-"`
	Citations         []AgentCitation    `json:"citations,omitempty"`
}

type ToolRequest struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Risk             string          `json:"risk"`
	ApprovalRequired bool            `json:"approval_required"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
}

type ToolResult struct {
	RequestID string          `json:"request_id"`
	Name      string          `json:"name"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type FileOperationPlan struct {
	Summary           string          `json:"summary"`
	CompletionSummary string          `json:"completion_summary"`
	Operations        []FileOperation `json:"operations"`
	Warnings          []string        `json:"warnings"`
}

type FileOperation struct {
	Type       string   `json:"type"`
	Path       string   `json:"path,omitempty"`
	From       string   `json:"from,omitempty"`
	To         string   `json:"to,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ModelRequest struct {
	SessionID    string
	UserID       string
	SystemPrompt string
	AgentTier    AgentTier
	Mode         string
	ActiveRoot   string
	Messages     []Message
	ToolResults  []ToolResult
	Capabilities ToolManifest
	KnownPaths   []string

	// SpaceCard describes what the Space is, what its surfaces do, and what the
	// current member may do there. It is stable for a conversation, so it sits in
	// the cacheable part of the prompt alongside SystemPrompt.
	SpaceCard string
	// SpaceRecords is the permission-filtered Space content for this turn. It is
	// rebuilt as the Space changes, so it sits after the transcript rather than
	// in front of it, or every turn would invalidate the cached prefix.
	SpaceRecords string
	// SpaceSection is the surface the member is working in this turn.
	SpaceSection string
}

type ModelResponse struct {
	Text         string
	ToolRequests []ToolRequest
	FilePlan     *FileOperationPlan
	Citations    []AgentCitation
	Usage        ModelUsage
}

type AgentCitation struct {
	ID           string `json:"id"`
	ArtifactID   string `json:"artifactId,omitempty"`
	ScopeID      string `json:"scopeId"`
	FileName     string `json:"fileName"`
	RelativePath string `json:"relativePath,omitempty"`
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	Page         int    `json:"page,omitempty"`
	Slide        int    `json:"slide,omitempty"`
	Sheet        string `json:"sheet,omitempty"`
	Range        string `json:"range,omitempty"`
	Section      string `json:"section,omitempty"`
	Excerpt      string `json:"excerpt,omitempty"`
}

type ModelUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
	Estimated         bool  `json:"estimated"`
}

type UsageReservation struct {
	ID               string
	ReservedMicrousd int64
	ReservedCredits  int64
}

type UsageSettlement struct {
	ChargedMicrousd  int64
	UsedRatio        float64
	ResetAt          time.Time
	CreditsUsed      int64
	CreditsRemaining int64
}

type UsageMeter interface {
	Reserve(userID, idempotencyKey, meter, provider, model string, estimatedInputTokens, maxOutputTokens int64) (*UsageReservation, error)
	Settle(reservation *UsageReservation, idempotencyKey, meter, provider, model string, usage ModelUsage) (UsageSettlement, error)
	Release(reservation *UsageReservation) error
	Refund(reservation *UsageReservation, idempotencyKey, reason string) (UsageSettlement, error)
}

type HostedAILimitReachedError struct {
	Required  int64
	Available int64
	ResetAt   time.Time
}

func (e HostedAILimitReachedError) Error() string { return "weekly hosted AI limit reached" }

type CreditsExhaustedError = HostedAILimitReachedError

type ModelProvider interface {
	Next(request ModelRequest) (ModelResponse, error)
}

// ContextModelProvider is implemented by production providers so an in-flight
// request can be canceled. ModelProvider remains for local/test compatibility.
type ContextModelProvider interface {
	NextContext(ctx context.Context, request ModelRequest) (ModelResponse, error)
}

type ProviderInfo interface {
	ProviderName() string
	ModelName() string
}

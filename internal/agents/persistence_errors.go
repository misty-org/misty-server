package agent

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// ErrPersistedSessionNotFound allows persistence implementations to hide
// whether a conversation exists or belongs to another user.
var ErrPersistedSessionNotFound = errors.New("persisted ai session not found")

const conversationRetention = 30 * 24 * time.Hour

// PersistedConversationEvent is an append-only, sanitized audit record. The
// current session state is stored separately so a server can resume tool loops
// after a restart without replaying side effects.
type PersistedConversationEvent struct {
	Type string
	Data json.RawMessage
}

// SessionPersistence is deliberately small so the agent runtime remains
// usable without a database in tests and local development.
type SessionPersistence interface {
	CreateAgentSession(ctx context.Context, conversationID, userID string, state json.RawMessage, activeUntil, retentionExpiresAt time.Time) error
	LoadAgentSession(ctx context.Context, conversationID, userID string) (json.RawMessage, error)
	SaveAgentSession(ctx context.Context, conversationID, userID string, state json.RawMessage, events []PersistedConversationEvent, activeUntil, retentionExpiresAt time.Time) error
}

type persistedSessionState struct {
	ID                    string            `json:"id"`
	UserID                string            `json:"userId"`
	BillingUserID         string            `json:"billingUserId"`
	BillingScope          string            `json:"billingScope"`
	ModelID               string            `json:"modelId,omitempty"`
	ReasoningEffort       string            `json:"reasoningEffort,omitempty"`
	SystemPrompt          string            `json:"systemPrompt,omitempty"`
	AllowTools            *bool             `json:"allowTools,omitempty"`
	AllowWriteTools       *bool             `json:"allowWriteTools,omitempty"`
	AgentTier             AgentTier         `json:"agentTier"`
	Mode                  string            `json:"mode"`
	Capabilities          ToolManifest      `json:"capabilities"`
	Messages              []Message         `json:"messages"`
	ToolResults           []ToolResult      `json:"toolResults"`
	Events                []AgentEvent      `json:"events"`
	NextSequence          int64             `json:"nextSequence"`
	CreatedAt             time.Time         `json:"createdAt"`
	UpdatedAt             time.Time         `json:"updatedAt"`
	Canceled              bool              `json:"canceled"`
	ProviderCallsThisTurn int               `json:"providerCallsThisTurn"`
	PendingToolRequests   map[string]string `json:"pendingToolRequests"`
	SpaceID               string            `json:"spaceId,omitempty"`
	SpaceCard             string            `json:"spaceCard,omitempty"`
	SpaceRecords          string            `json:"spaceRecords,omitempty"`
	SpaceContextRevision  string            `json:"spaceContextRevision,omitempty"`
	SpaceSection          string            `json:"spaceSection,omitempty"`
}

var (
	unixLocalPathPattern    = regexp.MustCompile(`(?:file://)?(?:/[^\s"'<>]+){2,}`)
	windowsLocalPathPattern = regexp.MustCompile(`(?i)[a-z]:\\(?:[^\s"'<>]+\\)+[^\s"'<>]*`)
)

func TestingMarshalPersistentSession(session *Session) (json.RawMessage, error) {
	allowWrite := session.AllowWriteTools
	allowTools := session.AllowTools
	state := persistedSessionState{
		ID:                    session.ID,
		UserID:                session.UserID,
		BillingUserID:         session.BillingUserID,
		BillingScope:          session.BillingScope,
		ModelID:               session.ModelID,
		ReasoningEffort:       session.ReasoningEffort,
		SystemPrompt:          session.SystemPrompt,
		AllowTools:            &allowTools,
		AllowWriteTools:       &allowWrite,
		AgentTier:             session.AgentTier,
		Mode:                  session.Mode,
		Capabilities:          session.Capabilities,
		Messages:              sanitizeMessages(session.Messages),
		ToolResults:           sanitizeToolResults(session.ToolResults),
		Events:                sanitizeAgentEvents(session.Events),
		NextSequence:          session.nextSequence,
		CreatedAt:             session.CreatedAt,
		UpdatedAt:             session.UpdatedAt,
		Canceled:              session.Canceled,
		ProviderCallsThisTurn: session.ProviderCallsThisTurn,
		PendingToolRequests:   cloneStringMap(session.PendingToolRequests),
		SpaceID:               session.SpaceID,
		SpaceCard:             session.SpaceCard,
		SpaceRecords:          session.SpaceRecords,
		SpaceContextRevision:  session.SpaceContextRevision,
		SpaceSection:          session.SpaceSection,
	}
	return json.Marshal(state)
}

func TestingUnmarshalPersistentSession(raw json.RawMessage, expectedID, expectedUserID string) (*Session, error) {
	var state persistedSessionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.ID != expectedID || state.UserID != expectedUserID {
		return nil, ErrPersistedSessionNotFound
	}
	if state.Mode == "" {
		state.Mode = ModeAsk
	}
	state.AgentTier = NormalizeAgentTier(state.AgentTier)
	if state.BillingUserID == "" {
		state.BillingUserID = state.UserID
	}
	allowWrite := true
	allowTools := true
	if state.AllowTools != nil {
		allowTools = *state.AllowTools
	}
	if state.AllowWriteTools != nil {
		allowWrite = *state.AllowWriteTools
	}
	return &Session{
		ID:                    state.ID,
		UserID:                state.UserID,
		BillingUserID:         state.BillingUserID,
		BillingScope:          state.BillingScope,
		ModelID:               state.ModelID,
		ReasoningEffort:       state.ReasoningEffort,
		SystemPrompt:          state.SystemPrompt,
		AllowTools:            allowTools,
		AllowWriteTools:       allowWrite,
		AgentTier:             state.AgentTier,
		Mode:                  state.Mode,
		Capabilities:          state.Capabilities,
		Messages:              state.Messages,
		ToolResults:           state.ToolResults,
		KnownPaths:            make(map[string]struct{}),
		Events:                state.Events,
		nextSequence:          state.NextSequence,
		CreatedAt:             state.CreatedAt,
		UpdatedAt:             state.UpdatedAt,
		Canceled:              state.Canceled,
		ProviderCallsThisTurn: state.ProviderCallsThisTurn,
		PendingToolRequests:   cloneStringMap(state.PendingToolRequests),
		SpaceID:               state.SpaceID,
		SpaceCard:             state.SpaceCard,
		SpaceRecords:          state.SpaceRecords,
		SpaceContextRevision:  state.SpaceContextRevision,
		SpaceSection:          state.SpaceSection,
	}, nil
}

func persistentEvents(beforeMessages, beforeResults, beforeEvents int, session *Session) []PersistedConversationEvent {
	events := make([]PersistedConversationEvent, 0)
	for _, message := range session.Messages[boundedIndex(beforeMessages, len(session.Messages)):] {
		eventType := "user_message"
		if message.Role == RoleAgent || message.Role == RoleAgentLegacy {
			eventType = EventAgentMessage
		}
		data, _ := json.Marshal(map[string]any{"message": sanitizeMessage(message)})
		events = append(events, PersistedConversationEvent{Type: eventType, Data: data})
	}
	for _, result := range session.ToolResults[boundedIndex(beforeResults, len(session.ToolResults)):] {
		data, _ := json.Marshal(map[string]any{"toolResult": sanitizeToolResult(result)})
		events = append(events, PersistedConversationEvent{Type: "tool_result", Data: data})
	}
	for _, event := range session.Events[boundedIndex(beforeEvents, len(session.Events)):] {
		if event.Type == EventAgentMessage || event.Type == EventAgentMessageLegacy {
			continue // already represented by the agent message above
		}
		eventType := "tool_call"
		if event.Type == EventError {
			eventType = "error"
		}
		data, _ := json.Marshal(map[string]any{"event": sanitizeAgentEvent(event)})
		events = append(events, PersistedConversationEvent{Type: eventType, Data: data})
	}
	return events
}

func boundedIndex(index, length int) int {
	if index < 0 || index > length {
		return length
	}
	return index
}

func sanitizeMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = sanitizeMessage(message)
	}
	return out
}

func sanitizeMessage(message Message) Message {
	message.Content = redactLocalPaths(message.Content)
	return message
}

func sanitizeToolResults(results []ToolResult) []ToolResult {
	out := make([]ToolResult, len(results))
	for i, result := range results {
		out[i] = sanitizeToolResult(result)
	}
	return out
}

func sanitizeToolResult(result ToolResult) ToolResult {
	if result.Name == ToolPreviewFile {
		result.Result = sanitizePreviewFileResult(result.Result)
	} else {
		result.Result = sanitizeRawJSON(result.Result)
	}
	result.Error = redactLocalPaths(result.Error)
	return result
}

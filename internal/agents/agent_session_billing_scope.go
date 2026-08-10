package agent

import (
	"context"
	"strings"
)

// SessionBillingScope exposes only the server-owned billing scope associated
// with a session. API adapters use it to bind short-lived document attachments
// to the exact durable job that created the conversation.
func (s *Service) SessionBillingScope(sessionID, userID string) (string, error) {
	var scope string
	err := s.store.WithSession(sessionID, userID, func(session *Session) error {
		scope = session.BillingScope
		return nil
	})
	return scope, err
}

func (s *Service) SendMessage(sessionID, userID string, request AgentMessageRequest) error {
	return s.SendMessageWithTier(sessionID, userID, request, TierLow)
}

func (s *Service) SendMessageWithTier(sessionID, userID string, request AgentMessageRequest, tier AgentTier) error {
	return s.SendMessageWithTierContext(context.Background(), sessionID, userID, request, tier)
}

func (s *Service) SendMessageWithTierContext(ctx context.Context, sessionID, userID string, request AgentMessageRequest, tier AgentTier) error {
	request.UserMessage = strings.TrimSpace(request.UserMessage)
	request.Mode = NormalizeMode(request.Mode)
	request.ActiveRoot = strings.TrimSpace(request.ActiveRoot)
	if request.UserMessage == "" {
		return ErrInvalidRequest("user_message is required")
	}
	if len(request.UserMessage) > MaxUserMessageBytes {
		return ErrInvalidRequest("user_message is too large")
	}
	if request.ActiveRoot != "" && !isSafeActiveRoot(request.ActiveRoot) {
		return ErrInvalidRequest("active_root must be an opaque scope ID or a relative display name")
	}
	request.SpaceSection = strings.TrimSpace(request.SpaceSection)
	if request.SpaceSection != "" && !SpaceSections[request.SpaceSection] {
		return ErrInvalidRequest("space_section must name a Space surface")
	}
	for _, selected := range request.SelectedPaths {
		if _, ok := normalizeRelativePath(selected); !ok {
			return ErrInvalidRequest("selected_paths must contain only safe relative paths")
		}
	}
	return s.store.WithSessionContext(ctx, sessionID, userID, func(ctx context.Context, session *Session) error {
		if session.Canceled {
			return ErrInvalidRequest("session is canceled")
		}
		session.Mode = request.Mode
		session.AgentTier = NormalizeAgentTier(tier)
		session.ActiveRoot = request.ActiveRoot
		session.Capabilities = request.Capabilities
		session.SpaceSection = request.SpaceSection
		// Only overwrite the Space blocks when the caller assembled fresh ones.
		// The handler leaves them empty when the revision token says nothing the
		// agent can see has changed, and the previous turn's records are reused so
		// the prompt bytes stay identical and remain cacheable.
		if request.SpaceCard != "" {
			session.SpaceCard = request.SpaceCard
		}
		if request.SpaceRecords != "" {
			session.SpaceRecords = request.SpaceRecords
			session.SpaceContextRevision = request.SpaceContextRevision
		}
		session.ProviderCallsThisTurn = 0
		session.ToolResults = nil
		clear(session.PendingToolRequests)
		for _, selected := range request.SelectedPaths {
			if normalized, ok := normalizeRelativePath(selected); ok {
				session.KnownPaths[normalized] = struct{}{}
			}
		}
		session.Messages = append(session.Messages, Message{Role: RoleUser, Content: request.UserMessage})
		return s.advanceLocked(ctx, session)
	})
}

func isSafeActiveRoot(value string) bool {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "scope_") && !strings.ContainsAny(trimmed, "/\\:") {
		return true
	}
	_, ok := normalizeRelativePath(trimmed)
	return ok
}

func (s *Service) SubmitToolResults(sessionID, userID string, results []ToolResult) error {
	return s.SubmitToolResultsWithTier(sessionID, userID, results, TierLow)
}

func (s *Service) SubmitToolResultsWithTier(sessionID, userID string, results []ToolResult, tier AgentTier) error {
	return s.SubmitToolResultsWithTierContext(context.Background(), sessionID, userID, results, tier)
}

func (s *Service) SubmitToolResultsWithTierContext(ctx context.Context, sessionID, userID string, results []ToolResult, tier AgentTier) error {
	if len(results) == 0 {
		return ErrInvalidRequest("tool results are required")
	}
	if len(results) > MaxToolResultsPerRequest {
		return ErrInvalidRequest("too many tool results")
	}
	totalBytes := 0
	for _, result := range results {
		totalBytes += len(result.Result) + len(result.Error)
	}
	if totalBytes > MaxToolResultBytes {
		return ErrInvalidRequest("tool results are too large")
	}
	return s.store.WithSessionContext(ctx, sessionID, userID, func(ctx context.Context, session *Session) error {
		if session.Canceled {
			return ErrInvalidRequest("session is canceled")
		}
		if session.ProviderCallsThisTurn >= providerCallLimit(session) {
			return ErrInvalidRequest("Agent tool step limit reached; send a new message to continue")
		}
		seen := make(map[string]struct{}, len(results))
		for _, result := range results {
			name, pending := session.PendingToolRequests[result.RequestID]
			if !pending || name != result.Name {
				return ErrInvalidRequest("tool result does not match an outstanding request")
			}
			if _, duplicate := seen[result.RequestID]; duplicate {
				return ErrInvalidRequest("duplicate tool result")
			}
			seen[result.RequestID] = struct{}{}
		}
		for requestID := range seen {
			delete(session.PendingToolRequests, requestID)
		}
		session.AgentTier = NormalizeAgentTier(tier)
		if containsPreviewFileResult(results) {
			for index := range session.ToolResults {
				if session.ToolResults[index].Name == ToolPreviewFile {
					session.ToolResults[index] = sanitizeToolResult(session.ToolResults[index])
				}
			}
		}
		session.ToolResults = append(session.ToolResults, results...)
		collectKnownPaths(session, results)
		return s.advanceLocked(ctx, session)
	})
}

func (s *Service) Events(sessionID, userID string, after int64) ([]AgentEvent, error) {
	return s.store.Events(sessionID, userID, after)
}

// SpaceContextState is what a caller needs to decide whether to rebuild Space
// context for the next turn.
type SpaceContextState struct {
	Revision string
	HasCard  bool
}

// SessionSpaceContext reports the change token the session's current Space
// records were built from. The API layer owns rebuilding context, because only
// it can reach the database; this lets it skip that work when nothing the agent
// can see has changed.
func (s *Service) SessionSpaceContext(ctx context.Context, sessionID, userID string) (SpaceContextState, error) {
	var state SpaceContextState
	err := s.store.WithSessionContext(ctx, sessionID, userID, func(_ context.Context, session *Session) error {
		state = SpaceContextState{Revision: session.SpaceContextRevision, HasCard: session.SpaceCard != ""}
		return nil
	})
	return state, err
}

// Transcript returns the conversation as plain messages, for a client rebuilding
// a session it does not hold locally. Replaying the event stream would be wrong
// for that: events carry tool requests, and a client that replayed them would
// run the tools a second time.
func (s *Service) Transcript(ctx context.Context, sessionID, userID string) ([]Message, error) {
	var messages []Message
	err := s.store.WithSessionContext(ctx, sessionID, userID, func(_ context.Context, session *Session) error {
		messages = append(messages, session.Messages...)
		return nil
	})
	return messages, err
}

// AppendExternalAgentMessage delivers the terminal result of delegated
// work back into the originating agent session without invoking the model.
// WithSessionContext persists both the message and its sequenced event.
func (s *Service) AppendExternalAgentMessage(ctx context.Context, sessionID, userID, runID, text string) (*AgentEvent, error) {
	var appended AgentEvent
	err := s.store.WithSessionContext(ctx, sessionID, userID, func(_ context.Context, session *Session) error {
		message := strings.TrimSpace(text)
		if message == "" {
			return ErrInvalidRequest("agent message is required")
		}
		session.Messages = append(session.Messages, Message{Role: RoleAgent, Content: message})
		session.appendEvent(AgentEvent{Type: EventAgentMessage, RunID: runID, Text: message})
		appended = session.Events[len(session.Events)-1]
		return nil
	})
	return &appended, err
}

// AppendExternalUserMessage persists a user turn that a server-coordinated
// Agent run will answer, without invoking the generic client-tool loop.
func (s *Service) AppendExternalUserMessage(ctx context.Context, sessionID, userID, text string) error {
	return s.store.WithSessionContext(ctx, sessionID, userID, func(_ context.Context, session *Session) error {
		message := strings.TrimSpace(text)
		if message == "" {
			return ErrInvalidRequest("user_message is required")
		}
		if len(message) > MaxUserMessageBytes {
			return ErrInvalidRequest("user_message is too large")
		}
		session.Messages = append(session.Messages, Message{Role: RoleUser, Content: message})
		return nil
	})
}

func (s *Service) Cancel(sessionID, userID string) error {
	return s.store.Cancel(sessionID, userID)
}

func (s *Service) Forget(sessionID, userID string) error {
	return s.store.Forget(sessionID, userID)
}

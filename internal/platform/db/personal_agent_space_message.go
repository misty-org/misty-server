package db

import (
	"context"
	"encoding/json"
)

// CreatePersonalAgentSpaceMessage is the first-class Space-Agent write
// boundary. It atomically requires the creator to retain both current Space
// message authority and ownership of an enabled Agent.
func (db *Database) CreatePersonalAgentSpaceMessage(ctx context.Context, actingUserID, spaceID, agentID, text string) (*SpaceMessage, error) {
	return db.createSpaceAgentMessageWithMembership(ctx, actingUserID, spaceID, "", agentID, []MessageSpan{{Type: "text", Text: text}}, true)
}

func (db *Database) CreatePersonalAgentSpaceMessageWithContent(ctx context.Context, actingUserID, spaceID, agentID string, content []MessageSpan) (*SpaceMessage, error) {
	return db.createSpaceAgentMessageWithMembership(ctx, actingUserID, spaceID, "", agentID, content, true)
}

func (db *Database) CreatePersonalAgentConversationMessageWithContent(ctx context.Context, actingUserID, spaceID, conversationID, agentID string, content []MessageSpan) (*SpaceMessage, error) {
	return db.createSpaceAgentMessageWithMembership(ctx, actingUserID, spaceID, conversationID, agentID, content, true)
}

// CreatePersonalAgentConversationRunMessage publishes the one canonical
// conversational response for a run and keeps its relationship to the user
// turn stable across reloads, retries, and the Activity drawer.
func (db *Database) CreatePersonalAgentConversationRunMessage(ctx context.Context, actingUserID, spaceID, conversationID, agentID, text, runID, sourceMessageID string) (*SpaceMessage, error) {
	origin, _ := json.Marshal(map[string]any{
		"kind":              "agent_run_response",
		"agent_run_id":      runID,
		"source_message_id": sourceMessageID,
	})
	return db.createSpaceAgentMessageWithProvenance(ctx, actingUserID, spaceID, conversationID, agentID, []MessageSpan{{Type: "text", Text: text}}, true, sourceMessageID, origin)
}

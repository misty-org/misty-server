package db

import (
	"context"
	"encoding/json"
)

// CreatePersonalAgentSpaceMessage is the first-class Space-Agent write
// boundary. It atomically requires the creator to retain both current Space
// message authority and ownership of an enabled Agent.
func (db *Database) CreatePersonalAgentSpaceMessage(ctx context.Context, billingUserID, spaceID, agentID, text string) (*SpaceMessage, error) {
	return db.createSpaceAgentMessageWithMembership(ctx, billingUserID, spaceID, "", agentID, []MessageSpan{{Type: "text", Text: text}}, true)
}

// CreatePersonalAgentConversationMessage publishes a creator-owned companion's
// result to the conversation that originated the run. Conversation membership
// and current creator authority are rechecked at the write boundary.
func (db *Database) CreatePersonalAgentConversationMessage(ctx context.Context, billingUserID, spaceID, conversationID, agentID, text string) (*SpaceMessage, error) {
	return db.createSpaceAgentMessageWithMembership(ctx, billingUserID, spaceID, conversationID, agentID, []MessageSpan{{Type: "text", Text: text}}, true)
}

// CreatePersonalAgentConversationRunMessage publishes the one canonical
// conversational response for a run and keeps its relationship to the user
// turn stable across reloads, retries, and the Activity drawer.
func (db *Database) CreatePersonalAgentConversationRunMessage(ctx context.Context, billingUserID, spaceID, conversationID, agentID, text, runID, sourceMessageID string) (*SpaceMessage, error) {
	origin, _ := json.Marshal(map[string]any{
		"kind":              "agent_run_response",
		"agent_run_id":      runID,
		"source_message_id": sourceMessageID,
	})
	return db.createSpaceAgentMessageWithProvenance(ctx, billingUserID, spaceID, conversationID, agentID, []MessageSpan{{Type: "text", Text: text}}, true, sourceMessageID, origin)
}

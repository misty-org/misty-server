package db

import "context"

// CreatePersonalAgentSpaceMessage is the first-class Space-Agent write
// boundary. It atomically requires both the triggering member and Agent
// membership to retain messages.read/write and agents.run.
func (db *Database) CreatePersonalAgentSpaceMessage(ctx context.Context, billingUserID, spaceID, agentID, text string) (*SpaceMessage, error) {
	return db.createSpaceAgentMessageWithMembership(ctx, billingUserID, spaceID, "", agentID, []MessageSpan{{Type: "text", Text: text}}, true)
}

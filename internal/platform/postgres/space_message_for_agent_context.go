package db

import "context"

// SpaceMessageForAgentContext returns one message through the same Space and
// conversation membership checks used by normal message reads. It is used only
// to recover the creator-selected attachments bound to a durable Agent run.
func (db *Database) SpaceMessageForAgentContext(ctx context.Context, userID, spaceID, conversationID, messageID string) (*SpaceMessage, error) {
	return db.spaceMessageByID(ctx, userID, spaceID, conversationID, messageID)
}

package db

import (
	"context"
	"encoding/json"
)

func (db *Database) PersonalAgentSpaceContext(ctx context.Context, userID, spaceID string, sections json.RawMessage) (string, error) {
	return db.PersonalAgentSpaceContextForConversation(ctx, userID, spaceID, "", sections)
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PersonalAgentAvatarForUser returns the immutable avatar descriptor for one
// Agent version after checking either ownership or active shared-Space access.
func (db *Database) PersonalAgentAvatarForUser(ctx context.Context, userID, agentID string, version int64) (json.RawMessage, error) {
	var avatar json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT v.avatar
			FROM personal_agent_versions v
			JOIN personal_agents a ON a.id=v.agent_id
			WHERE v.agent_id=$1 AND v.version=$2 AND (
				a.owner_user_id=$3 OR EXISTS(
					SELECT 1 FROM space_members m WHERE m.user_id=$3 AND (
						EXISTS(SELECT 1 FROM space_tasks t WHERE t.space_id=m.space_id AND t.assignee_agent_id=v.agent_id) OR
						EXISTS(SELECT 1 FROM space_messages sm WHERE sm.space_id=m.space_id AND sm.sender_agent_id=v.agent_id)
					)
				)
			)`, agentID, version, userID).Scan(&avatar)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	return avatar, err
}

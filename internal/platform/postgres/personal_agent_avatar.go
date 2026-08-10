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
					SELECT 1 FROM personal_agent_space_grants g
					JOIN space_members m ON m.space_id=g.space_id
					WHERE g.agent_id=v.agent_id AND g.approved_version_id=v.id
						AND g.enabled AND g.removed_at IS NULL AND m.user_id=$3
				)
			)`, agentID, version, userID).Scan(&avatar)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	return avatar, err
}

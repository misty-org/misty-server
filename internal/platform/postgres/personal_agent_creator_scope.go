package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// AccessiblePersonalAgents returns only the requester's enabled companions.
// Space membership makes them automatically available; no placement row or
// per-member grant participates in this lookup.
func (db *Database) AccessiblePersonalAgents(ctx context.Context, ownerUserID, spaceID string) ([]PersonalAgent, error) {
	items := []PersonalAgent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, ownerUserID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents
			WHERE owner_user_id=$1 AND enabled AND deleted_at IS NULL ORDER BY lower(name),id`, ownerUserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PersonalAgent
			if err := scanPersonalAgent(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) PersonalAgentForSpace(ctx context.Context, ownerUserID, spaceID, agentID string) (*PersonalAgent, error) {
	out := &PersonalAgent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, ownerUserID, spaceID, agentID); err != nil {
			return err
		}
		return scanPersonalAgent(tx.QueryRowContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents
			WHERE id=$1 AND owner_user_id=$2 AND enabled AND deleted_at IS NULL`, agentID, ownerUserID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrPersonalAgentNotFound
	}
	return out, err
}

func (db *Database) PersonalAgentSpaceContext(ctx context.Context, userID, spaceID string, sections json.RawMessage) (string, error) {
	return db.PersonalAgentSpaceContextForConversation(ctx, userID, spaceID, "", sections)
}

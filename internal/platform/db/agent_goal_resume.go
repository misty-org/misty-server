package db

import (
	"context"
	"database/sql"
	"errors"
)

// AgentGoalByID reads one of the account's goals.
func (db *Database) AgentGoalByID(ctx context.Context, user, id string) (*AgentGoal, error) {
	out := &AgentGoal{}
	err := db.WithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return scanAgentGoal(tx.QueryRowContext(ctx, `SELECT `+agentGoalColumns+` FROM agent_goals WHERE id=$1 AND owner_user_id=$2`, id, user), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

package db

import (
	"context"
	"database/sql"
)

func (db *Database) AgentRunHasUnconfirmedEffects(ctx context.Context, userID, runID string) (bool, error) {
	var pending bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_toolbox_action_journal
   WHERE run_id=$1 AND user_id=$2 AND risk<>'read' AND state IN ('started','unknown','failed'))`, runID, userID).Scan(&pending)
	})
	return pending, err
}

package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) AgentRunHasUnconfirmedEffects(ctx context.Context, userID, runID string) (bool, error) {
	var pending bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_toolbox_action_journal
   WHERE run_id=$1 AND user_id=$2 AND risk<>'read' AND state IN ('started','unknown','failed'))`, runID, userID).Scan(&pending)
	})
	return pending, err
}

// AgentRunPendingEffect is a preflight for interactive proposals. Execution must
// still claim with RequireSettledRun, which serializes this check with dispatch.
func (db *Database) AgentRunPendingEffect(ctx context.Context, userID, runID, exceptKey string) (*PendingRunEffect, error) {
	var result *PendingRunEffect
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var key string
		err := tx.QueryRowContext(ctx, `SELECT j.idempotency_key FROM agent_toolbox_action_journal j LEFT JOIN space_runs r ON r.id=j.run_id LEFT JOIN ai_invocations i ON i.id=j.run_id WHERE j.run_id=$1 AND j.user_id=$2 AND COALESCE(r.owner_user_id,i.user_id)=$2 AND j.idempotency_key<>$3 AND j.risk<>'read' AND (j.state IN ('started','unknown') OR (j.state='failed' AND COALESCE(j.error_code,'')<>'tool_not_attempted')) ORDER BY j.created_at,j.idempotency_key LIMIT 1`, runID, userID, exceptKey).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &PendingRunEffect{IdempotencyKey: key}
		return nil
	})
	return result, err
}

package db

import (
	"context"
	"database/sql"
)

func (db *Database) AgentRunHasUnconfirmedEffects(ctx context.Context, userID, runID string) (bool, error) {
	var pending bool
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		// An action the toolbox never attempted (tool_not_attempted) had no effect to confirm: two
		// sub-agents that never started turned a fully reported run into a failure (Odysseys, Oct 20).
		// Nor has a browser_run program that never allowed a consequential step: the page layer
		// refuses pay, send, delete and post without allowConsequential, so a program whose result
		// was lost (a read that outlived its lease) changed nothing that needs confirming (Oct 28).
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_toolbox_action_journal
   WHERE run_id=$1 AND user_id=$2 AND risk<>'read' AND state IN ('started','unknown','failed')
     AND NOT (state='failed' AND error_code='tool_not_attempted')
     AND NOT (tool_name='browser.run' AND COALESCE(request->>'code','') NOT ILIKE '%allowConsequential%'))`, runID, userID).Scan(&pending)
	})
	return pending, err
}

package db

import (
	"context"
	"database/sql"
)

type PersonalAgentWorkAvailability struct {
	Busy        bool   `json:"busy"`
	ActiveState string `json:"active_state,omitempty"`
	QueueCount  int    `json:"queue_count"`
}

// PersonalAgentWorkAvailability exposes no cross-Space task or content data;
// it only tells the creator whether their companion can start immediately.
func (db *Database) PersonalAgentWorkAvailability(ctx context.Context, ownerUserID, agentID string) (*PersonalAgentWorkAvailability, error) {
	out := &PersonalAgentWorkAvailability{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_agents WHERE id=$1 AND owner_user_id=$2 AND enabled AND deleted_at IS NULL)`, agentID, ownerUserID).Scan(&out.Busy); err != nil {
			return err
		}
		if !out.Busy {
			return ErrPersonalAgentNotFound
		}
		out.Busy = false
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT state FROM space_runs WHERE agent_id=$1 AND owner_user_id=$2 AND state IN ('running','awaiting_approval','awaiting_device') ORDER BY created_at LIMIT 1),'')`, agentID, ownerUserID).Scan(&out.ActiveState); err != nil {
			return err
		}
		out.Busy = out.ActiveState != ""
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_runs WHERE agent_id=$1 AND owner_user_id=$2 AND state='queued'`, agentID, ownerUserID).Scan(&out.QueueCount)
	})
	return out, err
}

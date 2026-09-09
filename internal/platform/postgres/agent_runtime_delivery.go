package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"time"
)

type AgentRuntimeDelivery struct {
	ID        string
	UserID    string
	RunID     string
	Operation string
	Payload   json.RawMessage
	LeaseID   string
	Attempts  int
}

func (db *Database) ClaimAgentRuntimeDeliveries(ctx context.Context, limit int) ([]AgentRuntimeDelivery, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AgentRuntimeDelivery{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE agent_runtime_deliveries d SET state='leased',lease_id=$1,lease_expires_at=NOW()+INTERVAL '90 seconds',attempts=d.attempts+1,updated_at=NOW()
   WHERE d.id IN (SELECT id FROM agent_runtime_deliveries WHERE available_at<=NOW() AND (state='pending' OR state='leased' AND lease_expires_at<NOW()) ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT $2)
   RETURNING id,user_id,run_id,operation,payload,lease_id,attempts`, uuid.NewString(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentRuntimeDelivery
			if err := rows.Scan(&item.ID, &item.UserID, &item.RunID, &item.Operation, &item.Payload, &item.LeaseID, &item.Attempts); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) FinishAgentRuntimeDelivery(ctx context.Context, item AgentRuntimeDelivery, deliveryErr error, terminal bool) error {
	state, message := "completed", ""
	if deliveryErr != nil {
		state = "pending"
		message = deliveryErr.Error()
		if len(message) > 1000 {
			message = message[:1000]
		}
		if terminal {
			state = "failed"
		}
	}
	delay := time.Duration(min(item.Attempts, 12)*5) * time.Second
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_runtime_deliveries SET state=$1,last_error=$2,available_at=$3,lease_id=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE id=$4 AND lease_id=$5 AND state='leased'`, state, message, time.Now().UTC().Add(delay), item.ID, item.LeaseID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return ErrSpaceConflict
		}
		return err
	})
}

func (db *Database) MarkAIInvocationDispatched(ctx context.Context, id, runtimeKind, runtimeID string) error {
	if strings.TrimSpace(runtimeID) == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET runtime_kind=$1,runtime_run_id=$2,updated_at=NOW() WHERE id=$3 AND state IN ('queued','running') AND (runtime_run_id='' OR runtime_run_id=$2)`, runtimeKind, runtimeID, id)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return ErrSpaceConflict
		}
		return err
	})
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// CommitAIInvocationEvent serializes writers across replicas and records a
// callback receipt before the caller acknowledges or publishes the event.
func (db *Database) CommitAIInvocationEvent(ctx context.Context, userID, invocationID, receipt, eventType string, payload json.RawMessage, nextState string) (AIInvocationEventRecord, error) {
	var event AIInvocationEventRecord
	var value map[string]any
	if receipt == "" || len(receipt) > 1000 || eventType == "" || json.Unmarshal(payload, &value) != nil || value == nil {
		return event, ErrSpaceInvalid
	}
	delete(value, "id")
	canonical, err := json.Marshal(value)
	if err != nil {
		return event, err
	}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state, runtimeID string
		if err := tx.QueryRowContext(ctx, `SELECT state,runtime_run_id FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`, invocationID, userID).Scan(&state, &runtimeID); err != nil {
			return err
		}
		var same bool
		lookupErr := tx.QueryRowContext(ctx, `SELECT sequence,event_type,payload,created_at,(payload-'id'=$3::jsonb) FROM ai_invocation_events WHERE invocation_id=$1 AND receipt_key=$2`, invocationID, receipt, canonical).Scan(&event.Sequence, &event.EventType, &event.Payload, &event.CreatedAt, &same)
		if lookupErr == nil {
			if !same || event.EventType != eventType {
				return ErrSpaceConflict
			}
			return nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return lookupErr
		}
		if state == "completed" || state == "failed" || state == "canceled" {
			return ErrSpaceConflict
		}
		if nextState == "" {
			nextState = state
		}
		if nextState != "queued" && nextState != "running" && nextState != "awaiting_approval" && nextState != "awaiting_device" && nextState != "awaiting_intervention" && nextState != "awaiting_timer" && nextState != "completed" && nextState != "failed" && nextState != "canceled" {
			return ErrSpaceInvalid
		}
		if nextState == "queued" && state != "queued" {
			return ErrSpaceConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM ai_invocation_events WHERE invocation_id=$1`, invocationID).Scan(&event.Sequence); err != nil {
			return err
		}
		value["id"] = strconv.FormatInt(event.Sequence, 10)
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, invocationID, event.Sequence, eventType, encoded, receipt).Scan(&event.CreatedAt); err != nil {
			return err
		}
		event.Payload, event.EventType = encoded, eventType
		_, err = tx.ExecContext(ctx, `UPDATE ai_invocations SET state=$1,updated_at=NOW(),canceled_at=CASE WHEN $1='canceled' THEN NOW() ELSE canceled_at END WHERE id=$2 AND user_id=$3`, nextState, invocationID, userID)
		if err != nil {
			return err
		}
		if nextState == "canceled" && runtimeID != "" {
			if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
				return err
			}
			return queueAgentContinuationTx(ctx, tx, userID, invocationID, "runtime.cancel", runtimeID, AgentContinuation{RuntimeID: runtimeID})
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return event, err
}

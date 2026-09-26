package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type SDKInvocationRecord struct {
	Request           cap.Invocation
	InvocationID      string
	EffectID          string
	CallerAppID       string
	State             string
	AdapterVersion    string
	OutcomeStatus     string
	OutcomeCiphertext []byte
	CancelRequested   bool
}

// The SDK's UUID run identity is a compatibility projection of the same existing
// invocation ID. It is not a second run or a second execution state machine.
func SDKPublicRunID(invocationID string) string {
	return strings.TrimPrefix(invocationID, "invocation_")
}

func (db *Database) SDKInvocationForRun(ctx context.Context, userID, invocationID string) (*SDKInvocationRecord, error) {
	var requestID, caller string
	var result *SDKInvocationRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT request_id,caller_app_id FROM sdk_capability_invocations WHERE user_id=$1 AND invocation_id=$2`, userID, invocationID).Scan(&requestID, &caller); err != nil {
			return err
		}
		var err error
		result, err = sdkInvocationByRequestTx(ctx, tx, userID, caller, requestID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return result, err
}

func (db *Database) StoreSDKObservedOutcome(ctx context.Context, userID, invocationID, effectID string, ciphertext []byte) error {
	if len(ciphertext) < 17 || len(ciphertext) > 2<<20 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE sdk_capability_invocations SET observed_outcome_ciphertext=$4 WHERE user_id=$1 AND invocation_id=$2 AND effect_id=$3 AND observed_outcome_ciphertext IS NULL`, userID, invocationID, effectID, ciphertext)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrSpaceConflict
		}
		return nil
	})
}
func (db *Database) StoreSDKWaitOutcome(ctx context.Context, userID, invocationID, status string, ciphertext []byte) error {
	if status != "approval_required" || len(ciphertext) < 17 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE sdk_capability_invocations c SET outcome_status=$3,outcome_ciphertext=$4 FROM ai_invocations i WHERE c.invocation_id=i.id AND c.user_id=$1 AND c.invocation_id=$2 AND i.state='awaiting_approval'`, userID, invocationID, status, ciphertext)
		return err
	})
}

type SDKCompletionEvidence struct {
	JournalState    string
	Proof           []byte
	CancelRequested bool
}

func (db *Database) SDKInvocationCompletionEvidence(ctx context.Context, userID, invocationID string) (*SDKCompletionEvidence, error) {
	var result SDKCompletionEvidence
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(j.state,''),c.observed_outcome_ciphertext,c.cancel_requested_at IS NOT NULL FROM sdk_capability_invocations c LEFT JOIN agent_toolbox_action_journal j ON j.idempotency_key='sdk-effect:'||c.effect_id::text WHERE c.user_id=$1 AND c.invocation_id=$2`, userID, invocationID).Scan(&result.JournalState, &result.Proof, &result.CancelRequested)
	})
	return &result, err
}

// Completion is serialized on the same invocation row used by ordinary event
// publication. The runtime's final prose is not evidence of a successful effect.
func (db *Database) PublishSDKCompletion(ctx context.Context, userID, invocationID, status, state string, ciphertext []byte) error {
	validPair := (status == "success" && state == "completed") || (status == "uncertain" && state == "failed") || (status == "failure" && (state == "failed" || state == "canceled"))
	if !validPair || len(ciphertext) < 17 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, invocationID).Scan(&current); err != nil {
			return err
		}
		var priorStatus string
		if err := tx.QueryRowContext(ctx, `SELECT outcome_status FROM sdk_capability_invocations WHERE user_id=$1 AND invocation_id=$2`, userID, invocationID).Scan(&priorStatus); err != nil {
			return err
		}
		if current == "completed" || current == "failed" || current == "canceled" {
			if priorStatus == "success" || priorStatus == "failure" || priorStatus == "uncertain" {
				return nil
			}
		}
		if status == "success" {
			var confirmed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sdk_capability_invocations c JOIN agent_toolbox_action_journal j ON j.idempotency_key='sdk-effect:'||c.effect_id::text WHERE c.user_id=$1 AND c.invocation_id=$2 AND j.state='completed' AND c.observed_outcome_ciphertext IS NOT NULL)`, userID, invocationID).Scan(&confirmed); err != nil {
				return err
			}
			if !confirmed {
				return ErrSpaceConflict
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sdk_capability_invocations SET outcome_status=$3,outcome_ciphertext=$4 WHERE user_id=$1 AND invocation_id=$2`, userID, invocationID, status, ciphertext); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state=$3,approval_wait_id='',updated_at=NOW() WHERE user_id=$1 AND id=$2`, userID, invocationID, state); err != nil {
			return err
		}
		eventType := "invocation." + state
		payload, _ := json.Marshal(map[string]any{"type": eventType, "state": state, "sdk_outcome": status})
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) SELECT $1,COALESCE(MAX(sequence),0)+1,$2,$3,'sdk:completion' FROM ai_invocation_events WHERE invocation_id=$1 ON CONFLICT DO NOTHING`, invocationID, eventType, payload)
		return err
	})
}

func sdkInvocationByRequestTx(ctx context.Context, tx *sql.Tx, userID, caller, requestID string) (*SDKInvocationRecord, error) {
	var result SDKInvocationRecord
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT c.request,c.invocation_id,c.effect_id,c.caller_app_id,c.adapter_version,c.outcome_status,c.outcome_ciphertext,c.cancel_requested_at IS NOT NULL,i.state FROM sdk_capability_invocations c JOIN ai_invocations i ON i.id=c.invocation_id WHERE c.user_id=$1 AND c.caller_app_id=$2 AND c.request_id=$3`, userID, caller, requestID).Scan(&raw, &result.InvocationID, &result.EffectID, &result.CallerAppID, &result.AdapterVersion, &result.OutcomeStatus, &result.OutcomeCiphertext, &result.CancelRequested, &result.State)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	if cap.Decode(raw, &result.Request) != nil {
		return nil, ErrSpaceInvalid
	}
	return &result, nil
}

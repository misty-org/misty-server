package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// AIInvocationDeviceWait checks or admits a durable wait for one exact call.
// Delivery retries may observe the same wait; another call cannot replace it.
func (db *Database) AIInvocationDeviceWait(ctx context.Context, userID, runID, runtimeID, callID, hook, scope, capability, argumentsHash string, begin bool) (bool, error) {
	if callID == "" || len(callID) > 200 || (begin && (hook == "" || len(hook) > 500)) || scope == "" || !strings.HasPrefix(capability, "browser.") || len(argumentsHash) != 64 {
		return false, ErrSpaceInvalid
	}
	waiting := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, _, err := deviceRunAuthorityTx(ctx, tx, userID, runID, &runtimeID, capability, true); err != nil {
			return err
		}
		var state, oldHook, oldCall, oldScope, oldCapability, oldHash string
		if err := tx.QueryRowContext(ctx, `SELECT state,device_wait_hook_token,device_wait_call_id,device_wait_scope_id,device_wait_capability,device_wait_arguments_hash FROM ai_invocations WHERE id=$1 AND user_id=$2`, runID, userID).Scan(&state, &oldHook, &oldCall, &oldScope, &oldCapability, &oldHash); err != nil {
			return err
		}
		if state == "awaiting_device" {
			if oldHook != hook || oldCall != callID || oldScope != scope || oldCapability != capability || oldHash != argumentsHash {
				return ErrSpaceConflict
			}
			waiting = true
			return nil
		}
		if !begin {
			return nil
		}
		if state != "running" {
			return ErrSpaceConflict
		}
		var target string
		var expiry time.Time
		var count int
		err := tx.QueryRowContext(ctx, `SELECT c.id,LEAST(c.expires_at,i.expires_at,NOW()+INTERVAL '24 hours'),COUNT(*) OVER() FROM ai_invocation_contexts c JOIN ai_invocations i ON i.id=c.invocation_id AND i.space_id=c.space_id AND i.user_id=c.user_id JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=c.user_id AND d.revoked_at IS NULL WHERE c.invocation_id=$1 AND c.user_id=$2 AND c.opaque_ref=$3 AND c.capabilities ? $4 AND c.state='attached' AND c.expires_at>NOW() AND i.expires_at>NOW()`, runID, userID, scope, capability).Scan(&target, &expiry, &count)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrSpaceConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE ai_invocations SET state='awaiting_device',device_wait_hook_token=$2,device_wait_expires_at=$3,device_wait_context_id=$4,device_wait_scope_id=$5,device_wait_capability=$6,device_wait_call_id=$7,device_wait_arguments_hash=$8,updated_at=NOW() WHERE id=$1`, runID, hook, expiry, target, scope, capability, callID, argumentsHash)
		if err != nil {
			return err
		}
		if err := aiDeviceWaitStatusTx(ctx, tx, runID, hook, "awaiting_device", "Waiting for the attached browser device. Reconnect it to continue."); err != nil {
			return err
		}
		waiting = true
		return nil
	})
	return waiting, err
}

const aiDeviceReady = `EXISTS(SELECT 1 FROM ai_invocation_contexts c JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=c.user_id WHERE c.id=i.device_wait_context_id AND c.invocation_id=i.id AND c.user_id=i.user_id AND c.space_id=i.space_id AND c.opaque_ref=i.device_wait_scope_id AND c.capabilities ? i.device_wait_capability AND c.state='attached' AND c.expires_at>NOW() AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds')`

func (db *Database) AIInvocationDeviceWaitsReady(ctx context.Context, limit int) ([]AgentDeviceWait, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AgentDeviceWait{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.device_wait_hook_token,`+aiDeviceReady+` FROM ai_invocations i WHERE i.state='awaiting_device' AND i.device_wait_hook_token<>'' AND (i.device_wait_expires_at<=NOW() OR `+aiDeviceReady+`) ORDER BY i.updated_at,i.id LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentDeviceWait
			if err := rows.Scan(&item.RunID, &item.HookToken, &item.Available); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) queueAIInvocationDeviceResume(ctx context.Context, wait AgentDeviceWait) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var user, runtime, capability string
		var expired, available bool
		err := tx.QueryRowContext(ctx, `SELECT i.user_id,i.runtime_run_id,i.device_wait_capability,i.device_wait_expires_at<=NOW(),`+aiDeviceReady+` FROM ai_invocations i WHERE i.id=$1 AND i.device_wait_hook_token=$2 AND i.state='awaiting_device' AND COALESCE(i.agent_run_id,'')='' FOR UPDATE`, wait.RunID, wait.HookToken).Scan(&user, &runtime, &capability, &expired, &available)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceConflict
		}
		if err != nil {
			return err
		}
		if !expired && !available {
			return ErrSpaceConflict
		}
		// A revoked principal must never receive an affirmative continuation.
		if _, _, err := deviceRunAuthorityTx(ctx, tx, user, wait.RunID, &runtime, capability, true); err != nil {
			if !deviceAuthorityDenied(err) {
				return err
			}
			available = false
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='running',updated_at=NOW() WHERE id=$1`, wait.RunID); err != nil {
			return err
		}
		text := "The attached browser is available. Rechecking the action before continuing."
		if expired || !available {
			text = "The browser wait ended without an available authorized device."
		}
		if err := aiDeviceWaitStatusTx(ctx, tx, wait.RunID, wait.HookToken, "device_resume_pending", text); err != nil {
			return err
		}
		return queueAgentContinuationTx(ctx, tx, user, wait.RunID, "device.resume", wait.HookToken, AgentContinuation{RuntimeID: runtime, HookToken: wait.HookToken, Available: available && !expired})
	})
}

// Caller holds the invocation lock, the same sequencing lock used by ordinary
// runtime events. Status and the corresponding state transition commit together.
func aiDeviceWaitStatusTx(ctx context.Context, tx *sql.Tx, runID, hook, phase, text string) error {
	hash := sha256.Sum256([]byte(hook))
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) SELECT $1,n,'assistant.status',jsonb_build_object('id',n::text,'type','assistant.status','phase',$3::text,'text',$4::text),$2 FROM (SELECT COALESCE(MAX(sequence),0)+1 n FROM ai_invocation_events WHERE invocation_id=$1) seq ON CONFLICT(invocation_id,receipt_key) WHERE receipt_key IS NOT NULL DO NOTHING`, runID, "device:"+hex.EncodeToString(hash[:])+":"+phase, phase, text)
	return err
}

func (db *Database) AIInvocationDeviceResumeAuthorized(ctx context.Context, delivery AgentRuntimeDelivery, payload AgentContinuation) (bool, error) {
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var capability string
		if err := tx.QueryRowContext(ctx, `SELECT device_wait_capability FROM ai_invocations WHERE id=$1 AND user_id=$2 AND runtime_run_id=$3 AND device_wait_hook_token=$4 AND state IN ('running','awaiting_approval','awaiting_device') FOR UPDATE`, delivery.RunID, delivery.UserID, payload.RuntimeID, payload.HookToken).Scan(&capability); err != nil {
			return err
		}
		_, _, err := deviceRunAuthorityTx(ctx, tx, delivery.UserID, delivery.RunID, &payload.RuntimeID, capability, true)
		if deviceAuthorityDenied(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations i JOIN ai_invocation_contexts c ON c.id=i.device_wait_context_id AND c.invocation_id=i.id AND c.user_id=i.user_id AND c.space_id=i.space_id JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=i.user_id WHERE i.id=$1 AND c.state='attached' AND c.expires_at>NOW() AND c.opaque_ref=i.device_wait_scope_id AND c.capabilities ? i.device_wait_capability AND d.revoked_at IS NULL)`, delivery.RunID).Scan(&allowed)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return allowed, err
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
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

func (db *Database) AdmitSDKInvocation(ctx context.Context, userID string, request cap.Invocation) (*SDKInvocationRecord, error) {
	if err := request.Validate(time.Now()); err != nil {
		return nil, err
	}
	caller := ""
	a := AppAuthorityFromContext(ctx)
	if a != nil {
		caller = a.AppID
		if err := db.ValidateAppExecutionAuthority(ctx, a, userID, a.SpaceID, "capabilities.invoke"); err != nil {
			return nil, err
		}
	}
	raw, _ := json.Marshal(request)
	var result SDKInvocationRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		// Serialize logical request identities, including changed-argument retries.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sdk:request:"+userID+":"+caller+":"+request.RequestID); err != nil {
			return err
		}
		existing, err := sdkInvocationByRequestTx(ctx, tx, userID, caller, request.RequestID)
		if err == nil {
			previous, _ := json.Marshal(existing.Request)
			if !cap.EqualJSON(previous, raw) {
				return ErrSpaceConflict
			}
			result = *existing
			return nil
		}
		if !errors.Is(err, ErrSpaceNotFound) {
			return err
		}
		bound, err := resolveSDKBoundCapabilityTx(ctx, tx, userID, request.TargetID, request.TargetRevision, request.Capability, request.CapabilityVersion)
		if err != nil {
			return err
		}
		if bound.Provider.ID != request.ProviderID || bound.Provider.Version != request.ProviderVersion {
			return ErrAppRuntimeForbidden
		}
		if a != nil {
			var scopesRaw []byte
			var generation int64
			if err := tx.QueryRowContext(ctx, `SELECT granted_scopes,authority_generation FROM space_app_installations WHERE space_id=$1 AND app_id=$2 AND state='installed' FOR SHARE`, a.SpaceID, a.AppID).Scan(&scopesRaw, &generation); err != nil {
				return err
			}
			var scopes []string
			if json.Unmarshal(scopesRaw, &scopes) != nil || generation != a.Generation || !cap.HasScopes(scopes, []string{"capabilities.invoke"}) {
				return ErrAppRuntimeForbidden
			}
		}
		schema, err := cap.CompileSchema(bound.Definition.InputSchema)
		if err != nil {
			return err
		}
		var input any
		if json.Unmarshal(request.Input, &input) != nil || schema.Validate(input) != nil {
			return cap.ErrInvalid
		}
		invocationID := "invocation_" + uuid.NewString()
		effectID := uuid.NewString()
		payload, _ := json.Marshal(map[string]any{"_misty_sdk_execution": true, "request_id": request.RequestID})
		payload, err = bindAppAuthority(ctx, payload)
		if err != nil {
			return err
		}
		record, created, err := createAIInvocationRecordTx(ctx, tx, AIInvocationRecord{ID: invocationID, UserID: userID, SpaceID: bound.Target.SpaceID, SurfaceID: "sdk", Mode: "quick", Trigger: "object", State: "queued", IdempotencyKey: "sdk:" + caller + ":" + request.RequestID, RequestPayload: payload, ExpiresAt: request.Deadline, DispatchRuntime: true})
		if err != nil {
			return err
		}
		if !created {
			return ErrSpaceConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sdk_capability_invocations(user_id,request_id,caller_app_id,invocation_id,effect_id,request,target_id,target_revision,adapter_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, userID, request.RequestID, caller, record.ID, effectID, raw, request.TargetID, request.TargetRevision, cap.ExecutionAdapterVersion(bound.Provider))
		if err != nil {
			return err
		}
		result = SDKInvocationRecord{Request: request, InvocationID: record.ID, EffectID: effectID, CallerAppID: caller, State: record.State, AdapterVersion: cap.ExecutionAdapterVersion(bound.Provider)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
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
func (db *Database) SDKInvocationByRequest(ctx context.Context, userID, requestID string) (*SDKInvocationRecord, error) {
	if !cap.ValidID(requestID) {
		return nil, cap.ErrInvalid
	}
	caller := ""
	if a := AppAuthorityFromContext(ctx); a != nil {
		caller = a.AppID
		if err := db.ValidateAppExecutionAuthority(ctx, a, userID, a.SpaceID, "capabilities.read"); err != nil {
			return nil, err
		}
	}
	var result *SDKInvocationRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		result, err = sdkInvocationByRequestTx(ctx, tx, userID, caller, requestID)
		if err != nil {
			return err
		}
		if authority := AppAuthorityFromContext(ctx); authority != nil {
			// Result payloads retain provider data. A freshly issued credential must
			// not recover data admitted under a revoked installation generation.
			var payload []byte
			if err := tx.QueryRowContext(ctx, `SELECT request_payload FROM ai_invocations WHERE id=$1 AND user_id=$2`, result.InvocationID, userID).Scan(&payload); err != nil {
				return err
			}
			admitted, err := ContextWithPersistedAppAuthority(context.Background(), payload)
			if err != nil {
				return err
			}
			original := AppAuthorityFromContext(admitted)
			if original == nil || original.Generation != authority.Generation || original.AppID != authority.AppID {
				return ErrAppRuntimeForbidden
			}
			_, err = resolveSDKBoundCapabilityTx(ctx, tx, userID, result.Request.TargetID, result.Request.TargetRevision, result.Request.Capability, result.Request.CapabilityVersion)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
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
func (db *Database) RequestSDKCancellation(ctx context.Context, userID, invocationID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var state, runtimeID string
		if err := tx.QueryRowContext(ctx, `SELECT state,runtime_run_id FROM ai_invocations WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, invocationID).Scan(&state, &runtimeID); err != nil {
			return err
		}
		if state == "completed" || state == "failed" || state == "canceled" {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sdk_capability_invocations SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()) WHERE user_id=$1 AND invocation_id=$2`, userID, invocationID); err != nil {
			return err
		}
		if runtimeID != "" {
			if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
				return err
			}
			return queueAgentContinuationTx(ctx, tx, userID, invocationID, "runtime.cancel", runtimeID, AgentContinuation{RuntimeID: runtimeID})
		}
		_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',updated_at=NOW() WHERE user_id=$1 AND id=$2 AND runtime_run_id=''`, userID, invocationID)
		return err
	})
}

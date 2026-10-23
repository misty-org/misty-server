package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrAIConversationBusy = errors.New("conversation already has an active invocation")

type AIInvocationRecord struct {
	ModelTurnLimit int
	// ExecutionLimitMS is a development eval's own execution allowance; 0 keeps the default.
	ExecutionLimitMS   int64
	DispatchRuntime    bool
	ID                 string
	UserID             string
	SpaceID            string
	ConversationID     string
	SurfaceID          string
	Mode               string
	Trigger            string
	State              string
	IdempotencyKey     string
	RequestPayload     json.RawMessage
	RuntimeKind        string
	RuntimeRunID       string
	AgentRunID         string
	RuntimeHeartbeatAt *time.Time
	ExpiresAt          time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreateAIInvocationRecord makes the database idempotency journal the durable
// authority. The returned boolean is false when a retry found the existing row.
func (db *Database) CreateAIInvocationRecord(ctx context.Context, record AIInvocationRecord) (AIInvocationRecord, bool, error) {
	record.SpaceID = "" // All AI admission is account-scoped; sources carry their own destinations.
	var stored AIInvocationRecord
	var created bool
	err := db.WithRLSContext(ctx, userRLSSettings(record.UserID), func(tx *sql.Tx) error {
		var err error
		stored, created, err = createAIInvocationRecordTx(ctx, tx, record)
		return err
	})
	return stored, created, err
}

// The caller can commit its pinned request metadata in this same transaction.
func createAIInvocationRecordTx(ctx context.Context, tx *sql.Tx, record AIInvocationRecord) (AIInvocationRecord, bool, error) {
	record.SpaceID = ""
	stored := AIInvocationRecord{}
	created := false
	err := func() error {

		if record.SpaceID != "" {
			var member bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2)`, record.SpaceID, record.UserID).Scan(&member); err != nil {
				return err
			}
			if !member {
				return ErrSpaceForbidden
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, record.UserID); err != nil {
			return err
		}
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM ai_user_settings WHERE user_id=$1 FOR SHARE`, record.UserID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return ErrSpaceForbidden
		}
		if record.ConversationID != "" {
			// Serialize admission across every window/surface using the canonical
			// conversation row. An idempotent retry still returns its original run.
			var id string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`, record.ConversationID, record.UserID).Scan(&id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrSpaceNotFound
				}
				return err
			}
			var busy, retry bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations WHERE user_id=$1 AND conversation_id=$2 AND idempotency_key<>$3 AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer'))`, record.UserID, record.ConversationID, record.IdempotencyKey).Scan(&busy); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocations WHERE user_id=$1 AND idempotency_key=$2)`, record.UserID, record.IdempotencyKey).Scan(&retry); err != nil {
				return err
			}
			if busy && !retry {
				return ErrAIConversationBusy
			}
		}
		modelTurnLimit, err := invocationModelTurnLimitTx(ctx, tx, record)
		if err != nil {
			return err
		}
		var inserted bool
		err = tx.QueryRowContext(ctx, `
			INSERT INTO ai_invocations(id,user_id,space_id,conversation_id,surface_id,mode,trigger_kind,state,idempotency_key,request_payload,expires_at,model_turn_limit,execution_limit_ms)
			VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,COALESCE(NULLIF($13::bigint,0),1800000))
			ON CONFLICT(user_id,idempotency_key) DO UPDATE SET updated_at=ai_invocations.updated_at
				RETURNING id,user_id,COALESCE(space_id,''),COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
					runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at,model_turn_limit,(xmax=0)
			`, record.ID, record.UserID, record.SpaceID, record.ConversationID, record.SurfaceID, record.Mode, record.Trigger, record.State, record.IdempotencyKey, record.RequestPayload, record.ExpiresAt, modelTurnLimit, record.ExecutionLimitMS).Scan(
			&stored.ID, &stored.UserID, &stored.SpaceID, &stored.ConversationID, &stored.SurfaceID, &stored.Mode, &stored.Trigger, &stored.State, &stored.IdempotencyKey, &stored.RequestPayload,
			&stored.RuntimeKind, &stored.RuntimeRunID, &stored.AgentRunID, &stored.RuntimeHeartbeatAt, &stored.ExpiresAt, &stored.CreatedAt, &stored.UpdatedAt, &stored.ModelTurnLimit, &inserted,
		)
		created = inserted
		if err == nil && created && record.DispatchRuntime {
			// Admission and dispatch intent commit together. Service context applies
			// only to the private delivery row; invocation ownership was checked above.
			if _, setErr := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); setErr != nil {
				return setErr
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO agent_runtime_deliveries(id,user_id,run_id,operation) VALUES($1,$2,$3,'invocation.start')`, "start:"+stored.ID, stored.UserID, stored.ID)
		}
		return err
	}()
	return stored, created, err
}

func (db *Database) AIInvocationByID(ctx context.Context, userID, invocationID string) (*AIInvocationRecord, error) {
	result := &AIInvocationRecord{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id,user_id,COALESCE(space_id,''),COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
				runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at,model_turn_limit
			FROM ai_invocations WHERE id=$1 AND user_id=$2
		`, invocationID, userID).Scan(&result.ID, &result.UserID, &result.SpaceID, &result.ConversationID, &result.SurfaceID, &result.Mode, &result.Trigger, &result.State, &result.IdempotencyKey, &result.RequestPayload,
			&result.RuntimeKind, &result.RuntimeRunID, &result.AgentRunID, &result.RuntimeHeartbeatAt, &result.ExpiresAt, &result.CreatedAt, &result.UpdatedAt, &result.ModelTurnLimit)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return result, err
}

// ActivateAIInvocationRuntime binds a signed durable runtime identity to an
// invocation. Replays from the same workflow are idempotent; a different
// workflow identity may never take over an active invocation.
func (db *Database) ActivateAIInvocationRuntime(ctx context.Context, invocationID, runtimeKind, runtimeRunID string) (*AIInvocationRecord, error) {
	runtimeKind, runtimeRunID = strings.TrimSpace(runtimeKind), strings.TrimSpace(runtimeRunID)
	if runtimeKind == "" || runtimeRunID == "" {
		return nil, ErrSpaceInvalid
	}
	out := &AIInvocationRecord{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE ai_invocations SET
			runtime_kind=$1,runtime_run_id=$2,runtime_heartbeat_at=NOW(),state='running',updated_at=NOW()
			WHERE id=$3 AND state IN ('queued','running') AND (runtime_run_id='' OR runtime_run_id=$2)
			RETURNING id,user_id,COALESCE(space_id,''),COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
				runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at,model_turn_limit`, runtimeKind, runtimeRunID, invocationID).Scan(
			&out.ID, &out.UserID, &out.SpaceID, &out.ConversationID, &out.SurfaceID, &out.Mode, &out.Trigger, &out.State, &out.IdempotencyKey, &out.RequestPayload,
			&out.RuntimeKind, &out.RuntimeRunID, &out.AgentRunID, &out.RuntimeHeartbeatAt, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt, &out.ModelTurnLimit)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceConflict
	}
	return out, err
}

// ValidateAIInvocationRuntime revalidates the opaque runtime binding on every
// context, tool, event, and completion callback.
func (db *Database) ValidateAIInvocationRuntime(ctx context.Context, invocationID, runtimeRunID string) (*AIInvocationRecord, error) {
	out := &AIInvocationRecord{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,COALESCE(space_id,''),COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
			runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at,model_turn_limit
			FROM ai_invocations WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer')`, invocationID, runtimeRunID).Scan(
			&out.ID, &out.UserID, &out.SpaceID, &out.ConversationID, &out.SurfaceID, &out.Mode, &out.Trigger, &out.State, &out.IdempotencyKey, &out.RequestPayload,
			&out.RuntimeKind, &out.RuntimeRunID, &out.AgentRunID, &out.RuntimeHeartbeatAt, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt, &out.ModelTurnLimit)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	if err == nil {
		err = db.ValidateNativeAgentExecution(ctx, out)
	}
	return out, err
}

func (db *Database) AIInvocationRuntimeRecord(ctx context.Context, invocationID, runtimeRunID string) (*AIInvocationRecord, error) {
	out := &AIInvocationRecord{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,COALESCE(space_id,''),COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
			runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at,model_turn_limit
			FROM ai_invocations WHERE id=$1 AND runtime_run_id=$2`, invocationID, runtimeRunID).Scan(
			&out.ID, &out.UserID, &out.SpaceID, &out.ConversationID, &out.SurfaceID, &out.Mode, &out.Trigger, &out.State, &out.IdempotencyKey, &out.RequestPayload,
			&out.RuntimeKind, &out.RuntimeRunID, &out.AgentRunID, &out.RuntimeHeartbeatAt, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt, &out.ModelTurnLimit)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return out, err
}

func (db *Database) TouchAIInvocationRuntime(ctx context.Context, invocationID, runtimeRunID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET runtime_heartbeat_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer')`, invocationID, runtimeRunID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrSpaceForbidden
		}
		return nil
	})
}

func jsonOr(value json.RawMessage, fallback string) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(fallback)
	}
	return value
}

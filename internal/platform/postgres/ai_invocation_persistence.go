package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AIInvocationRecord struct {
	ID                 string
	UserID             string
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
	stored := AIInvocationRecord{}
	created := false
	err := db.TestingWithRLSContext(ctx, userRLSSettings(record.UserID), func(tx *sql.Tx) error {
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
		var inserted bool
		err := tx.QueryRowContext(ctx, `
			INSERT INTO ai_invocations(id,user_id,conversation_id,surface_id,mode,trigger_kind,state,idempotency_key,request_payload,expires_at)
			VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT(user_id,idempotency_key) DO UPDATE SET updated_at=ai_invocations.updated_at
				RETURNING id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
					runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at,(xmax=0)
			`, record.ID, record.UserID, record.ConversationID, record.SurfaceID, record.Mode, record.Trigger, record.State, record.IdempotencyKey, record.RequestPayload, record.ExpiresAt).Scan(
			&stored.ID, &stored.UserID, &stored.ConversationID, &stored.SurfaceID, &stored.Mode, &stored.Trigger, &stored.State, &stored.IdempotencyKey, &stored.RequestPayload,
			&stored.RuntimeKind, &stored.RuntimeRunID, &stored.AgentRunID, &stored.RuntimeHeartbeatAt, &stored.ExpiresAt, &stored.CreatedAt, &stored.UpdatedAt, &inserted,
		)
		created = inserted
		return err
	})
	return stored, created, err
}

func (db *Database) AIInvocationByID(ctx context.Context, userID, invocationID string) (*AIInvocationRecord, error) {
	result := &AIInvocationRecord{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
				runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at
			FROM ai_invocations WHERE id=$1 AND user_id=$2
		`, invocationID, userID).Scan(&result.ID, &result.UserID, &result.ConversationID, &result.SurfaceID, &result.Mode, &result.Trigger, &result.State, &result.IdempotencyKey, &result.RequestPayload,
			&result.RuntimeKind, &result.RuntimeRunID, &result.AgentRunID, &result.RuntimeHeartbeatAt, &result.ExpiresAt, &result.CreatedAt, &result.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return result, err
}

func (db *Database) LinkAIInvocationAgentRun(ctx context.Context, userID, invocationID, runID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET agent_run_id=$1,updated_at=NOW()
			WHERE id=$2 AND user_id=$3 AND (agent_run_id=$1 OR (state='queued' AND agent_run_id IS NULL))`, runID, invocationID, userID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrSpaceConflict
		}
		return nil
	})
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
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE ai_invocations SET
			runtime_kind=$1,runtime_run_id=$2,runtime_heartbeat_at=NOW(),state='running',updated_at=NOW()
			WHERE id=$3 AND state IN ('queued','running') AND (runtime_run_id='' OR runtime_run_id=$2)
			RETURNING id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
				runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at`, runtimeKind, runtimeRunID, invocationID).Scan(
			&out.ID, &out.UserID, &out.ConversationID, &out.SurfaceID, &out.Mode, &out.Trigger, &out.State, &out.IdempotencyKey, &out.RequestPayload,
			&out.RuntimeKind, &out.RuntimeRunID, &out.AgentRunID, &out.RuntimeHeartbeatAt, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
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
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
			runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at
			FROM ai_invocations WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval')`, invocationID, runtimeRunID).Scan(
			&out.ID, &out.UserID, &out.ConversationID, &out.SurfaceID, &out.Mode, &out.Trigger, &out.State, &out.IdempotencyKey, &out.RequestPayload,
			&out.RuntimeKind, &out.RuntimeRunID, &out.AgentRunID, &out.RuntimeHeartbeatAt, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return out, err
}

func (db *Database) AIInvocationRuntimeRecord(ctx context.Context, invocationID, runtimeRunID string) (*AIInvocationRecord, error) {
	out := &AIInvocationRecord{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,
			runtime_kind,runtime_run_id,COALESCE(agent_run_id,''),runtime_heartbeat_at,expires_at,created_at,updated_at
			FROM ai_invocations WHERE id=$1 AND runtime_run_id=$2`, invocationID, runtimeRunID).Scan(
			&out.ID, &out.UserID, &out.ConversationID, &out.SurfaceID, &out.Mode, &out.Trigger, &out.State, &out.IdempotencyKey, &out.RequestPayload,
			&out.RuntimeKind, &out.RuntimeRunID, &out.AgentRunID, &out.RuntimeHeartbeatAt, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return out, err
}

func (db *Database) TouchAIInvocationRuntime(ctx context.Context, invocationID, runtimeRunID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET runtime_heartbeat_at=NOW(),updated_at=NOW()
			WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval')`, invocationID, runtimeRunID)
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

func (db *Database) AppendAIInvocationEvent(ctx context.Context, userID, invocationID string, sequence int64, eventType string, payload json.RawMessage, state string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload)
			SELECT $1,$2,$3,$4 FROM ai_invocations WHERE id=$1 AND user_id=$5
			ON CONFLICT(invocation_id,sequence) DO NOTHING
		`, invocationID, sequence, eventType, payload, userID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE ai_invocations SET state=$1,updated_at=NOW(),
				canceled_at=CASE WHEN $1='canceled' THEN NOW() ELSE canceled_at END
			WHERE id=$2 AND user_id=$3
		`, state, invocationID, userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err == nil && rows == 0 {
			return ErrSpaceNotFound
		}
		return err
	})
}

type AIInvocationEventRecord struct {
	Sequence  int64
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// AIConversationTurnRecord is the user-facing projection of a companion
// invocation. The stored request contains the original prompt, while the
// invocation event stream contains the final reply or durable failure. Agent
// runtime messages are intentionally not used here because their user message
// includes the compiled context/security envelope sent to the model.
type AIConversationTurnRecord struct {
	InvocationID    string
	Prompt          string
	State           string
	Reply           string
	Status          string
	Failure         string
	AgentRunID      string
	AgentState      string
	AgentProgress   int
	AgentError      string
	ResultSpaceID   string
	ResultDrawingID string
	CreatedAt       time.Time
	ReplyAt         time.Time
}

func (db *Database) AIConversationTurns(ctx context.Context, userID, conversationID string) ([]AIConversationTurnRecord, error) {
	items := []AIConversationTurnRecord{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT invocation.id,
				COALESCE(invocation.request_payload->>'prompt',''),
				invocation.state,
				COALESCE(reply.payload->>'text',''),
				COALESCE(status.payload->>'text',''),
				COALESCE(failure.payload->>'error',''),
				COALESCE(run.id,''),
				COALESCE(run.state,''),
				COALESCE(run.progress,0),
				COALESCE(run.error_message,''),
				COALESCE(drawing.result->>'space_id',''),
				COALESCE(drawing.result->>'id',''),
				invocation.created_at,
				COALESCE(reply.created_at,failure.created_at,status.created_at,invocation.updated_at)
			FROM ai_invocations AS invocation
			LEFT JOIN space_runs AS run ON run.id=invocation.agent_run_id
			LEFT JOIN LATERAL (
				SELECT action.result
				FROM agent_toolbox_action_journal AS action
				WHERE action.run_id=run.id AND action.tool_name='drawings.create' AND action.state='completed'
				ORDER BY action.created_at DESC LIMIT 1
			) AS drawing ON TRUE
			LEFT JOIN LATERAL (
				SELECT event.payload,event.created_at
				FROM ai_invocation_events AS event
				WHERE event.invocation_id=invocation.id AND event.event_type='assistant.message'
				ORDER BY event.sequence DESC LIMIT 1
			) AS reply ON TRUE
			LEFT JOIN LATERAL (
				SELECT event.payload,event.created_at
				FROM ai_invocation_events AS event
				WHERE event.invocation_id=invocation.id AND event.event_type='assistant.status'
				ORDER BY event.sequence DESC LIMIT 1
			) AS status ON TRUE
			LEFT JOIN LATERAL (
				SELECT event.payload,event.created_at
				FROM ai_invocation_events AS event
				WHERE event.invocation_id=invocation.id AND event.event_type='invocation.failed'
				ORDER BY event.sequence DESC LIMIT 1
			) AS failure ON TRUE
			WHERE invocation.user_id=$1 AND invocation.conversation_id=$2
			ORDER BY invocation.created_at,invocation.id
		`, userID, conversationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIConversationTurnRecord
			if err := rows.Scan(
				&item.InvocationID, &item.Prompt, &item.State, &item.Reply,
				&item.Status, &item.Failure, &item.AgentRunID, &item.AgentState,
				&item.AgentProgress, &item.AgentError, &item.ResultSpaceID,
				&item.ResultDrawingID, &item.CreatedAt, &item.ReplyAt,
			); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AIInvocationEvents(ctx context.Context, userID, invocationID string, after int64) ([]AIInvocationEventRecord, string, error) {
	items := []AIInvocationEventRecord{}
	state := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2`, invocationID, userID).Scan(&state); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT sequence,event_type,payload,created_at FROM ai_invocation_events
			WHERE invocation_id=$1 AND sequence>$2 ORDER BY sequence
		`, invocationID, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIInvocationEventRecord
			if err := rows.Scan(&item.Sequence, &item.EventType, &item.Payload, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrSpaceNotFound
	}
	return items, state, err
}

func (db *Database) UpsertAIArtifact(ctx context.Context, userID, invocationID string, payload json.RawMessage) error {
	var artifact struct {
		ID             string          `json:"id"`
		SchemaVersion  int             `json:"schemaVersion"`
		Kind           string          `json:"kind"`
		Title          string          `json:"title"`
		Summary        string          `json:"summary"`
		Sources        json.RawMessage `json:"sources"`
		Target         json.RawMessage `json:"target"`
		BaseRevision   json.RawMessage `json:"baseRevision"`
		Operations     json.RawMessage `json:"operations"`
		Risk           string          `json:"risk"`
		ApprovalPolicy string          `json:"approvalPolicy"`
		IdempotencyKey string          `json:"idempotencyKey"`
		ExpiresAt      time.Time       `json:"expiresAt"`
		State          string          `json:"state"`
		Error          string          `json:"error"`
	}
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return err
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ai_artifacts(id,invocation_id,user_id,schema_version,kind,title,summary,sources,target,base_revision,operations,risk,approval_policy,idempotency_key,state,error_message,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9::jsonb,'null'::jsonb),NULLIF($10::jsonb,'null'::jsonb),$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT(id) DO UPDATE SET state=EXCLUDED.state,error_message=EXCLUDED.error_message,updated_at=NOW()
		`, artifact.ID, invocationID, userID, artifact.SchemaVersion, artifact.Kind, artifact.Title, artifact.Summary, jsonOr(artifact.Sources, `[]`), jsonOr(artifact.Target, `null`), jsonOr(artifact.BaseRevision, `null`), jsonOr(artifact.Operations, `{}`), artifact.Risk, artifact.ApprovalPolicy, artifact.IdempotencyKey, artifact.State, artifact.Error, artifact.ExpiresAt)
		return err
	})
}

func (db *Database) AIArtifactByID(ctx context.Context, userID, artifactID string) (json.RawMessage, error) {
	var payload json.RawMessage
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT jsonb_build_object(
				'id',id,'invocationId',invocation_id,'schemaVersion',schema_version,'kind',kind,'title',title,'summary',summary,
				'sources',sources,'target',target,'baseRevision',base_revision,'operations',operations,
				'risk',risk,'approvalPolicy',approval_policy,'idempotencyKey',idempotency_key,
				'expiresAt',expires_at,'state',state,'error',NULLIF(error_message,'')
			) FROM ai_artifacts WHERE id=$1 AND user_id=$2
		`, artifactID, userID).Scan(&payload)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return payload, err
}

func (db *Database) DecideAIArtifact(ctx context.Context, userID, artifactID, decision string) (string, error) {
	next := "applying"
	if decision == "reject" || decision == "refine" {
		next = "rejected"
	}
	state := ""
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			UPDATE ai_artifacts SET state=$1,decided_at=NOW(),updated_at=NOW()
			WHERE id=$2 AND user_id=$3 AND state='proposed' AND expires_at>NOW()
			RETURNING state
		`, next, artifactID, userID).Scan(&state)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceConflict
	}
	return state, err
}

func (db *Database) UpdateAIArtifactOperations(ctx context.Context, userID, artifactID string, operations json.RawMessage) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE ai_artifacts SET operations=$1,updated_at=NOW()
			WHERE id=$2 AND user_id=$3 AND state='proposed' AND expires_at>NOW()
		`, operations, artifactID, userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err == nil && rows == 0 {
			return ErrSpaceConflict
		}
		return err
	})
}

func (db *Database) CompleteAIArtifact(ctx context.Context, userID, artifactID, state, message string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE ai_artifacts SET state=$1,error_message=$2,completed_at=NOW(),updated_at=NOW()
			WHERE id=$3 AND user_id=$4 AND state='applying'
		`, state, message, artifactID, userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err == nil && rows == 0 {
			return ErrSpaceConflict
		}
		return err
	})
}

func jsonOr(value json.RawMessage, fallback string) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(fallback)
	}
	return value
}

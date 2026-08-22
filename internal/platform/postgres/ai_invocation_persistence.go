package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type AIInvocationRecord struct {
	ID             string
	UserID         string
	ConversationID string
	SurfaceID      string
	Mode           string
	Trigger        string
	State          string
	IdempotencyKey string
	RequestPayload json.RawMessage
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
			RETURNING id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,expires_at,created_at,updated_at,(xmax=0)
		`, record.ID, record.UserID, record.ConversationID, record.SurfaceID, record.Mode, record.Trigger, record.State, record.IdempotencyKey, record.RequestPayload, record.ExpiresAt).Scan(
			&stored.ID, &stored.UserID, &stored.ConversationID, &stored.SurfaceID, &stored.Mode, &stored.Trigger, &stored.State, &stored.IdempotencyKey, &stored.RequestPayload, &stored.ExpiresAt, &stored.CreatedAt, &stored.UpdatedAt, &inserted,
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
			SELECT id,user_id,COALESCE(conversation_id,''),surface_id,mode,trigger_kind,state,idempotency_key,request_payload,expires_at,created_at,updated_at
			FROM ai_invocations WHERE id=$1 AND user_id=$2
		`, invocationID, userID).Scan(&result.ID, &result.UserID, &result.ConversationID, &result.SurfaceID, &result.Mode, &result.Trigger, &result.State, &result.IdempotencyKey, &result.RequestPayload, &result.ExpiresAt, &result.CreatedAt, &result.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return result, err
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
				'id',id,'schemaVersion',schema_version,'kind',kind,'title',title,'summary',summary,
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

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

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
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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

func (db *Database) CompleteAIArtifact(ctx context.Context, userID, artifactID, state, message string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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

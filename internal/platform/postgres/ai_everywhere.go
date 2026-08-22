package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (db *Database) AIActionAvailable(ctx context.Context, userID, surfaceID, actionID, modelID string) (bool, error) {
	enabled := true
	rollout := 100
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var userEnabled bool
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM ai_user_settings WHERE user_id=$1),TRUE)`, userID).Scan(&userEnabled); err != nil {
			return err
		}
		if !userEnabled {
			enabled = false
			rollout = 0
			return nil
		}
		err := tx.QueryRowContext(ctx, `
			SELECT enabled,rollout_percent FROM ai_feature_flags
			WHERE surface_id IN ('*',$1) AND action_id IN ('*',$2) AND model_id IN ('*',$3)
			ORDER BY (surface_id=$1)::int+(action_id=$2)::int+(model_id=$3)::int DESC,updated_at DESC
			LIMIT 1
		`, surfaceID, actionID, modelID).Scan(&enabled, &rollout)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil || !enabled || rollout <= 0 {
		return false, err
	}
	if rollout >= 100 {
		return true, nil
	}
	digest := sha256.Sum256([]byte(userID + "\x00" + surfaceID + "\x00" + actionID + "\x00" + modelID))
	cohort := int(digest[0])<<8 | int(digest[1])
	return cohort%100 < rollout, nil
}

type AIUserSettings struct {
	Enabled       bool      `json:"enabled"`
	RetentionDays int       `json:"retention_days"`
	PurgeState    string    `json:"purge_state"`
	DisabledAt    time.Time `json:"disabled_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AISurfacePreference struct {
	SurfaceID     string          `json:"surface_id"`
	PinnedAgentID string          `json:"pinned_agent_id,omitempty"`
	Proactive     bool            `json:"proactive_enabled"`
	SavedActions  json.RawMessage `json:"saved_actions"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (db *Database) AISettings(ctx context.Context, userID string) (AIUserSettings, []AISurfacePreference, error) {
	settings := AIUserSettings{Enabled: true, RetentionDays: 30, PurgeState: "none"}
	preferences := []AISurfacePreference{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var disabledAt sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT enabled,retention_days,purge_state,disabled_at,updated_at FROM ai_user_settings WHERE user_id=$1
		`, userID).Scan(&settings.Enabled, &settings.RetentionDays, &settings.PurgeState, &disabledAt, &settings.UpdatedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if disabledAt.Valid {
			settings.DisabledAt = disabledAt.Time
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT surface_id,COALESCE(pinned_agent_id,''),proactive_enabled,saved_actions,updated_at
			FROM ai_surface_preferences WHERE user_id=$1 ORDER BY surface_id
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AISurfacePreference
			if err := rows.Scan(&item.SurfaceID, &item.PinnedAgentID, &item.Proactive, &item.SavedActions, &item.UpdatedAt); err != nil {
				return err
			}
			preferences = append(preferences, item)
		}
		return rows.Err()
	})
	return settings, preferences, err
}

func (db *Database) UpdateAISettings(ctx context.Context, userID string, enabled bool, retentionDays int) (AIUserSettings, error) {
	if retentionDays < 1 || retentionDays > 365 {
		return AIUserSettings{}, ErrSpaceInvalid
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		purgeState := "none"
		if !enabled {
			purgeState = "queued"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_user_settings(user_id,enabled,retention_days,purge_state,disabled_at)
			VALUES($1,$2,$3,$4,CASE WHEN $2 THEN NULL ELSE NOW() END)
			ON CONFLICT(user_id) DO UPDATE SET enabled=EXCLUDED.enabled,retention_days=EXCLUDED.retention_days,
				purge_state=EXCLUDED.purge_state,disabled_at=EXCLUDED.disabled_at,updated_at=NOW()
		`, userID, enabled, retentionDays, purgeState); err != nil {
			return err
		}
		if enabled {
			_, err := tx.ExecContext(ctx, `DELETE FROM ai_cleanup_jobs WHERE user_id=$1 AND state IN ('queued','failed','verified')`, userID)
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',canceled_at=NOW(),updated_at=NOW() WHERE user_id=$1 AND state IN ('queued','running','awaiting_approval')`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_retrieval_documents WHERE owner_user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_invocations i WHERE i.user_id=$1 AND NOT EXISTS(SELECT 1 FROM ai_artifacts a WHERE a.invocation_id=i.id AND a.state='applied')`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET request_payload='{}'::jsonb,updated_at=NOW() WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_artifacts SET sources='[]'::jsonb,operations='{}'::jsonb,updated_at=NOW() WHERE user_id=$1 AND state='applied'`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_feedback WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_surface_preferences WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_recaps WHERE user_id=$1`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_cleanup_jobs(id,user_id) VALUES($1,$2)`, "aicleanup_"+uuid.NewString(), userID)
		return err
	})
	if err != nil {
		return AIUserSettings{}, err
	}
	settings, _, err := db.AISettings(ctx, userID)
	return settings, err
}

// ProcessAICleanupJobs verifies the high-priority second phase of an AI
// disable request. The initial request transaction already blocks work and
// removes database-owned personal AI data; this worker repeats those deletions
// idempotently and only marks the purge verified after proving no scoped rows
// remain. There are currently no AI-owned object-storage blobs, so database
// verification is the complete cleanup boundary. A future cache/object store
// must add its proof here before the verified transition.
func (db *Database) ProcessAICleanupJobs(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	type job struct {
		ID       string
		UserID   string
		Attempts int
	}
	jobs := []job{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id,user_id,attempts FROM ai_cleanup_jobs
			WHERE state IN ('queued','failed') AND available_at<=NOW() AND attempts<20
			ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT $1
		`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item job
			if err := rows.Scan(&item.ID, &item.UserID, &item.Attempts); err != nil {
				return err
			}
			jobs = append(jobs, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, item := range jobs {
			if _, err := tx.ExecContext(ctx, `UPDATE ai_cleanup_jobs SET state='working',attempts=attempts+1,error_code='',updated_at=NOW() WHERE id=$1`, item.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE ai_user_settings SET purge_state='working',updated_at=NOW() WHERE user_id=$1 AND enabled=FALSE`, item.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, item := range jobs {
		err := db.verifyAICleanupJob(ctx, item.ID, item.UserID)
		if err == nil {
			completed++
			continue
		}
		_ = db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
			_, updateErr := tx.ExecContext(ctx, `
				UPDATE ai_cleanup_jobs SET state='failed',error_code='verification_failed',
					available_at=NOW()+LEAST(attempts,20)*INTERVAL '30 seconds',updated_at=NOW()
				WHERE id=$1
			`, item.ID)
			if updateErr != nil {
				return updateErr
			}
			_, updateErr = tx.ExecContext(ctx, `UPDATE ai_user_settings SET purge_state='failed',updated_at=NOW() WHERE user_id=$1 AND enabled=FALSE`, item.UserID)
			return updateErr
		})
	}
	return completed, nil
}

func (db *Database) verifyAICleanupJob(ctx context.Context, jobID, userID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM ai_user_settings WHERE user_id=$1 FOR UPDATE`, userID).Scan(&enabled); err != nil {
			return err
		}
		if enabled {
			_, err := tx.ExecContext(ctx, `DELETE FROM ai_cleanup_jobs WHERE id=$1`, jobID)
			return err
		}
		statements := []string{
			`DELETE FROM ai_retrieval_documents WHERE owner_user_id=$1`,
			`DELETE FROM ai_feedback WHERE user_id=$1`,
			`DELETE FROM ai_surface_preferences WHERE user_id=$1`,
			`DELETE FROM ai_recaps WHERE user_id=$1`,
			`DELETE FROM ai_invocations i WHERE i.user_id=$1 AND NOT EXISTS(SELECT 1 FROM ai_artifacts a WHERE a.invocation_id=i.id AND a.state='applied')`,
			`UPDATE ai_invocations SET request_payload='{}'::jsonb,updated_at=NOW() WHERE user_id=$1`,
			`UPDATE ai_artifacts SET sources='[]'::jsonb,operations='{}'::jsonb,updated_at=NOW() WHERE user_id=$1 AND state='applied'`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
				return err
			}
		}
		var remaining int
		if err := tx.QueryRowContext(ctx, `
			SELECT
			  (SELECT COUNT(*) FROM ai_retrieval_documents WHERE owner_user_id=$1)+
			  (SELECT COUNT(*) FROM ai_feedback WHERE user_id=$1)+
			  (SELECT COUNT(*) FROM ai_surface_preferences WHERE user_id=$1)+
			  (SELECT COUNT(*) FROM ai_recaps WHERE user_id=$1)+
			  (SELECT COUNT(*) FROM ai_invocations i WHERE i.user_id=$1 AND i.request_payload<>'{}'::jsonb)+
			  (SELECT COUNT(*) FROM ai_artifacts WHERE user_id=$1 AND state='applied' AND (sources<>'[]'::jsonb OR operations<>'{}'::jsonb))
		`, userID).Scan(&remaining); err != nil {
			return err
		}
		if remaining != 0 {
			return fmt.Errorf("AI cleanup verification found %d residual rows", remaining)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_cleanup_jobs SET state='verified',error_code='',updated_at=NOW() WHERE id=$1`, jobID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ai_user_settings SET purge_state='verified',updated_at=NOW() WHERE user_id=$1 AND enabled=FALSE`, userID)
		return err
	})
}

// PurgeExpiredAITransients enforces the 24-hour ceiling for unaccepted quick
// transforms while preserving invocations that own an applied artifact.
func (db *Database) PurgeExpiredAITransients(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	var purged int64
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM ai_invocations WHERE id IN (
				SELECT i.id FROM ai_invocations i
				WHERE i.expires_at<=NOW()
				  AND NOT EXISTS(SELECT 1 FROM ai_artifacts a WHERE a.invocation_id=i.id AND a.state='applied')
				ORDER BY i.expires_at LIMIT $1
			)
		`, limit)
		if err != nil {
			return err
		}
		purged, _ = result.RowsAffected()
		return nil
	})
	return purged, err
}

func (db *Database) UpsertAISurfacePreference(ctx context.Context, userID string, preference AISurfacePreference) (AISurfacePreference, error) {
	if strings.TrimSpace(preference.SurfaceID) == "" {
		return AISurfacePreference{}, ErrSpaceInvalid
	}
	if len(preference.SavedActions) == 0 {
		preference.SavedActions = json.RawMessage(`[]`)
	}
	out := AISurfacePreference{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if preference.PinnedAgentID != "" {
			var allowed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_agents WHERE id=$1 AND owner_user_id=$2 AND enabled AND deleted_at IS NULL)`, preference.PinnedAgentID, userID).Scan(&allowed); err != nil || !allowed {
				return ErrSpaceInvalid
			}
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO ai_surface_preferences(user_id,surface_id,pinned_agent_id,proactive_enabled,saved_actions)
			VALUES($1,$2,NULLIF($3,''),$4,$5)
			ON CONFLICT(user_id,surface_id) DO UPDATE SET pinned_agent_id=EXCLUDED.pinned_agent_id,
				proactive_enabled=EXCLUDED.proactive_enabled,saved_actions=EXCLUDED.saved_actions,updated_at=NOW()
			RETURNING surface_id,COALESCE(pinned_agent_id,''),proactive_enabled,saved_actions,updated_at
		`, userID, preference.SurfaceID, preference.PinnedAgentID, preference.Proactive, preference.SavedActions).Scan(&out.SurfaceID, &out.PinnedAgentID, &out.Proactive, &out.SavedActions, &out.UpdatedAt)
	})
	return out, err
}

func (db *Database) RecordAIFeedback(ctx context.Context, userID, invocationID string, rating int, reason, comment string) error {
	if rating != -1 && rating != 1 || len([]rune(comment)) > 2000 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ai_feedback(id,user_id,invocation_id,rating,reason_code,comment)
			SELECT $1,$2,$3,$4,$5,$6 FROM ai_invocations WHERE id=$3 AND user_id=$2
			ON CONFLICT(user_id,invocation_id) DO UPDATE SET rating=EXCLUDED.rating,reason_code=EXCLUDED.reason_code,comment=EXCLUDED.comment,created_at=NOW()
		`, "aifeedback_"+uuid.NewString(), userID, invocationID, rating, strings.TrimSpace(reason), strings.TrimSpace(comment))
		return err
	})
}

package db

import (
	"context"
	"database/sql"
	"fmt"
)

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
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
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
		_ = db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
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
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
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
			`DELETE FROM misty_memories WHERE user_id=$1`,
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
			  (SELECT COUNT(*) FROM misty_memories WHERE user_id=$1)+
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
// transforms while preserving applied artifacts and retained conversation history.
// A chat a device keeps is not retained here: its device holds the transcript.
// Retaining history never extends invocation/device authority, which still expires.
func (db *Database) PurgeExpiredAITransients(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	var purged int64
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			DELETE FROM ai_invocations WHERE id IN (
				SELECT i.id FROM ai_invocations i
				WHERE i.expires_at<=NOW()
				  AND NOT (i.mode IN ('drawer','companion') AND EXISTS(
				    SELECT 1 FROM misty_ask_conversations c
				    LEFT JOIN ai_user_settings prefs ON prefs.user_id=c.user_id
				    WHERE c.id=i.conversation_id AND c.user_id=i.user_id
				      AND c.deleted_at IS NULL AND c.retention_expires_at>NOW()
				      AND c.kept_on_device_id IS NULL
				      AND COALESCE(prefs.enabled,true)
				      AND i.created_at+COALESCE(prefs.retention_days,30)*INTERVAL '1 day'>NOW()
				  ))
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

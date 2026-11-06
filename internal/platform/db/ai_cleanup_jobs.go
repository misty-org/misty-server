package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
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
// A conversation's turns stay for the person's retention setting; a chat uploaded
// from a computer's old local store counts from the upload.
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
				      AND c.deleted_at IS NULL
				      AND COALESCE(prefs.enabled,true)
				      AND GREATEST(i.created_at,c.imported_at)+COALESCE(prefs.retention_days,30)*INTERVAL '1 day'>NOW()
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

// PurgeExpiredAgentConversations applies each person's conversation retention:
// a conversation with no activity for that many days goes with everything stored
// for it, and older tool output and screenshots go from conversations still in
// use. It returns how many rows went and the object keys now free to delete.
// A conversation with a run still going waits for it.
func (db *Database) PurgeExpiredAgentConversations(ctx context.Context, limit int) (int64, []string, error) {
	if limit < 1 || limit > 1000 {
		limit = 250
	}
	var purged int64
	keys := []string{}
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT c.id,c.user_id FROM misty_ask_conversations c
			LEFT JOIN ai_user_settings prefs ON prefs.user_id=c.user_id
			WHERE GREATEST(c.updated_at,c.imported_at)+COALESCE(prefs.retention_days,30)*INTERVAL '1 day'<=NOW()
			  AND NOT EXISTS(SELECT 1 FROM ai_invocations i WHERE i.user_id=c.user_id AND i.conversation_id=c.id AND i.state NOT IN ('completed','failed','canceled'))
			ORDER BY c.updated_at LIMIT $1 FOR UPDATE OF c SKIP LOCKED
		`, limit)
		if err != nil {
			return err
		}
		byOwner := map[string][]string{}
		for rows.Next() {
			var id, owner string
			if err := rows.Scan(&id, &owner); err != nil {
				rows.Close()
				return err
			}
			byOwner[owner] = append(byOwner[owner], id)
			purged++
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for owner, ids := range byOwner {
			found, err := attachmentKeysTx(ctx, tx, `SELECT object_key,model_object_key FROM ai_conversation_attachments WHERE user_id=$1 AND conversation_id=ANY($2)`, owner, pq.Array(ids))
			if err != nil {
				return err
			}
			keys = append(keys, found...)
			if err := deleteAgentConversationsTx(ctx, tx, owner, ids); err != nil {
				return err
			}
		}
		// What's aged out of a conversation still in use.
		found, err := attachmentKeysTx(ctx, tx, `DELETE FROM ai_conversation_attachments a USING ai_user_settings prefs
			WHERE a.scope='tool_output' AND prefs.user_id=a.user_id AND a.created_at+prefs.retention_days*INTERVAL '1 day'<=NOW()
			RETURNING a.object_key,a.model_object_key`)
		if err != nil {
			return err
		}
		keys = append(keys, found...)
		result, err := tx.ExecContext(ctx, `DELETE FROM misty_ask_conversation_events e USING ai_user_settings prefs
			WHERE prefs.user_id=e.user_id AND e.created_at+prefs.retention_days*INTERVAL '1 day'<=NOW()`)
		if err != nil {
			return err
		}
		aged, _ := result.RowsAffected()
		purged += aged
		return nil
	})
	return purged, keys, err
}

// attachmentKeysTx reads original and model object keys, once each: a screenshot
// keeps one object under both.
func attachmentKeysTx(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var original, model string
		if err := rows.Scan(&original, &model); err != nil {
			return nil, err
		}
		keys = append(keys, original)
		if model != original {
			keys = append(keys, model)
		}
	}
	return keys, rows.Err()
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AppDataDeletionJob struct {
	UserID    string    `json:"user_id"`
	AppID     string    `json:"app_id"`
	DeleteAt  time.Time `json:"delete_at"`
	Attempts  int       `json:"attempts"`
	StartedAt time.Time `json:"started_at"`
}

// ClaimDueAppDataDeletionJobs crosses the irreversible boundary. Reinstall is
// allowed throughout the recovery window, but once a job is claimed the
// installation is marked purging and cannot be restored with partial data.
func (db *Database) ClaimDueAppDataDeletionJobs(ctx context.Context, limit int) ([]AppDataDeletionJob, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	items := []AppDataDeletionJob{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `WITH due AS (
			SELECT j.user_id,j.app_id FROM app_data_deletion_jobs j
			JOIN user_app_installations i ON i.user_id=j.user_id AND i.app_id=j.app_id
			WHERE j.delete_at<=NOW() AND (
				(j.state IN ('pending','failed') AND i.state='recoverable') OR
				(j.state='running' AND i.state='purging' AND j.started_at<=NOW()-INTERVAL '15 minutes')
			)
			ORDER BY j.delete_at,j.user_id,j.app_id FOR UPDATE OF j SKIP LOCKED LIMIT $1
		), claimed AS (
			UPDATE app_data_deletion_jobs j SET state='running',attempts=j.attempts+1,last_error='',started_at=NOW(),updated_at=NOW()
			FROM due WHERE j.user_id=due.user_id AND j.app_id=due.app_id
			RETURNING j.user_id,j.app_id,j.delete_at,j.attempts,j.started_at
		)
		SELECT user_id,app_id,delete_at,attempts,started_at FROM claimed`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AppDataDeletionJob
			if err := rows.Scan(&item.UserID, &item.AppID, &item.DeleteAt, &item.Attempts, &item.StartedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, item := range items {
			if _, err := tx.ExecContext(ctx, `UPDATE user_app_installations SET state='purging',pinned=FALSE,updated_at=NOW()
				WHERE user_id=$1 AND app_id=$2 AND state IN ('recoverable','purging')`, item.UserID, item.AppID); err != nil {
				return err
			}
			if err := recordAppInstallEventTx(ctx, tx, item.UserID, item.AppID, "purge_started", map[string]any{"attempt": item.Attempts}); err != nil {
				return err
			}
		}
		return nil
	})
	return items, err
}

// CompleteAppDataDeletion removes only account-private, app-namespaced data.
// Space-owned notes, messages, files, tasks, and other collaborative records
// deliberately remain intact when one member uninstalls an app.
func (db *Database) CompleteAppDataDeletion(ctx context.Context, job AppDataDeletionJob, now time.Time) error {
	job.UserID, job.AppID = strings.TrimSpace(job.UserID), strings.TrimSpace(job.AppID)
	if job.UserID == "" || job.AppID == "" {
		return ErrAppRecordInvalid
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM app_data_deletion_jobs
			WHERE user_id=$1 AND app_id=$2 AND started_at=$3 FOR UPDATE`, job.UserID, job.AppID, job.StartedAt).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAppRuntimeForbidden
			}
			return err
		}
		if state != "running" {
			return ErrAppRuntimeForbidden
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_personal_records WHERE user_id=$1 AND app_id=$2`, job.UserID, job.AppID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_runtime_sessions WHERE user_id=$1 AND app_id=$2`, job.UserID, job.AppID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_app_activity WHERE user_id=$1 AND app_id=$2`, job.UserID, job.AppID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE user_app_installations SET state='purged',pinned=FALSE,purged_at=$3,updated_at=$3
			WHERE user_id=$1 AND app_id=$2 AND state='purging'`, job.UserID, job.AppID, now.UTC())
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrAppRuntimeForbidden
		}
		if _, err := tx.ExecContext(ctx, `UPDATE app_data_deletion_jobs SET state='completed',completed_at=$3,updated_at=$3,last_error=''
			WHERE user_id=$1 AND app_id=$2`, job.UserID, job.AppID, now.UTC()); err != nil {
			return err
		}
		return recordAppInstallEventTx(ctx, tx, job.UserID, job.AppID, "purged", map[string]any{"attempt": job.Attempts})
	})
}

func (db *Database) FailAppDataDeletion(ctx context.Context, job AppDataDeletionJob, cause error) error {
	message := "app data purge failed"
	if cause != nil {
		message = strings.TrimSpace(cause.Error())
	}
	if len(message) > 500 {
		message = message[:500]
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE app_data_deletion_jobs SET state='failed',last_error=$3,updated_at=NOW()
			WHERE user_id=$1 AND app_id=$2 AND state='running' AND started_at=$4`, job.UserID, job.AppID, message, job.StartedAt)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated == 0 {
			// A newer worker reclaimed this stale lease. Its purge owns the
			// installation state and must not be undone by this worker.
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE user_app_installations SET state='recoverable',updated_at=NOW()
			WHERE user_id=$1 AND app_id=$2 AND state='purging'`, job.UserID, job.AppID); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]any{"attempt": job.Attempts, "error": message})
		_, err = tx.ExecContext(ctx, `INSERT INTO app_install_events(user_id,app_id,event_type,metadata)
			VALUES($1,$2,'purge_failed',$3::jsonb)`, job.UserID, job.AppID, metadata)
		return err
	})
}

func (db *Database) AppDataDeletionJob(ctx context.Context, userID, appID string) (*AppDataDeletionJob, error) {
	var item AppDataDeletionJob
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT user_id,app_id,delete_at,attempts,COALESCE(started_at,created_at)
			FROM app_data_deletion_jobs WHERE user_id=$1 AND app_id=$2`, userID, appID).
			Scan(&item.UserID, &item.AppID, &item.DeleteAt, &item.Attempts, &item.StartedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &item, err
}

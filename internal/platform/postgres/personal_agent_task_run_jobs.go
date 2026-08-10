package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type PersonalAgentTaskRunJob struct {
	Run          SpaceRun
	Task         SpaceTask
	Attempt      int
	LeaseOwner   string
	LeaseExpires time.Time
}

func (db *Database) ValidatePersonalAgentTaskRun(ctx context.Context, userID, runID, taskID, agentID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT r.space_id,EXISTS(
			SELECT 1 FROM personal_agent_task_run_jobs j JOIN space_tasks t ON t.id=j.task_id
			WHERE j.run_id=r.id AND j.state='leased' AND t.id=$2 AND t.assignee_agent_id=$3 AND t.archived_at IS NULL
		) FROM space_runs r WHERE r.id=$1 AND r.state='running' AND r.requesting_member_id=$4`, runID, taskID, agentID, userID).Scan(&spaceID, &active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceForbidden
			}
			return err
		}
		if !active {
			return ErrSpaceForbidden
		}
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		if !agentMembershipPermission(membership.Permissions, PermissionTasksView) ||
			!agentMembershipPermission(membership.Permissions, PermissionTasksManage) {
			return ErrSpaceForbidden
		}
		return nil
	})
}

func (db *Database) ClaimPersonalAgentTaskRunJobs(ctx context.Context, workerID string, limit int, lease time.Duration) ([]PersonalAgentTaskRunJob, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, ErrSpaceInvalid
	}
	if limit < 1 || limit > 20 {
		limit = 2
	}
	if lease < 30*time.Second || lease > 10*time.Minute {
		lease = 90 * time.Second
	}
	jobs := []PersonalAgentTaskRunJob{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE personal_agent_task_run_jobs j SET state=CASE
			WHEN r.state IN ('completed','completed_with_errors') THEN 'completed'
			WHEN r.state='failed' THEN 'failed' ELSE 'canceled' END,
			lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW() FROM space_runs r
			WHERE j.run_id=r.id AND j.state IN ('queued','leased') AND r.state IN ('completed','completed_with_errors','failed','canceled','rejected')`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT j.run_id FROM personal_agent_task_run_jobs j
			JOIN space_runs r ON r.id=j.run_id JOIN space_tasks t ON t.id=j.task_id
			WHERE ((j.state='queued' AND j.available_at<=NOW()) OR (j.state='leased' AND j.lease_expires_at<=NOW()))
			  AND r.state IN ('queued','running') AND t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL
			ORDER BY j.available_at,j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT $1`, limit)
		if err != nil {
			return err
		}
		runIDs := []string{}
		for rows.Next() {
			var runID string
			if err := rows.Scan(&runID); err != nil {
				rows.Close()
				return err
			}
			runIDs = append(runIDs, runID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, runID := range runIDs {
			var job PersonalAgentTaskRunJob
			job.LeaseOwner = workerID
			job.LeaseExpires = time.Now().UTC().Add(lease)
			if err := tx.QueryRowContext(ctx, `UPDATE personal_agent_task_run_jobs SET state='leased',attempt=attempt+1,
				lease_owner=$1,lease_expires_at=$2,updated_at=NOW() WHERE run_id=$3
				RETURNING attempt`, workerID, job.LeaseExpires, runID).Scan(&job.Attempt); err != nil {
				return err
			}
			if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='running',attempt=$1,
				next_retry_at=NULL,updated_at=NOW() WHERE id=$2 AND state IN ('queued','running') RETURNING `+spaceRunColumns,
				job.Attempt, runID), &job.Run); err != nil {
				return err
			}
			if err := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE id=$1`, job.Run.SourceTaskID), &job.Task); err != nil {
				return err
			}
			if _, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: job.Task.SpaceID, TaskID: job.Task.ID,
				ActorKind: "agent", ActorAgentID: job.Run.AgentID, RunID: job.Run.ID, Kind: "progress",
				Message: "Started working on this task", Metadata: mustJSON(map[string]any{"attempt": job.Attempt})}); err != nil {
				return err
			}
			if _, err := recordSpaceEventTx(ctx, tx, job.Run.SpaceID, job.Run.InitiatedByUserID, "agent.run.started", job.Run.ID,
				map[string]any{"agent_id": job.Run.AgentID, "source_type": "task", "task_id": job.Task.ID, "attempt": job.Attempt}); err != nil {
				return err
			}
			jobs = append(jobs, job)
		}
		return nil
	})
	return jobs, err
}

func (db *Database) RenewPersonalAgentTaskRunLease(ctx context.Context, runID, workerID string, lease time.Duration) (bool, error) {
	if lease < 30*time.Second || lease > 10*time.Minute {
		lease = 90 * time.Second
	}
	active := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE personal_agent_task_run_jobs j SET lease_expires_at=$1,updated_at=NOW()
			FROM space_runs r,space_tasks t,personal_agent_space_grants g,personal_agents a
			WHERE j.run_id=$2 AND j.lease_owner=$3 AND j.state='leased' AND r.id=j.run_id AND r.state='running'
			  AND t.id=j.task_id AND t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL
			  AND g.space_id=j.space_id AND g.agent_id=j.agent_id AND g.enabled AND g.removed_at IS NULL
			  AND a.id=j.agent_id AND a.enabled AND a.deleted_at IS NULL`, time.Now().UTC().Add(lease), runID, workerID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		active = err == nil && changed == 1
		return err
	})
	return active, err
}

func (db *Database) CompletePersonalAgentTaskRunJob(ctx context.Context, runID, workerID string) error {
	return db.finishPersonalAgentTaskRunJob(ctx, runID, workerID, "completed", "", "")
}

func (db *Database) FailPersonalAgentTaskRunJob(ctx context.Context, runID, workerID, code, message string, retry bool) (bool, error) {
	requeued := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if retry {
			result, err := tx.ExecContext(ctx, `UPDATE personal_agent_task_run_jobs SET state='queued',available_at=NOW()+(attempt*INTERVAL '5 seconds'),
				lease_owner=NULL,lease_expires_at=NULL,last_error_code=$1,last_error_message=$2,updated_at=NOW()
				WHERE run_id=$3 AND lease_owner=$4 AND state='leased' AND attempt<3
				  AND EXISTS(SELECT 1 FROM space_runs r WHERE r.id=$3 AND r.state='running')`, code, message, runID, workerID)
			if err != nil {
				return err
			}
			changed, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if changed == 1 {
				requeued = true
				_, err = tx.ExecContext(ctx, `UPDATE space_runs SET state='queued',next_retry_at=NOW()+(attempt*INTERVAL '5 seconds'),
					error_code=$1,error_message=$2,updated_at=NOW() WHERE id=$3 AND state='running'`, code, message, runID)
				return err
			}
		}
		canceled, err := tx.ExecContext(ctx, `UPDATE personal_agent_task_run_jobs j SET state='canceled',lease_owner=NULL,
			lease_expires_at=NULL,last_error_code=$1,last_error_message=$2,completed_at=NOW(),updated_at=NOW()
			FROM space_runs r WHERE j.run_id=$3 AND j.lease_owner=$4 AND j.state='leased' AND r.id=j.run_id AND r.state='canceled'`, code, message, runID, workerID)
		if err != nil {
			return err
		}
		if changed, err := canceled.RowsAffected(); err != nil || changed == 1 {
			return err
		}
		return finishPersonalAgentTaskRunJobTx(ctx, tx, runID, workerID, "failed", code, message)
	})
	return requeued, err
}

func (db *Database) finishPersonalAgentTaskRunJob(ctx context.Context, runID, workerID, state, code, message string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return finishPersonalAgentTaskRunJobTx(ctx, tx, runID, workerID, state, code, message)
	})
}

func finishPersonalAgentTaskRunJobTx(ctx context.Context, tx *sql.Tx, runID, workerID, state, code, message string) error {
	result, err := tx.ExecContext(ctx, `UPDATE personal_agent_task_run_jobs SET state=$1,lease_owner=NULL,lease_expires_at=NULL,
		last_error_code=$2,last_error_message=$3,completed_at=NOW(),updated_at=NOW()
		WHERE run_id=$4 AND lease_owner=$5 AND state='leased'`, state, code, message, runID, workerID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM personal_agent_task_run_jobs WHERE run_id=$1`, runID).Scan(&current); err == nil && current == state {
			return nil
		}
		return ErrSpaceConflict
	}
	return nil
}

func (db *Database) PersonalAgentTaskRunJobState(ctx context.Context, runID string) (string, int, error) {
	var state string
	var attempt int
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT state,attempt FROM personal_agent_task_run_jobs WHERE run_id=$1`, runID).Scan(&state, &attempt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSpaceNotFound
	}
	return state, attempt, err
}

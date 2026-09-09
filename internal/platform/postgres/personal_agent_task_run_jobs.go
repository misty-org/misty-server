package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

type PersonalAgentTaskRunJob struct {
	Run          SpaceRun
	Task         SpaceTask
	HasTask      bool
	Attempt      int
	LeaseOwner   string
	LeaseExpires time.Time
}

func (db *Database) ValidatePersonalAgentTaskRun(ctx context.Context, userID, runID, taskID, agentID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT r.space_id,EXISTS(
			SELECT 1 FROM agent_run_jobs j JOIN space_tasks t ON t.id=j.task_id
			WHERE j.run_id=r.id AND j.state IN ('leased','dispatched') AND t.id=$2 AND t.assignee_agent_id=$3 AND t.archived_at IS NULL
		) FROM space_runs r WHERE r.id=$1 AND r.state='running' AND r.requesting_member_id=$4`, runID, taskID, agentID, userID).Scan(&spaceID, &active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceForbidden
			}
			return err
		}
		if !active {
			return ErrSpaceForbidden
		}
		_, err := askExecutionContextTx(ctx, tx, userID, spaceID, agentID)
		return err
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
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs j SET state=CASE
			WHEN r.state IN ('completed','completed_with_errors') THEN 'completed'
			WHEN r.state='failed' THEN 'failed' ELSE 'canceled' END,
			lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW() FROM space_runs r
			WHERE j.run_id=r.id AND r.execution_owner='go' AND j.state IN ('queued','leased','dispatched') AND r.state IN ('completed','completed_with_errors','failed','canceled','rejected')`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT j.run_id,j.agent_id FROM agent_run_jobs j
			JOIN space_runs r ON r.id=j.run_id LEFT JOIN space_tasks t ON t.id=j.task_id
			WHERE ((j.state='queued' AND j.available_at<=NOW()) OR (j.state='leased' AND j.lease_expires_at<=NOW()))
			  AND r.execution_owner='go' AND r.state IN ('queued','running') AND (j.task_id IS NULL OR (t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL))
			  AND NOT EXISTS(SELECT 1 FROM agent_run_jobs active
			    WHERE active.agent_id=j.agent_id AND active.run_id<>j.run_id AND active.state='dispatched')
			ORDER BY j.available_at,j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT $1`, limit*5)
		if err != nil {
			return err
		}
		type candidate struct{ runID, agentID string }
		candidates := []candidate{}
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.runID, &item.agentID); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		seenAgents := map[string]bool{}
		for _, candidate := range candidates {
			if len(jobs) >= limit || seenAgents[candidate.agentID] {
				continue
			}
			var locked bool
			if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, "personal-agent-task:"+candidate.agentID).Scan(&locked); err != nil {
				return err
			}
			if !locked {
				continue
			}
			var active bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_run_jobs WHERE agent_id=$1 AND run_id<>$2 AND state IN ('leased','dispatched'))`, candidate.agentID, candidate.runID).Scan(&active); err != nil {
				return err
			}
			if active {
				continue
			}
			runID := candidate.runID
			var job PersonalAgentTaskRunJob
			job.LeaseOwner = workerID
			job.LeaseExpires = time.Now().UTC().Add(lease)
			if err := tx.QueryRowContext(ctx, `UPDATE agent_run_jobs SET state='leased',attempt=attempt+1,
				lease_owner=$1,lease_expires_at=$2,updated_at=NOW() WHERE run_id=$3
				RETURNING attempt`, workerID, job.LeaseExpires, runID).Scan(&job.Attempt); err != nil {
				return err
			}
			if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='running',attempt=$1,
				next_retry_at=NULL,updated_at=NOW() WHERE id=$2 AND state IN ('queued','running') RETURNING `+spaceRunColumns,
				job.Attempt, runID), &job.Run); err != nil {
				return err
			}
			if job.Run.SourceTaskID != "" {
				if err := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE id=$1`, job.Run.SourceTaskID), &job.Task); err != nil {
					return err
				}
				job.HasTask = true
				if _, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: job.Task.SpaceID, TaskID: job.Task.ID,
					ActorKind: "agent", ActorAgentID: job.Run.AgentID, RunID: job.Run.ID, Kind: "progress",
					Message: "Started working on this task", Metadata: mustJSON(map[string]any{"attempt": job.Attempt})}); err != nil {
					return err
				}
			}
			if _, err := recordSpaceEventTx(ctx, tx, job.Run.SpaceID, job.Run.InitiatedByUserID, "agent.run.started", job.Run.ID,
				map[string]any{"agent_id": job.Run.AgentID, "source_type": job.Run.SourceType, "task_id": job.Run.SourceTaskID, "attempt": job.Attempt}); err != nil {
				return err
			}
			jobs = append(jobs, job)
			seenAgents[candidate.agentID] = true
		}
		return nil
	})
	return jobs, err
}

// ActivatePersonalAgentTaskRuntime binds the first workflow that reaches the
// control plane to the Misty run. Later duplicate workflow starts are rejected
// before they can execute a tool.
func (db *Database) ActivatePersonalAgentTaskRuntime(ctx context.Context, runID, runtimeKind, runtimeRunID string) (*SpaceRun, error) {
	if strings.TrimSpace(runtimeKind) == "" || strings.TrimSpace(runtimeRunID) == "" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var jobState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM agent_run_jobs WHERE run_id=$1 FOR UPDATE`, runID).Scan(&jobState); err != nil {
			return err
		}
		if jobState != "leased" && jobState != "dispatched" {
			return ErrSpaceConflict
		}
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT runtime_run_id FROM space_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&current); err != nil {
			return err
		}
		if current != "" && current != runtimeRunID {
			return ErrSpaceConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='dispatched',lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE run_id=$1`, runID); err != nil {
			return err
		}
		return scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='running',runtime_kind=$2,runtime_run_id=$3,
			runtime_phase='starting',runtime_heartbeat_at=NOW(),next_retry_at=NULL,updated_at=NOW()
			WHERE id=$1 AND state IN ('queued','running') RETURNING `+spaceRunColumns, runID, runtimeKind, runtimeRunID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) MarkPersonalAgentTaskRunDispatched(ctx context.Context, runID, workerID, runtimeKind, runtimeRunID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var jobState, leaseOwner, currentRuntime string
		if err := tx.QueryRowContext(ctx, `SELECT j.state,COALESCE(j.lease_owner,''),r.runtime_run_id FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id WHERE j.run_id=$1 FOR UPDATE OF j,r`, runID).Scan(&jobState, &leaseOwner, &currentRuntime); err != nil {
			return err
		}
		if currentRuntime != "" && currentRuntime != runtimeRunID {
			return ErrSpaceConflict
		}
		if jobState == "leased" && leaseOwner != workerID || jobState != "leased" && jobState != "dispatched" {
			return ErrSpaceConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='dispatched',lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE run_id=$1`, runID); err != nil {
			return err
		}
		return scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='running',runtime_kind=$2,runtime_run_id=$3,
			runtime_phase=CASE WHEN runtime_phase='' THEN 'starting' ELSE runtime_phase END,runtime_heartbeat_at=NOW(),next_retry_at=NULL,updated_at=NOW()
			WHERE id=$1 AND state IN ('queued','running') RETURNING `+spaceRunColumns, runID, runtimeKind, runtimeRunID), out)
	})
	return out, err
}

func (db *Database) ValidatePersonalAgentTaskRuntime(ctx context.Context, runID, runtimeRunID string, allowIntervention ...bool) (*SpaceRun, *SpaceTask, error) {
	run := &SpaceRun{}
	task := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs r WHERE r.id=$1 AND r.runtime_run_id=$2 AND (r.state='running' OR ($3 AND r.state='awaiting_intervention'))`, runID, runtimeRunID, len(allowIntervention) > 0 && allowIntervention[0]), run); err != nil {
			return err
		}
		if run.SourceTaskID != "" {
			if err := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks t JOIN agent_run_jobs j ON j.task_id=t.id WHERE j.run_id=$1 AND j.state='dispatched' AND t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL`, runID), task); err != nil {
				return err
			}
		} else {
			var active bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_run_jobs WHERE run_id=$1 AND state='dispatched' AND task_id IS NULL)`, runID).Scan(&active); err != nil || !active {
				return ErrSpaceForbidden
			}
		}
		_, err := askExecutionContextTx(ctx, tx, run.OwnerUserID, run.SpaceID, run.AgentID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return run, task, err
}

func (db *Database) RenewPersonalAgentTaskRunLease(ctx context.Context, runID, workerID string, lease time.Duration) (bool, error) {
	if lease < 30*time.Second || lease > 10*time.Minute {
		lease = 90 * time.Second
	}
	active := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs j SET lease_expires_at=$1,updated_at=NOW()
			FROM space_runs r,space_tasks t,misty_ask_identities a
			WHERE j.run_id=$2 AND j.lease_owner=$3 AND j.state='leased' AND r.id=j.run_id AND r.state='running'
			  AND t.id=j.task_id AND t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL
			  AND a.id=j.agent_id AND a.owner_user_id=r.owner_user_id AND a.enabled AND a.deleted_at IS NULL
			  AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=j.space_id AND m.user_id=a.owner_user_id)`, time.Now().UTC().Add(lease), runID, workerID)
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
			result, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='queued',available_at=NOW()+(attempt*INTERVAL '5 seconds'),
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
		canceled, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs j SET state='canceled',lease_owner=NULL,
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
	result, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state=$1,lease_owner=NULL,lease_expires_at=NULL,
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
		if err := tx.QueryRowContext(ctx, `SELECT state FROM agent_run_jobs WHERE run_id=$1`, runID).Scan(&current); err == nil && current == state {
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
		return tx.QueryRowContext(ctx, `SELECT state,attempt FROM agent_run_jobs WHERE run_id=$1`, runID).Scan(&state, &attempt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, ErrSpaceNotFound
	}
	return state, attempt, err
}

// ReconcileStalePersonalAgentTaskRuns preserves the pinned runtime. A missed
// heartbeat is never permission to restart the prompt and repeat its effects.
func (db *Database) ReconcileStalePersonalAgentTaskRuns(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	count := 0
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.owner_user_id,r.runtime_run_id FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id
   WHERE j.state='dispatched' AND r.execution_owner='go' AND r.state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention') AND r.runtime_run_id<>''
    AND COALESCE(r.runtime_heartbeat_at,r.updated_at)<$1
   ORDER BY COALESCE(r.runtime_heartbeat_at,r.updated_at) FOR UPDATE OF j,r SKIP LOCKED LIMIT $2`, staleBefore, limit)
		if err != nil {
			return err
		}
		type item struct{ runID, userID, runtimeID string }
		items := []item{}
		for rows.Next() {
			var i item
			if err := rows.Scan(&i.runID, &i.userID, &i.runtimeID); err != nil {
				rows.Close()
				return err
			}
			items = append(items, i)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, i := range items {
			if err := queueAgentContinuationTx(ctx, tx, i.userID, i.runID, "runtime.reconcile", i.runtimeID+":"+uuid.NewString(), AgentContinuation{RuntimeID: i.runtimeID}); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET runtime_phase=CASE WHEN state='running' THEN 'recovering' ELSE runtime_phase END,runtime_heartbeat_at=NOW(),updated_at=NOW() WHERE id=$1`, i.runID); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (db *Database) RecordAgentRuntimeStatus(ctx context.Context, runID, runtimeID, status string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		switch status {
		case "pending", "running":
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET runtime_heartbeat_at=NOW(),runtime_phase=CASE WHEN runtime_phase='recovering' THEN 'working' ELSE runtime_phase END,updated_at=NOW()
    WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention')`, runID, runtimeID)
			return err
		case "completed", "failed", "cancelled", "missing":
			// The durable completion callback, not a model's final text or workflow exit,
			// is authoritative for successful completion. Preserve all existing outputs.
			result, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='failed',runtime_phase='needs_attention',
    error_code='runtime_reconciliation_required',error_message='The pinned runtime stopped without a confirmed completion. Review completed and uncertain actions before recovery.',
    completed_at=NOW(),updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention')`, runID, runtimeID)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil || n == 0 {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='failed',completed_at=NOW(),updated_at=NOW() WHERE run_id=$1 AND state='dispatched'`, runID); err != nil {
				return err
			}
			return releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID)
		default:
			return ErrSpaceInvalid
		}
	})
}

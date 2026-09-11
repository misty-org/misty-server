package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Only a v2 lease that never received a start acknowledgement can be delivered
// again. A potentially executed operation keeps its token and uncertain evidence.
func (db *Database) ReapWorkflowDeviceJobs(userID string) error {
	return db.agentTx(userID, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE workflow_device_node_jobs SET
   state=CASE
    WHEN state='leased' AND control_version=2 AND execution_started_at IS NULL AND cancel_requested_at IS NULL AND deadline_at>NOW() AND delivery_attempts<3 THEN 'queued'
    WHEN state='executing' OR state='leased' AND control_version=1 THEN 'uncertain'
    ELSE 'canceled' END,
   error_code=CASE WHEN state='executing' OR state='leased' AND control_version=1 THEN 'device_execution_uncertain' ELSE 'device_execution_stopped' END,
   completed_at=CASE WHEN state='leased' AND control_version=2 AND execution_started_at IS NULL AND cancel_requested_at IS NULL AND deadline_at>NOW() AND delivery_attempts<3 THEN NULL ELSE NOW() END
   WHERE user_id=$1 AND state IN ('queued','leased','executing') AND
    (deadline_at<=NOW() OR state IN ('leased','executing') AND lease_expires_at<=NOW() OR cancel_requested_at IS NOT NULL AND (state='queued' OR state='leased' AND control_version=2 AND execution_started_at IS NULL))`, userID)
		return err
	})
}

// HTTP callers must state their supported protocol. Direct callers default to
// v2; the v1 route only drains v1 jobs and cannot claim deadline-aware work.
func (db *Database) ClaimWorkflowDeviceNodeJob(userID, deviceID string, lease time.Duration, protocol ...int) (*WorkflowDeviceNodeJob, string, error) {
	version := 2
	if len(protocol) > 0 {
		version = protocol[0]
	}
	if version != 1 && version != 2 {
		return nil, "", ErrSpaceInvalid
	}
	if err := db.ReapWorkflowDeviceJobs(userID); err != nil {
		return nil, "", err
	}
	token, err := TestingSecureToken()
	if err != nil {
		return nil, "", err
	}
	var item *WorkflowDeviceNodeJob
	err = db.agentTx(userID, func(tx *sql.Tx) error {
		ctx := context.Background()
		rows, err := tx.Query(`SELECT id FROM workflow_device_node_jobs WHERE user_id=$1 AND assigned_device_id=$2 AND state='queued' AND control_version=$3 AND deadline_at>NOW() AND cancel_requested_at IS NULL ORDER BY created_at LIMIT 32`, userID, deviceID, version)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, id := range ids {
			candidate := &WorkflowDeviceNodeJob{}
			if err := scanWorkflowDeviceJob(tx.QueryRow(`SELECT `+workflowDeviceJobColumns+` FROM workflow_device_node_jobs WHERE id=$1 AND user_id=$2`, id, userID), candidate); err != nil {
				return err
			}
			_, _, authorityErr := deviceRunAuthorityTx(ctx, tx, userID, candidate.RunID, &candidate.RuntimeRunID, candidate.RequiredCapability)
			if authorityErr == nil {
				authorityErr = validateDeviceJobTargetTx(ctx, tx, candidate, deviceID)
			}
			if authorityErr != nil {
				if !deviceAuthorityDenied(authorityErr) {
					return authorityErr
				}
				if _, err := tx.Exec(`UPDATE workflow_device_node_jobs SET state='canceled',cancel_requested_at=NOW(),completed_at=NOW(),error_code='device_authority_changed' WHERE id=$1 AND state='queued'`, id); err != nil {
					return err
				}
				continue
			}
			claimed := &WorkflowDeviceNodeJob{}
			err = scanWorkflowDeviceJob(tx.QueryRow(`UPDATE workflow_device_node_jobs j SET state='leased',leased_device_id=$2,lease_token_hash=$3,lease_expires_at=LEAST(deadline_at,NOW()+INTERVAL '60 seconds'),last_heartbeat_at=NOW(),delivery_attempts=delivery_attempts+1,error_code=NULL
    WHERE j.id=(SELECT id FROM workflow_device_node_jobs WHERE id=$1 AND state='queued' AND deadline_at>NOW() AND cancel_requested_at IS NULL FOR UPDATE SKIP LOCKED) RETURNING `+workflowDeviceJobUpdateColumns, id, deviceID, TestingHashToken(token)), claimed)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := loadDeviceJobSpace(tx, claimed); err != nil {
				return err
			}

			item = claimed
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if item == nil {
		return nil, "", ErrAgentJobNotFound
	}
	return item, token, nil
}

func deviceAuthorityDenied(err error) bool {
	return errors.Is(err, ErrSpaceForbidden) || errors.Is(err, ErrAppRuntimeForbidden) || errors.Is(err, ErrAgentExecutionTimeLimit) || errors.Is(err, ErrSpaceNotFound)
}

func (db *Database) BeginWorkflowDeviceNodeJob(userID, deviceID, jobID, token string) (*WorkflowDeviceNodeJob, error) {
	return db.advanceWorkflowDeviceLease(userID, deviceID, jobID, token, true)
}

func (db *Database) RenewWorkflowDeviceNodeJob(userID, deviceID, jobID, token string) (*WorkflowDeviceNodeJob, error) {
	return db.advanceWorkflowDeviceLease(userID, deviceID, jobID, token, false)
}

func (db *Database) advanceWorkflowDeviceLease(userID, deviceID, jobID, token string, begin bool) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	var denied error
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		ctx := context.Background()
		if err := scanWorkflowDeviceJob(tx.QueryRow(`SELECT `+workflowDeviceJobColumns+` FROM workflow_device_node_jobs WHERE id=$1 AND user_id=$2 AND leased_device_id=$3 AND lease_token_hash=$4`, jobID, userID, deviceID, TestingHashToken(token)), item); err != nil {
			return err
		}
		if begin && item.ControlVersion != 2 {
			return ErrInvalidLease
		}
		_, _, authorityErr := deviceRunAuthorityTx(ctx, tx, userID, item.RunID, &item.RuntimeRunID, item.RequiredCapability)
		if authorityErr == nil {
			authorityErr = validateDeviceJobTargetTx(ctx, tx, item, deviceID)
		}
		if authorityErr != nil {
			if !deviceAuthorityDenied(authorityErr) {
				return authorityErr
			}
			denied = authorityErr
			_, err := tx.Exec(`UPDATE workflow_device_node_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()) WHERE id=$1 AND state IN ('leased','executing')`, jobID)
			return err
		}
		if err := scanWorkflowDeviceJob(tx.QueryRow(`UPDATE workflow_device_node_jobs SET
   state=CASE WHEN $5 THEN 'executing' ELSE state END,
   execution_started_at=CASE WHEN $5 THEN COALESCE(execution_started_at,NOW()) ELSE execution_started_at END,
   lease_expires_at=LEAST(deadline_at,NOW()+INTERVAL '60 seconds'),last_heartbeat_at=NOW()
   WHERE id=$1 AND user_id=$2 AND leased_device_id=$3 AND lease_token_hash=$4 AND state IN ('leased','executing') AND deadline_at>NOW() AND lease_expires_at>NOW() AND cancel_requested_at IS NULL
   RETURNING `+workflowDeviceJobColumns, jobID, userID, deviceID, TestingHashToken(token), begin), item); err != nil {
			return err
		}
		return loadDeviceJobSpace(tx, item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrInvalidLease
	}
	if err == nil && denied != nil {
		err = denied
	}
	return item, err
}

func (db *Database) FinishWorkflowDeviceNodeJob(userID, deviceID, jobID, token, state string, output json.RawMessage, errorCode string) (*WorkflowDeviceNodeJob, error) {
	if state != "completed" && state != "failed" && state != "uncertain" {
		return nil, ErrInvalidJobState
	}
	if len(output) == 0 {
		output = json.RawMessage(`null`)
	}
	item := &WorkflowDeviceNodeJob{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		if err := scanWorkflowDeviceJob(tx.QueryRow(`SELECT `+workflowDeviceJobColumns+` FROM workflow_device_node_jobs WHERE id=$1 AND user_id=$2 AND leased_device_id=$3 AND lease_token_hash=$4 FOR UPDATE`, jobID, userID, deviceID, TestingHashToken(token)), item); err != nil {
			return err
		}
		if item.State == "completed" || item.State == "failed" || item.State == "canceled" {
			var same bool
			if err := tx.QueryRow(`SELECT state=$2 AND COALESCE(output,'null'::jsonb)=$3::jsonb AND COALESCE(error_code,'')=$4 FROM workflow_device_node_jobs WHERE id=$1`, jobID, state, output, errorCode).Scan(&same); err != nil {
				return err
			}
			if !same {
				return ErrSpaceConflict
			}
			return nil
		}
		// No later lease replaces an executing/uncertain job. The original signed
		// device can report a late observed outcome, even after cancellation or expiry.
		if item.State != "leased" && item.State != "executing" && item.State != "uncertain" {
			return ErrInvalidLease
		}
		if item.ControlVersion == 2 && item.ExecutionStartedAt == nil && state != "failed" {
			return ErrInvalidLease
		}
		return scanWorkflowDeviceJob(tx.QueryRow(`UPDATE workflow_device_node_jobs SET state=$2,output=$3,error_code=NULLIF($4,''),completed_at=NOW(),lease_expires_at=NULL WHERE id=$1 RETURNING `+workflowDeviceJobColumns, jobID, state, output, errorCode), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrInvalidLease
	}
	return item, err
}

// StopWorkflowDeviceNodeJob prevents new starts and preserves possible effects.
// It deliberately uses an independent transaction when the caller's transport
// has already been canceled. Late device evidence can still reconcile uncertainty.
func (db *Database) StopWorkflowDeviceNodeJob(userID, jobID string) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		return scanWorkflowDeviceJob(tx.QueryRow(`UPDATE workflow_device_node_jobs SET
   state=CASE WHEN state='executing' OR state='leased' AND control_version=1 THEN 'uncertain'
    WHEN state IN ('queued','leased') THEN 'canceled' ELSE state END,
   cancel_requested_at=COALESCE(cancel_requested_at,NOW()),
   error_code=CASE WHEN state='executing' OR state='leased' AND control_version=1 THEN 'device_execution_uncertain'
    WHEN state IN ('queued','leased') THEN 'device_execution_stopped' ELSE error_code END,
   completed_at=COALESCE(completed_at,NOW())
   WHERE id=$1 AND user_id=$2 RETURNING `+workflowDeviceJobColumns, jobID, userID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrAgentJobNotFound
	}
	return item, err
}

// RearmUnstartedBrowserJob retries the same durable job only after proof that no
// native execution began. Lease tokens are discarded and every grant is checked.
func (db *Database) RearmUnstartedBrowserJob(ctx context.Context, userID string, job *WorkflowDeviceNodeJob) (*WorkflowDeviceNodeJob, error) {
	if job == nil || job.State != "canceled" || job.ExecutionStartedAt != nil || job.ControlVersion != 2 || !strings.HasPrefix(job.Operation, "browser.") {
		return nil, ErrSpaceConflict
	}
	result := &WorkflowDeviceNodeJob{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, budget, err := deviceRunAuthorityTx(ctx, tx, userID, job.RunID, &job.RuntimeRunID, job.RequiredCapability)
		if err != nil {
			return err
		}
		if err := validateDeviceJobTargetTx(ctx, tx, job, job.AssignedDeviceID); err != nil {
			return err
		}
		var expiry time.Time
		query := `SELECT expires_at FROM agent_run_contexts WHERE id=$1`
		if strings.HasPrefix(job.RunID, "invocation_") {
			query = `SELECT expires_at FROM ai_invocation_contexts WHERE id=$1`
		}
		if err := tx.QueryRowContext(ctx, query, job.ContextID).Scan(&expiry); err != nil {
			return err
		}
		return scanWorkflowDeviceJob(tx.QueryRowContext(ctx, `UPDATE workflow_device_node_jobs SET state='queued',deadline_at=$3,cancel_requested_at=NULL,completed_at=NULL,lease_token_hash=NULL,lease_expires_at=NULL,leased_device_id=NULL,last_heartbeat_at=NULL,error_code=NULL,recovery_attempts=recovery_attempts+1 WHERE id=$1 AND user_id=$2 AND state='canceled' AND control_version=2 AND execution_started_at IS NULL AND recovery_attempts<3 AND delivery_attempts<3 AND COALESCE(run_id,invocation_id)=$4 AND assigned_device_id=$5 AND COALESCE(context_id,ai_context_id)=$6 AND scope_id=$7 AND runtime_run_id=$8 AND required_capability=$9 AND operation=$10 RETURNING `+workflowDeviceJobColumns, job.ID, userID, deviceJobDeadline(ctx, expiry, budget), job.RunID, job.AssignedDeviceID, job.ContextID, job.ScopeID, job.RuntimeRunID, job.RequiredCapability, job.Operation), result)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceConflict
	}
	return result, err
}

// Hydrate delivery context from the authoritative run, never the client's active Space.
func loadDeviceJobSpace(tx *sql.Tx, job *WorkflowDeviceNodeJob) error {
	query := `SELECT COALESCE(space_id,'') FROM space_runs WHERE id=$1 AND owner_user_id=$2`
	if strings.HasPrefix(job.RunID, "invocation_") {
		query = `SELECT COALESCE(space_id,'') FROM ai_invocations WHERE id=$1 AND user_id=$2`
	}
	return tx.QueryRow(query, job.RunID, job.UserID).Scan(&job.SpaceID)
}

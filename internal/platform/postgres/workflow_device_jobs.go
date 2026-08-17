package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type WorkflowDeviceNodeJob struct {
	ID               string          `json:"id"`
	RunID            string          `json:"runId"`
	NodeID           string          `json:"nodeId"`
	UserID           string          `json:"-"`
	ScopeID          string          `json:"scopeId"`
	Operation        string          `json:"operation"`
	State            string          `json:"state"`
	Attempt          int             `json:"attempt"`
	Input            json.RawMessage `json:"input"`
	Config           json.RawMessage `json:"config"`
	InputSchema      json.RawMessage `json:"inputSchema"`
	OutputSchema     json.RawMessage `json:"outputSchema"`
	Output           json.RawMessage `json:"output,omitempty"`
	LeasedDeviceID   string          `json:"leasedDeviceId,omitempty"`
	DeviceGrantID    string          `json:"deviceGrantId,omitempty"`
	AssignedDeviceID string          `json:"assignedDeviceId,omitempty"`
	ErrorCode        string          `json:"errorCode,omitempty"`
	LeaseExpiresAt   *time.Time      `json:"leaseExpiresAt,omitempty"`
	LastHeartbeatAt  *time.Time      `json:"lastHeartbeatAt,omitempty"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
}

const workflowDeviceJobColumns = `id,run_id,node_id,attempt,user_id,scope_id,operation,input,config,input_schema,output_schema,state,COALESCE(leased_device_id,''),COALESCE(device_grant_id,''),COALESCE(assigned_device_id,''),lease_expires_at,last_heartbeat_at,output,COALESCE(error_code,''),created_at,completed_at`

func scanWorkflowDeviceJob(scanner interface{ Scan(...any) error }, item *WorkflowDeviceNodeJob) error {
	var output []byte
	if err := scanner.Scan(&item.ID, &item.RunID, &item.NodeID, &item.Attempt, &item.UserID, &item.ScopeID, &item.Operation, &item.Input, &item.Config, &item.InputSchema, &item.OutputSchema, &item.State, &item.LeasedDeviceID, &item.DeviceGrantID, &item.AssignedDeviceID, &item.LeaseExpiresAt, &item.LastHeartbeatAt, &output, &item.ErrorCode, &item.CreatedAt, &item.CompletedAt); err != nil {
		return err
	}
	item.Output = output
	return nil
}

func (db *Database) QueueWorkflowDeviceNodeJob(ctx context.Context, userID, runID, nodeID string, attempt int, scopeID, operation, capability string, input, config, inputSchema, outputSchema json.RawMessage) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		var grantID, deviceID string
		if err := tx.QueryRowContext(ctx, `SELECT g.id,g.device_id FROM space_runs r
			JOIN agent_device_grants g ON g.user_id=$1 AND g.space_id=r.space_id AND g.agent_id=r.agent_id
				AND g.scope_id=$3 AND g.revoked_at IS NULL AND g.expires_at>NOW() AND g.capabilities ? $4
			JOIN trusted_devices d ON d.id=g.device_id AND d.user_id=$1 AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds'
				AND (COALESCE(g.metadata->>'kind','')<>'browser_tab' OR g.metadata->>'sessionId'=d.capabilities->>'browser_session_id')
			WHERE r.id=$2 AND r.requesting_member_id=$1 ORDER BY g.updated_at DESC LIMIT 1`, userID, runID, scopeID, capability).Scan(&grantID, &deviceID); errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		} else if err != nil {
			return err
		}
		return scanWorkflowDeviceJob(tx.QueryRowContext(ctx, `INSERT INTO workflow_device_node_jobs(id,run_id,node_id,attempt,user_id,scope_id,operation,input,config,input_schema,output_schema,device_grant_id,assigned_device_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT(run_id,node_id,attempt) DO UPDATE SET run_id=EXCLUDED.run_id
			RETURNING `+workflowDeviceJobColumns, "devicejob_"+uuid.NewString(), runID, nodeID, attempt, userID, scopeID, operation, input, config, inputSchema, outputSchema, grantID, deviceID), item)
	})
	return item, err
}

func (db *Database) ClaimWorkflowDeviceNodeJob(userID, deviceID string, lease time.Duration) (*WorkflowDeviceNodeJob, string, error) {
	if lease != time.Minute {
		lease = time.Minute
	}
	token, err := TestingSecureToken()
	if err != nil {
		return nil, "", err
	}
	item := &WorkflowDeviceNodeJob{}
	err = db.agentTx(userID, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE workflow_device_node_jobs SET state='queued',leased_device_id=NULL,lease_token_hash=NULL,lease_expires_at=NULL,last_heartbeat_at=NULL WHERE user_id=$1 AND state='leased' AND lease_expires_at<=NOW()`, userID); err != nil {
			return err
		}
		return scanWorkflowDeviceJob(tx.QueryRow(`WITH candidate AS (
			SELECT j.id FROM workflow_device_node_jobs j JOIN trusted_devices d ON d.id=$1 AND d.user_id=$2 AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds'
			JOIN agent_device_grants g ON g.id=j.device_grant_id AND g.device_id=$1 AND g.user_id=$2 AND g.revoked_at IS NULL AND g.expires_at>NOW()
				AND (COALESCE(g.metadata->>'kind','')<>'browser_tab' OR g.metadata->>'sessionId'=d.capabilities->>'browser_session_id')
			WHERE j.user_id=$2 AND j.assigned_device_id=$1 AND j.state='queued' ORDER BY j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1)
			UPDATE workflow_device_node_jobs j SET state='leased',leased_device_id=$1,lease_token_hash=$3,lease_expires_at=NOW()+INTERVAL '60 seconds',last_heartbeat_at=NOW()
			FROM candidate c WHERE j.id=c.id RETURNING `+workflowDeviceJobColumns, deviceID, userID, TestingHashToken(token)), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrAgentJobNotFound
	}
	return item, token, err
}

func (db *Database) RenewWorkflowDeviceNodeJob(userID, deviceID, jobID, token string) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		return scanWorkflowDeviceJob(tx.QueryRow(`UPDATE workflow_device_node_jobs SET lease_expires_at=NOW()+INTERVAL '60 seconds',last_heartbeat_at=NOW()
			WHERE id=$1 AND user_id=$2 AND leased_device_id=$3 AND lease_token_hash=$4 AND state='leased' AND lease_expires_at>NOW() RETURNING `+workflowDeviceJobColumns, jobID, userID, deviceID, TestingHashToken(token)), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrInvalidLease
	}
	return item, err
}

func (db *Database) FinishWorkflowDeviceNodeJob(userID, deviceID, jobID, token, state string, output json.RawMessage, errorCode string) (*WorkflowDeviceNodeJob, error) {
	if state != "completed" && state != "failed" {
		return nil, ErrInvalidJobState
	}
	item := &WorkflowDeviceNodeJob{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		return scanWorkflowDeviceJob(tx.QueryRow(`UPDATE workflow_device_node_jobs SET state=$1,output=$2,error_code=NULLIF($3,''),completed_at=NOW(),lease_expires_at=NULL
			WHERE id=$4 AND user_id=$5 AND leased_device_id=$6 AND lease_token_hash=$7 AND state='leased' AND lease_expires_at>NOW() RETURNING `+workflowDeviceJobColumns, state, output, errorCode, jobID, userID, deviceID, TestingHashToken(token)), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		// A duplicate completion is idempotent only for the same terminal state.
		err = db.agentTx(userID, func(tx *sql.Tx) error {
			return scanWorkflowDeviceJob(tx.QueryRow(`SELECT `+workflowDeviceJobColumns+` FROM workflow_device_node_jobs WHERE id=$1 AND user_id=$2 AND leased_device_id=$3 AND lease_token_hash=$4 AND state=$5`, jobID, userID, deviceID, TestingHashToken(token), state), item)
		})
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrInvalidLease
	}
	return item, err
}

func (db *Database) WorkflowDeviceNodeJob(ctx context.Context, userID, jobID string) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	err := db.agentTx(userID, func(tx *sql.Tx) error {
		return scanWorkflowDeviceJob(tx.QueryRowContext(ctx, `SELECT `+workflowDeviceJobColumns+` FROM workflow_device_node_jobs WHERE id=$1 AND user_id=$2`, jobID, userID), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrAgentJobNotFound
	}
	return item, err
}

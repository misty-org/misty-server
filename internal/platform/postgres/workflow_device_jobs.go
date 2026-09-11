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
	SpaceID            string          `json:"spaceId"`
	ID                 string          `json:"id"`
	RunID              string          `json:"runId"`
	NodeID             string          `json:"nodeId"`
	UserID             string          `json:"-"`
	ScopeID            string          `json:"scopeId"`
	Operation          string          `json:"operation"`
	State              string          `json:"state"`
	Attempt            int             `json:"attempt"`
	Input              json.RawMessage `json:"input"`
	Config             json.RawMessage `json:"config"`
	InputSchema        json.RawMessage `json:"inputSchema"`
	OutputSchema       json.RawMessage `json:"outputSchema"`
	Output             json.RawMessage `json:"output,omitempty"`
	LeasedDeviceID     string          `json:"leasedDeviceId,omitempty"`
	ContextID          string          `json:"contextId,omitempty"`
	AssignedDeviceID   string          `json:"assignedDeviceId,omitempty"`
	ErrorCode          string          `json:"errorCode,omitempty"`
	LeaseExpiresAt     *time.Time      `json:"leaseExpiresAt,omitempty"`
	LastHeartbeatAt    *time.Time      `json:"lastHeartbeatAt,omitempty"`
	CompletedAt        *time.Time      `json:"completedAt,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	ControlVersion     int             `json:"controlVersion"`
	DeadlineAt         time.Time       `json:"deadlineAt"`
	RuntimeRunID       string          `json:"-"`
	RequiredCapability string          `json:"-"`
	ExecutionStartedAt *time.Time      `json:"executionStartedAt,omitempty"`
	CancelRequestedAt  *time.Time      `json:"cancelRequestedAt,omitempty"`
}

const workflowDeviceJobColumns = `id,COALESCE(run_id,invocation_id,''),node_id,attempt,user_id,scope_id,operation,input,config,input_schema,output_schema,state,COALESCE(leased_device_id,''),COALESCE(context_id,ai_context_id,''),COALESCE(assigned_device_id,''),lease_expires_at,last_heartbeat_at,output,COALESCE(error_code,''),created_at,completed_at,control_version,deadline_at,runtime_run_id,required_capability,execution_started_at,cancel_requested_at`
const workflowDeviceJobUpdateColumns = `j.id,COALESCE(j.run_id,j.invocation_id,''),j.node_id,j.attempt,j.user_id,j.scope_id,j.operation,j.input,j.config,j.input_schema,j.output_schema,j.state,COALESCE(j.leased_device_id,''),COALESCE(j.context_id,j.ai_context_id,''),COALESCE(j.assigned_device_id,''),j.lease_expires_at,j.last_heartbeat_at,j.output,COALESCE(j.error_code,''),j.created_at,j.completed_at,j.control_version,j.deadline_at,j.runtime_run_id,j.required_capability,j.execution_started_at,j.cancel_requested_at`

func scanWorkflowDeviceJob(scanner interface{ Scan(...any) error }, item *WorkflowDeviceNodeJob) error {
	var output []byte
	if err := scanner.Scan(&item.ID, &item.RunID, &item.NodeID, &item.Attempt, &item.UserID, &item.ScopeID, &item.Operation, &item.Input, &item.Config, &item.InputSchema, &item.OutputSchema, &item.State, &item.LeasedDeviceID, &item.ContextID, &item.AssignedDeviceID, &item.LeaseExpiresAt, &item.LastHeartbeatAt, &output, &item.ErrorCode, &item.CreatedAt, &item.CompletedAt, &item.ControlVersion, &item.DeadlineAt, &item.RuntimeRunID, &item.RequiredCapability, &item.ExecutionStartedAt, &item.CancelRequestedAt); err != nil {
		return err
	}
	item.Output = output
	return nil
}

func (db *Database) QueueWorkflowDeviceNodeJob(ctx context.Context, userID, runID, nodeID string, attempt int, scopeID, operation, capability string, input, config, inputSchema, outputSchema json.RawMessage) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		runtimeID, budgetDeadline, err := deviceRunAuthorityTx(ctx, tx, userID, runID, nil, capability)
		if err != nil {
			return err
		}
		var contextID, deviceID string
		var contextCapabilities json.RawMessage
		var contextExpiresAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT c.id,c.device_id,c.capabilities,c.expires_at FROM space_runs r
			JOIN agent_run_contexts c ON c.run_id=r.id AND c.owner_user_id=$1 AND c.space_id=r.space_id
				AND c.opaque_ref=$3 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? $4
			JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds'
			WHERE r.id=$2 AND r.owner_user_id=$1 ORDER BY c.updated_at DESC LIMIT 1`, userID, runID, scopeID, capability).Scan(&contextID, &deviceID, &contextCapabilities, &contextExpiresAt); errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		} else if err != nil {
			return err
		}
		contextConfig := mustJSON(map[string]any{"contextId": contextID, "contextCapabilities": contextCapabilities, "contextExpiresAt": contextExpiresAt})
		return scanWorkflowDeviceJob(tx.QueryRowContext(ctx, `INSERT INTO workflow_device_node_jobs(id,run_id,node_id,attempt,user_id,scope_id,operation,input,config,input_schema,output_schema,context_id,assigned_device_id,deadline_at,runtime_run_id,required_capability)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb||$10::jsonb,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT(run_id,node_id,attempt) WHERE run_id IS NOT NULL DO UPDATE SET run_id=EXCLUDED.run_id
			WHERE workflow_device_node_jobs.input=EXCLUDED.input AND workflow_device_node_jobs.operation=EXCLUDED.operation AND workflow_device_node_jobs.scope_id=EXCLUDED.scope_id AND workflow_device_node_jobs.context_id=EXCLUDED.context_id AND workflow_device_node_jobs.assigned_device_id=EXCLUDED.assigned_device_id AND workflow_device_node_jobs.runtime_run_id=EXCLUDED.runtime_run_id AND workflow_device_node_jobs.required_capability=EXCLUDED.required_capability AND workflow_device_node_jobs.input_schema=EXCLUDED.input_schema AND workflow_device_node_jobs.output_schema=EXCLUDED.output_schema AND workflow_device_node_jobs.config=EXCLUDED.config
			RETURNING `+workflowDeviceJobColumns, "devicejob_"+uuid.NewString(), runID, nodeID, attempt, userID, scopeID, operation, input, config, contextConfig, inputSchema, outputSchema, contextID, deviceID, deviceJobDeadline(ctx, contextExpiresAt, budgetDeadline), runtimeID, capability), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceConflict
	}
	return item, err
}

func (db *Database) QueueAIInvocationDeviceNodeJob(ctx context.Context, userID, invocationID, nodeID string, attempt int, scopeID, operation, capability string, input, config, inputSchema, outputSchema json.RawMessage) (*WorkflowDeviceNodeJob, error) {
	item := &WorkflowDeviceNodeJob{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		runtimeID, budgetDeadline, err := deviceRunAuthorityTx(ctx, tx, userID, invocationID, nil, capability)
		if err != nil {
			return err
		}
		var contextID, deviceID string
		var contextCapabilities json.RawMessage
		var contextExpiresAt time.Time
		if err := tx.QueryRowContext(ctx, `SELECT c.id,c.device_id,c.capabilities,c.expires_at FROM ai_invocations i
			JOIN ai_invocation_contexts c ON c.invocation_id=i.id AND c.user_id=$1
				AND c.opaque_ref=$3 AND c.state='attached' AND c.expires_at>NOW() AND c.capabilities ? $4
			JOIN trusted_devices d ON d.id=c.device_id AND d.user_id=$1 AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds'
			WHERE i.id=$2 AND i.user_id=$1 AND i.state IN ('running','awaiting_approval') ORDER BY c.updated_at DESC LIMIT 1`, userID, invocationID, scopeID, capability).Scan(&contextID, &deviceID, &contextCapabilities, &contextExpiresAt); errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		} else if err != nil {
			return err
		}
		contextConfig := mustJSON(map[string]any{"contextId": contextID, "contextCapabilities": contextCapabilities, "contextExpiresAt": contextExpiresAt})
		return scanWorkflowDeviceJob(tx.QueryRowContext(ctx, `INSERT INTO workflow_device_node_jobs(id,invocation_id,node_id,attempt,user_id,scope_id,operation,input,config,input_schema,output_schema,ai_context_id,assigned_device_id,deadline_at,runtime_run_id,required_capability)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb||$10::jsonb,$11,$12,$13,$14,$15,$16,$17)
			ON CONFLICT(invocation_id,node_id,attempt) WHERE invocation_id IS NOT NULL DO UPDATE SET invocation_id=EXCLUDED.invocation_id
			WHERE workflow_device_node_jobs.input=EXCLUDED.input AND workflow_device_node_jobs.operation=EXCLUDED.operation AND workflow_device_node_jobs.scope_id=EXCLUDED.scope_id AND workflow_device_node_jobs.ai_context_id=EXCLUDED.ai_context_id AND workflow_device_node_jobs.assigned_device_id=EXCLUDED.assigned_device_id AND workflow_device_node_jobs.runtime_run_id=EXCLUDED.runtime_run_id AND workflow_device_node_jobs.required_capability=EXCLUDED.required_capability AND workflow_device_node_jobs.input_schema=EXCLUDED.input_schema AND workflow_device_node_jobs.output_schema=EXCLUDED.output_schema AND workflow_device_node_jobs.config=EXCLUDED.config
			RETURNING `+workflowDeviceJobColumns, "devicejob_"+uuid.NewString(), invocationID, nodeID, attempt, userID, scopeID, operation, input, config, contextConfig, inputSchema, outputSchema, contextID, deviceID, deviceJobDeadline(ctx, contextExpiresAt, budgetDeadline), runtimeID, capability), item)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceConflict
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

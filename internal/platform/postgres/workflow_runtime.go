package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type WorkflowRunStep struct {
	ID, RunID, NodeID, State, ErrorCode, ErrorMessage string
	Attempt                                           int
	Input, Output                                     json.RawMessage
	StartedAt, CompletedAt                            *time.Time
	UpdatedAt                                         time.Time
}

func (db *Database) WorkflowWritePreauthorized(ctx context.Context, userID, instanceID, workflowVersionID, nodeID, provider, connectionID, destination string) (bool, error) {
	var allowed bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM space_agent_instance_workflows w JOIN space_agent_instances i ON i.id=w.instance_id,
			LATERAL jsonb_array_elements(COALESCE(w.consent->'preauthorizedWrites','[]'::jsonb)) grant
			WHERE w.instance_id=$1 AND w.workflow_version_id=$2 AND w.enabled AND w.consent->>'granted'='true' AND i.user_id=$3
			AND grant->>'nodeId'=$4 AND COALESCE(grant->>'provider','')=$5 AND COALESCE(grant->>'connectionId','')=$6 AND COALESCE(grant->>'destination','')=$7)`, instanceID, workflowVersionID, userID, nodeID, provider, connectionID, destination).Scan(&allowed)
	})
	return allowed, err
}

func (db *Database) WorkflowRunSteps(ctx context.Context, userID, runID string) ([]WorkflowRunStep, error) {
	items := []WorkflowRunStep{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.run_id,s.node_id,s.state,s.attempt,s.input,s.output,COALESCE(s.error_code,''),COALESCE(s.error_message,''),s.started_at,s.completed_at,s.updated_at
			FROM space_run_steps s JOIN space_runs r ON r.id=s.run_id WHERE s.run_id=$1 AND r.requesting_member_id=$2 ORDER BY s.updated_at,s.node_id`, runID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item WorkflowRunStep
			if err := rows.Scan(&item.ID, &item.RunID, &item.NodeID, &item.State, &item.Attempt, &item.Input, &item.Output, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CompletedWorkflowStepOutputs(ctx context.Context, userID, runID string) (map[string]json.RawMessage, error) {
	outputs := map[string]json.RawMessage{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.node_id,s.output FROM space_run_steps s JOIN space_runs r ON r.id=s.run_id WHERE s.run_id=$1 AND r.requesting_member_id=$2 AND s.state IN ('completed','completed_with_errors')`, runID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var nodeID string
			var output []byte
			if err := rows.Scan(&nodeID, &output); err != nil {
				return err
			}
			outputs[nodeID] = json.RawMessage(output)
		}
		return rows.Err()
	})
	return outputs, err
}

func (db *Database) EnsureWorkflowNodeApproval(ctx context.Context, runID, nodeID, actionKind string, input json.RawMessage) (bool, error) {
	digestBytes := sha256.Sum256(append([]byte(nodeID+":"+actionKind+":"), input...))
	digest := hex.EncodeToString(digestBytes[:])
	approved := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var userID, spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT requesting_member_id,space_id FROM space_runs WHERE id=$1 AND state IN ('running','awaiting_approval') FOR UPDATE`, runID).Scan(&userID, &spaceID); err != nil {
			return err
		}
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM space_run_actions WHERE run_id=$1 AND action_kind=$2 AND details->>'node_id'=$3 AND details->>'action_digest'=$4 ORDER BY created_at DESC LIMIT 1`, runID, actionKind, nodeID, digest).Scan(&state)
		if err == nil {
			approved = state == "approved" || state == "completed"
			if !approved && state == "proposed" {
				_, err = tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_approval',updated_at=NOW() WHERE id=$1`, runID)
			}
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		actionID, approvalID := "runaction_"+uuid.NewString(), "runapproval_"+uuid.NewString()
		details := mustJSON(map[string]any{"node_id": nodeID, "action_digest": digest, "input": json.RawMessage(input)})
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_run_actions(id,run_id,action_kind,summary,details,destructive,state) VALUES($1,$2,$3,$4,$5,TRUE,'proposed')`, actionID, runID, actionKind, "Approve "+actionKind, details); err != nil {
			return err
		}
		proposed := mustJSON([]map[string]any{{"node_id": nodeID, "action_kind": actionKind, "action_digest": digest, "input": json.RawMessage(input)}})
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_run_approvals(id,run_id,requested_from_user_id,action_summary,proposed_actions) VALUES($1,$2,$3,$4,$5)`, approvalID, runID, userID, "Approve "+actionKind, proposed); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_approval',updated_at=NOW() WHERE id=$1`, runID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO space_inbox_items(user_id,space_id,kind,payload) VALUES($1,$2,'approval',$3)`, userID, spaceID, mustJSON(map[string]any{"run_id": runID, "node_id": nodeID, "approval_id": approvalID, "action_digest": digest}))
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return approved, err
}

func (db *Database) NotifyWorkflowResult(ctx context.Context, runID, nodeID string, payload json.RawMessage) (string, error) {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	eventID := "workflow_node_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO space_inbox_items(user_id,space_id,kind,event_id,payload)
			SELECT requesting_member_id,space_id,'workflow',$1,jsonb_build_object('run_id',id,'node_id',$2,'output',$3::jsonb)
			FROM space_runs WHERE id=$4 AND requesting_member_id=misty_rls_user_id()`, eventID, nodeID, payload, runID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			return ErrSpaceNotFound
		}
		return nil
	})
	return eventID, err
}

func (db *Database) WriteAgentMemoryEvent(ctx context.Context, instanceID, kind string, data json.RawMessage) (int64, error) {
	if kind != "user" && kind != "agent" && kind != "tool" && kind != "workflow" && kind != "compaction" || len(data) == 0 {
		return 0, ErrSpaceInvalid
	}
	var id int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `INSERT INTO space_agent_memory_events(instance_id,kind,data)
			SELECT id,$2,$3 FROM space_agent_instances WHERE id=$1 AND user_id=misty_rls_user_id() RETURNING id`, instanceID, kind, data).Scan(&id)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return id, err
}

func (db *Database) CheckpointWorkflowStep(ctx context.Context, runID string, event workflowv2.StepEvent) error {
	input, output := event.Input, event.Output
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	errorCode, errorMessage := "", ""
	if event.Error != nil {
		errorCode, errorMessage = workflowErrorCode(event.Error), event.Error.Error()
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_run_steps(id,run_id,node_id,state,attempt,input,output,error_code,error_message,started_at,completed_at,updated_at)
			VALUES('step_'||md5($1||':'||$2),$1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),CASE WHEN $3='running' THEN NOW() END,CASE WHEN $3 IN ('completed','completed_with_errors','failed','canceled','rejected') THEN NOW() END,NOW())
			ON CONFLICT(run_id,node_id) DO UPDATE SET state=EXCLUDED.state,attempt=EXCLUDED.attempt,input=EXCLUDED.input,output=EXCLUDED.output,error_code=EXCLUDED.error_code,error_message=EXCLUDED.error_message,started_at=COALESCE(space_run_steps.started_at,EXCLUDED.started_at),completed_at=EXCLUDED.completed_at,updated_at=NOW()`, runID, event.NodeID, event.State, event.Attempt, input, output, errorCode, errorMessage); err != nil {
			return err
		}
		if event.State == workflowv2.StepCooldown {
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='cooldown',attempt=$2,next_retry_at=NOW()+INTERVAL '60 seconds',updated_at=NOW() WHERE id=$1 AND state IN ('running','cooldown')`, runID, event.Attempt)
			return err
		}
		if event.State == workflowv2.StepRunning {
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='running',attempt=$2,next_retry_at=NULL,updated_at=NOW() WHERE id=$1 AND state IN ('queued','running','cooldown')`, runID, event.Attempt)
			return err
		}
		if event.State == workflowv2.StepAwaitingApproval {
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='awaiting_approval',next_retry_at=NULL,updated_at=NOW() WHERE id=$1 AND state IN ('running','awaiting_approval')`, runID)
			return err
		}
		return nil
	})
}

func workflowErrorCode(err error) string {
	if errors.Is(err, workflowv2.ErrDeviceUnavailable) {
		return "device_unavailable"
	}
	if errors.Is(err, workflowv2.ErrProviderMissing) {
		return "provider_unavailable"
	}
	if errors.Is(err, workflowv2.ErrOutputInvalid) {
		return "invalid_tool_output"
	}
	if errors.Is(err, workflowv2.ErrUnsupportedContent) {
		return "unsupported_content_type"
	}
	if errors.Is(err, workflowv2.ErrAwaitingApproval) {
		return "awaiting_approval"
	}
	return "node_failed"
}

func (db *Database) JournalWorkflowAction(ctx context.Context, runID, nodeID, idempotencyKey, provider string, risk workflowv2.Risk, request json.RawMessage, execute func() (json.RawMessage, error)) (json.RawMessage, error) {
	if len(request) == 0 {
		request = json.RawMessage(`{}`)
	}
	var existing json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state,result FROM space_workflow_action_journal WHERE idempotency_key=$1`, idempotencyKey).Scan(&state, &existing)
		if err == nil && state == "completed" {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO space_workflow_action_journal(idempotency_key,run_id,node_id,provider,risk,state,request) VALUES($1,$2,$3,$4,$5,'started',$6) ON CONFLICT(idempotency_key) DO UPDATE SET state='started',request=EXCLUDED.request,updated_at=NOW()`, idempotencyKey, runID, nodeID, provider, risk, request)
		return err
	})
	if err != nil || existing != nil {
		return existing, err
	}
	result, executeErr := execute()
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	state, code := "completed", ""
	if executeErr != nil {
		state, code = "failed", workflowErrorCode(executeErr)
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_workflow_action_journal SET state=$1,result=$2,error_code=NULLIF($3,''),updated_at=NOW() WHERE idempotency_key=$4`, state, result, code, idempotencyKey)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, executeErr
}

func (db *Database) ClaimWorkflowEvent(ctx context.Context, instanceID, workflowVersionID, provider, eventID, fingerprint, runID string) (bool, error) {
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_event_claims(instance_id,workflow_version_id,provider,event_id,fingerprint,run_id,state) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),'claimed') ON CONFLICT DO NOTHING`, instanceID, workflowVersionID, provider, eventID, fingerprint, runID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		claimed = count == 1
		return nil
	})
	return claimed, err
}

func (db *Database) ReclaimFailedWorkflowEvent(ctx context.Context, instanceID, workflowVersionID, provider, eventID, fingerprint, runID string) (bool, error) {
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_workflow_event_claims SET state='claimed',fingerprint=$1,run_id=NULLIF($2,''),updated_at=NOW()
			WHERE instance_id=$3 AND workflow_version_id=$4 AND provider=$5 AND event_id=$6 AND state='failed'`, fingerprint, runID, instanceID, workflowVersionID, provider, eventID)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		claimed = count == 1
		return nil
	})
	return claimed, err
}

func (db *Database) AcquireWorkflowResourceLease(ctx context.Context, runID, nodeID, resourceKey, fingerprint string, duration time.Duration) (bool, error) {
	if duration < time.Second || duration > 10*time.Minute {
		return false, ErrSpaceInvalid
	}
	acquired := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_resource_leases(resource_key,run_id,node_id,fingerprint,expires_at) VALUES($1,$2,$3,$4,NOW()+$5::interval) ON CONFLICT(resource_key) DO UPDATE SET run_id=EXCLUDED.run_id,node_id=EXCLUDED.node_id,fingerprint=EXCLUDED.fingerprint,expires_at=EXCLUDED.expires_at,created_at=NOW() WHERE space_workflow_resource_leases.expires_at<=NOW() OR space_workflow_resource_leases.run_id=EXCLUDED.run_id`, resourceKey, runID, nodeID, fingerprint, duration.String())
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		acquired = count == 1
		return nil
	})
	return acquired, err
}

func (db *Database) ReleaseWorkflowResourceLease(ctx context.Context, runID, nodeID, resourceKey string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM space_workflow_resource_leases WHERE resource_key=$1 AND run_id=$2 AND node_id=$3`, resourceKey, runID, nodeID)
		return err
	})
}

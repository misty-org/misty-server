package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type WorkflowRunStep struct {
	ID           string          `json:"id"`
	RunID        string          `json:"run_id"`
	NodeID       string          `json:"node_id"`
	State        string          `json:"state"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	Attempt      int             `json:"attempt"`
	Input        json.RawMessage `json:"input"`
	Output       json.RawMessage `json:"output"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
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


package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"strings"
	"time"
)

type PersonalAgentRunSummary struct {
	RunID              string          `json:"run_id"`
	AgentID            string          `json:"agent_id"`
	SpaceID            string          `json:"space_id"`
	SpaceName          string          `json:"space_name"`
	TriggerKind        string          `json:"trigger_kind"`
	SourceType         string          `json:"source_type"`
	SourceMessageID    string          `json:"source_message_id,omitempty"`
	ResponseMessageID  string          `json:"response_message_id,omitempty"`
	InputModality      string          `json:"input_modality"`
	State              string          `json:"state"`
	HasFailedSteps     bool            `json:"has_failed_steps"`
	Phase              string          `json:"phase"`
	Progress           int             `json:"progress"`
	Attempt            int             `json:"attempt"`
	RuntimeKind        string          `json:"runtime_kind,omitempty"`
	ErrorCode          string          `json:"error_code,omitempty"`
	ErrorMessage       string          `json:"error_message,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	RuntimeHeartbeatAt *time.Time      `json:"runtime_heartbeat_at,omitempty"`
	OwnerUserID        string          `json:"owner_user_id"`
	InitialRunMode     string          `json:"initial_run_mode"`
	EffectiveRunMode   string          `json:"effective_run_mode"`
	ParentRunID        string          `json:"parent_run_id,omitempty"`
	DelegationDepth    int             `json:"delegation_depth"`
	ContextBindings    json.RawMessage `json:"context_bindings"`
}

type PersonalAgentRunDetail struct {
	Summary     PersonalAgentRunSummary `json:"summary"`
	Instruction string                  `json:"instruction"`
	Result      json.RawMessage         `json:"result"`
	Steps       []WorkflowRunStep       `json:"steps"`
}

const personalAgentRunSummaryColumns = `r.id,r.agent_id,''::text,''::text,
	r.trigger_kind,r.source_type,COALESCE(r.source_message_id,''),COALESCE((SELECT response.id FROM space_messages response WHERE response.origin->>'agent_run_id'=r.id ORDER BY response.created_at DESC LIMIT 1),''),COALESCE(r.input->>'input_modality','text'),r.state,EXISTS(SELECT 1 FROM space_run_steps failed_step WHERE failed_step.run_id=r.id AND failed_step.state='failed'),r.runtime_phase,r.progress,r.attempt,r.runtime_kind,COALESCE(r.error_code,''),COALESCE(r.error_message,''),
	r.created_at,r.updated_at,r.completed_at,r.runtime_heartbeat_at,
	r.owner_user_id,r.initial_run_mode,r.effective_run_mode,COALESCE(r.parent_run_id,''),r.delegation_depth,r.context_bindings`

func scanPersonalAgentRunSummary(row scanner, out *PersonalAgentRunSummary) error {
	return row.Scan(&out.RunID, &out.AgentID, &out.SpaceID, &out.SpaceName,
		&out.TriggerKind, &out.SourceType, &out.SourceMessageID, &out.ResponseMessageID, &out.InputModality, &out.State, &out.HasFailedSteps, &out.Phase, &out.Progress, &out.Attempt,
		&out.RuntimeKind, &out.ErrorCode, &out.ErrorMessage, &out.CreatedAt, &out.UpdatedAt,
		&out.CompletedAt, &out.RuntimeHeartbeatAt, &out.OwnerUserID, &out.InitialRunMode, &out.EffectiveRunMode,
		&out.ParentRunID, &out.DelegationDepth, &out.ContextBindings)
}

func (db *Database) PersonalAgentRunDetailForOwner(ctx context.Context, userID, runID string) (*PersonalAgentRunDetail, error) {
	out := &PersonalAgentRunDetail{Steps: []WorkflowRunStep{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanPersonalAgentRunSummary(tx.QueryRowContext(ctx, `SELECT `+personalAgentRunSummaryColumns+`
			FROM space_runs r
			JOIN misty_ask_identities a ON a.id=r.agent_id
			WHERE r.id=$1 AND a.owner_user_id=$2 AND a.deleted_at IS NULL
			  AND r.owner_user_id=$2`, runID, userID), &out.Summary); err != nil {
			return err
		}
		var input json.RawMessage
		if err := tx.QueryRowContext(ctx, `SELECT input,result FROM space_runs WHERE id=$1`, runID).Scan(&input, &out.Result); err != nil {
			return err
		}
		var runInput struct {
			Instruction string `json:"instruction"`
		}
		if json.Unmarshal(input, &runInput) == nil {
			out.Instruction = strings.TrimSpace(runInput.Instruction)
		}
		stepRows, err := tx.QueryContext(ctx, `SELECT id,run_id,node_id,state,attempt,input,output,COALESCE(error_code,''),COALESCE(error_message,''),started_at,completed_at,updated_at FROM space_run_steps WHERE run_id=$1 ORDER BY updated_at,node_id`, runID)
		if err != nil {
			return err
		}
		for stepRows.Next() {
			var item WorkflowRunStep
			if err := stepRows.Scan(&item.ID, &item.RunID, &item.NodeID, &item.State, &item.Attempt, &item.Input, &item.Output, &item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt, &item.UpdatedAt); err != nil {
				stepRows.Close()
				return err
			}
			out.Steps = append(out.Steps, item)
		}
		return stepRows.Close()
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) TouchPersonalAgentTaskRuntime(ctx context.Context, runID, runtimeRunID, phase string, progress int) error {
	phase = strings.TrimSpace(phase)
	if phase == "" || len(phase) > 80 {
		return ErrSpaceInvalid
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 99 {
		progress = 99
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_runs SET runtime_phase=$1,progress=GREATEST(progress,$2),runtime_heartbeat_at=NOW(),updated_at=NOW() WHERE id=$3 AND runtime_run_id=$4 AND state='running'`, phase, progress, runID, runtimeRunID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrSpaceForbidden
		}
		return nil
	})
}

func (db *Database) FinishDispatchedPersonalAgentTaskRunJob(ctx context.Context, runID, runtimeRunID, state string) error {
	if state != "completed" && state != "failed" && state != "canceled" {
		return ErrSpaceInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs j SET state=$1,completed_at=NOW(),updated_at=NOW()
			FROM space_runs r WHERE j.run_id=$2 AND j.state='dispatched' AND r.id=j.run_id AND r.runtime_run_id=$3`, state, runID, runtimeRunID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			var current string
			if err := tx.QueryRowContext(ctx, `SELECT state FROM agent_run_jobs WHERE run_id=$1`, runID).Scan(&current); err == nil && current == state {
				return nil
			}
			return ErrSpaceConflict
		}
		return nil
	})
}

// PersonalAgentRuntimeRecord is the run a runtime callback names.
func (db *Database) PersonalAgentRuntimeRecord(ctx context.Context, runID, runtimeRunID string) (*SpaceRun, error) {
	run := &SpaceRun{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE id=$1 AND runtime_run_id=$2`, runID, runtimeRunID), run)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return run, err
}

func releasePersonalAgentRuntimeReservationsTx(ctx context.Context, tx *sql.Tx, runID string) error {
	group := "agent-runtime:" + runID
	if _, err := tx.ExecContext(ctx, `UPDATE billing_adapter_intents SET state='abandoned',expires_at=now() WHERE admission->>'operation_id'=$1 AND state='pending'`, group); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.reservation_id,r.admission FROM billing_adapter_reservations r WHERE r.admission->>'operation_id'=$1 AND NOT EXISTS(SELECT 1 FROM billing_adapter_outbox o WHERE o.payload->>'account_id'=r.account_id AND o.action IN ('settle','settle_group') AND (o.payload->>'reservation_id'=r.reservation_id OR o.payload->'reservation_ids' ? r.reservation_id))`, group)
	if err != nil {
		return err
	}
	var requests []billingadapter.Request
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var request billingadapter.Request
		if err = json.Unmarshal(raw, &request); err != nil {
			rows.Close()
			return err
		}
		request.ReservationID = id
		request.Key = "cancel:" + request.Key
		requests = append(requests, request)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, request := range requests {
		raw, err := json.Marshal(request)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(request.AccountID + "\x00" + request.Key))
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_adapter_outbox(id,action,payload) VALUES($1,'release',$2) ON CONFLICT(id) DO NOTHING`, hex.EncodeToString(sum[:]), string(raw)); err != nil {
			return err
		}
	}
	return nil
}

func (db *Database) ReleasePersonalAgentRuntimeReservations(ctx context.Context, runID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID)
	})
}

func (db *Database) CancelPersonalAgentTaskRunForOwner(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs r
			WHERE r.id=$1
			AND EXISTS(SELECT 1 FROM misty_ask_identities a WHERE a.id=r.agent_id AND a.owner_user_id=$2 AND a.deleted_at IS NULL)
			AND r.owner_user_id=$2
			FOR UPDATE`, runID, userID), out); err != nil {
			return err
		}
		if err := cancelMistyChildrenTx(ctx, tx, userID, runID); err != nil {
			return err
		}
		if out.State == "canceled" {
			return nil
		}
		if out.State != "queued" && out.State != "running" && out.State != "awaiting_approval" && out.State != "awaiting_device" && out.State != "awaiting_intervention" {
			return ErrSpaceConflict
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='canceled',runtime_phase='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW() WHERE id=$1 RETURNING `+spaceRunColumns, runID), out); err != nil {
			return err
		}
		if out.RuntimeRunID != "" {
			if err := queueAgentContinuationTx(ctx, tx, userID, runID, "runtime.cancel", out.RuntimeRunID, AgentContinuation{RuntimeID: out.RuntimeRunID}); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW() WHERE run_id=$1 AND state IN ('queued','leased','dispatched')`, runID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_contexts SET state='detached',updated_at=NOW() WHERE run_id=$1 AND state='attached'`, runID); err != nil {
			return err
		}
		if err := releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID); err != nil {
			return err
		}
		return nil // Account event trigger publishes the state change.
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return out, err
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PersonalAgentRunSummary struct {
	RunID              string          `json:"run_id"`
	AgentID            string          `json:"agent_id"`
	SpaceID            string          `json:"space_id"`
	SpaceName          string          `json:"space_name"`
	TaskID             string          `json:"task_id"`
	TaskKey            string          `json:"task_key"`
	TaskTitle          string          `json:"task_title"`
	TaskStatus         string          `json:"task_status"`
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
	ApprovalState      string          `json:"approval_state"`
	ParentRunID        string          `json:"parent_run_id,omitempty"`
	DelegationDepth    int             `json:"delegation_depth"`
	ContextBindings    json.RawMessage `json:"context_bindings"`
}

type PersonalAgentActivityPage struct {
	AgentID    string                    `json:"agent_id"`
	WorkState  string                    `json:"work_state"`
	QueueCount int                       `json:"queue_count"`
	ActiveRun  *PersonalAgentRunSummary  `json:"active_run,omitempty"`
	Runs       []PersonalAgentRunSummary `json:"runs"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

type PersonalAgentRunDetail struct {
	Summary     PersonalAgentRunSummary `json:"summary"`
	Instruction string                  `json:"instruction"`
	Result      json.RawMessage         `json:"result"`
	Steps       []WorkflowRunStep       `json:"steps"`
	Activity    []SpaceTaskActivity     `json:"activity"`
	Approvals   []AgentToolApproval     `json:"approvals"`
}

const personalAgentRunSummaryColumns = `r.id,r.agent_id,r.space_id,s.name,COALESCE(t.id,''),COALESCE(t.task_key,''),COALESCE(t.title,''),COALESCE(t.status,''),
	r.trigger_kind,r.source_type,COALESCE(r.source_message_id,''),COALESCE((SELECT response.id FROM space_messages response WHERE response.origin->>'agent_run_id'=r.id ORDER BY response.created_at DESC LIMIT 1),''),COALESCE(r.input->>'input_modality','text'),r.state,EXISTS(SELECT 1 FROM space_run_steps failed_step WHERE failed_step.run_id=r.id AND failed_step.state='failed'),r.runtime_phase,r.progress,r.attempt,r.runtime_kind,COALESCE(r.error_code,''),COALESCE(r.error_message,''),
	r.created_at,r.updated_at,r.completed_at,r.runtime_heartbeat_at,
	r.owner_user_id,r.initial_run_mode,r.effective_run_mode,r.approval_state,COALESCE(r.parent_run_id,''),r.delegation_depth,r.context_bindings`

func scanPersonalAgentRunSummary(row scanner, out *PersonalAgentRunSummary) error {
	return row.Scan(&out.RunID, &out.AgentID, &out.SpaceID, &out.SpaceName, &out.TaskID, &out.TaskKey,
		&out.TaskTitle, &out.TaskStatus, &out.TriggerKind, &out.SourceType, &out.SourceMessageID, &out.ResponseMessageID, &out.InputModality, &out.State, &out.HasFailedSteps, &out.Phase, &out.Progress, &out.Attempt,
		&out.RuntimeKind, &out.ErrorCode, &out.ErrorMessage, &out.CreatedAt, &out.UpdatedAt,
		&out.CompletedAt, &out.RuntimeHeartbeatAt, &out.OwnerUserID, &out.InitialRunMode, &out.EffectiveRunMode,
		&out.ApprovalState, &out.ParentRunID, &out.DelegationDepth, &out.ContextBindings)
}

func (db *Database) PersonalAgentActivity(ctx context.Context, userID, agentID, before string, limit int) (*PersonalAgentActivityPage, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	out := &PersonalAgentActivityPage{AgentID: agentID, WorkState: "ready", Runs: []PersonalAgentRunSummary{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var owner bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_agents WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL)`, agentID, userID).Scan(&owner); err != nil {
			return err
		}
		if !owner {
			return ErrPersonalAgentNotFound
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id
			WHERE j.agent_id=$1 AND j.state='queued' AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=r.space_id AND m.user_id=$2)`, agentID, userID).Scan(&out.QueueCount); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+personalAgentRunSummaryColumns+`
			FROM space_runs r JOIN spaces s ON s.id=r.space_id LEFT JOIN space_tasks t ON t.id=r.source_task_id
			WHERE r.agent_id=$1 AND r.owner_user_id=$2
			  AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=r.space_id AND m.user_id=$2)
			  AND ($3='' OR r.created_at<to_timestamp($3::double precision / 1000000))
			ORDER BY r.created_at DESC,r.id DESC LIMIT $4`, agentID, userID, before, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PersonalAgentRunSummary
			if err := scanPersonalAgentRunSummary(rows, &item); err != nil {
				return err
			}
			out.Runs = append(out.Runs, item)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(out.Runs) > limit {
			last := out.Runs[limit-1]
			out.NextCursor = strconv.FormatInt(last.CreatedAt.UnixMicro(), 10)
			out.Runs = out.Runs[:limit]
		}
		for index := range out.Runs {
			item := &out.Runs[index]
			if item.State == "running" || item.State == "awaiting_approval" || item.State == "awaiting_device" {
				out.ActiveRun, out.WorkState = item, item.State
				break
			}
		}
		if out.ActiveRun == nil && out.QueueCount > 0 {
			out.WorkState = "queued"
		} else if out.ActiveRun == nil && len(out.Runs) > 0 && (out.Runs[0].State == "failed" || out.Runs[0].State == "completed_with_errors") {
			out.WorkState = "failed"
		}
		return nil
	})
	return out, err
}

func (db *Database) PersonalAgentRunDetailForOwner(ctx context.Context, userID, runID string) (*PersonalAgentRunDetail, error) {
	out := &PersonalAgentRunDetail{Steps: []WorkflowRunStep{}, Activity: []SpaceTaskActivity{}, Approvals: []AgentToolApproval{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanPersonalAgentRunSummary(tx.QueryRowContext(ctx, `SELECT `+personalAgentRunSummaryColumns+`
			FROM space_runs r JOIN spaces s ON s.id=r.space_id LEFT JOIN space_tasks t ON t.id=r.source_task_id
			JOIN personal_agents a ON a.id=r.agent_id
			WHERE r.id=$1 AND a.owner_user_id=$2 AND a.deleted_at IS NULL
			  AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=r.space_id AND m.user_id=$2)`, runID, userID), &out.Summary); err != nil {
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
		if err := stepRows.Close(); err != nil {
			return err
		}
		activityRows, err := tx.QueryContext(ctx, `SELECT `+spaceTaskActivityColumns+` FROM space_task_activity WHERE run_id=$1 ORDER BY created_at,id`, runID)
		if err != nil {
			return err
		}
		for activityRows.Next() {
			var item SpaceTaskActivity
			if err := scanSpaceTaskActivity(activityRows, &item); err != nil {
				return err
			}
			out.Activity = append(out.Activity, item)
		}
		if err := activityRows.Close(); err != nil {
			return err
		}
		approvalRows, err := tx.QueryContext(ctx, `SELECT `+agentToolApprovalColumns+` FROM agent_run_tool_approvals WHERE run_id=$1 ORDER BY created_at`, runID)
		if err != nil {
			return err
		}
		defer approvalRows.Close()
		for approvalRows.Next() {
			var item AgentToolApproval
			if err := scanAgentToolApproval(approvalRows, &item); err != nil {
				return err
			}
			item.HookToken = ""
			item.SignedCall = ""
			out.Approvals = append(out.Approvals, item)
		}
		return approvalRows.Err()
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
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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

func (db *Database) PersonalAgentTaskDone(ctx context.Context, runID, runtimeRunID string) (bool, error) {
	var done bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT t.status='done' FROM space_tasks t JOIN space_runs r ON r.source_task_id=t.id
			WHERE r.id=$1 AND r.runtime_run_id=$2 AND r.state='running' AND t.assignee_agent_id=r.agent_id AND t.archived_at IS NULL`, runID, runtimeRunID).Scan(&done)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceForbidden
	}
	return done, err
}

func (db *Database) PersonalAgentTaskRuntimeRecord(ctx context.Context, runID, runtimeRunID string) (*SpaceRun, *SpaceTask, error) {
	run := &SpaceRun{}
	task := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE id=$1 AND runtime_run_id=$2`, runID, runtimeRunID), run); err != nil {
			return err
		}
		if run.SourceTaskID == "" {
			return nil
		}
		return scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE id=$1`, run.SourceTaskID), task)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return run, task, err
}

func (db *Database) AddPersonalAgentRuntimeFinalActivity(ctx context.Context, run *SpaceRun, task *SpaceTask, kind, message string, metadata json.RawMessage) error {
	if run == nil || task == nil || strings.TrimSpace(message) == "" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_task_activity WHERE run_id=$1 AND metadata->>'runtime_final'='true')`, run.ID).Scan(&exists); err != nil || exists {
			return err
		}
		var values map[string]any
		if json.Unmarshal(metadata, &values) != nil || values == nil {
			values = map[string]any{}
		}
		values["runtime_final"] = true
		_, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: task.SpaceID, TaskID: task.ID, ActorKind: "agent", ActorAgentID: run.AgentID, RunID: run.ID, Kind: kind, Message: message, Metadata: mustJSON(values)})
		return err
	})
}

func releasePersonalAgentRuntimeReservationsTx(ctx context.Context, tx *sql.Tx, runID string) error {
	_, err := tx.ExecContext(ctx, `WITH released AS (
		UPDATE hosted_ai_reservations SET status='released',settled_at=NOW()
		WHERE LEFT(idempotency_key,LENGTH($1))=$1 AND status='reserved'
		RETURNING user_id,reserved_microusd
	), totals AS (
		SELECT user_id,SUM(reserved_microusd) AS amount FROM released GROUP BY user_id
	)
	UPDATE hosted_ai_wallets w SET reserved_microusd=GREATEST(0,w.reserved_microusd-t.amount),updated_at=NOW()
	FROM totals t WHERE w.user_id=t.user_id`, "agent-runtime:"+runID+":model:")
	return err
}

func (db *Database) ReleasePersonalAgentRuntimeReservations(ctx context.Context, runID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID)
	})
}

func (db *Database) CancelPersonalAgentTaskRunForOwner(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs r
			WHERE r.id=$1
			AND EXISTS(SELECT 1 FROM personal_agents a WHERE a.id=r.agent_id AND a.owner_user_id=$2 AND a.deleted_at IS NULL)
			AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=r.space_id AND m.user_id=$2)
			FOR UPDATE`, runID, userID), out); err != nil {
			return err
		}
		if out.State == "canceled" {
			return nil
		}
		if out.State != "queued" && out.State != "running" && out.State != "awaiting_approval" && out.State != "awaiting_device" {
			return ErrSpaceConflict
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='canceled',runtime_phase='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW() WHERE id=$1 RETURNING `+spaceRunColumns, runID), out); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW() WHERE run_id=$1 AND state IN ('queued','leased','dispatched')`, runID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='denied',decided_by_user_id=$2,decided_at=NOW() WHERE run_id=$1 AND state='pending'`, runID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_run_contexts SET state='detached',updated_at=NOW() WHERE run_id=$1 AND state='attached'`, runID); err != nil {
			return err
		}
		if err := releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, out.SpaceID, userID, "agent.run.canceled", runID, map[string]any{"agent_id": out.AgentID, "task_id": out.SourceTaskID})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) RetryPersonalAgentTaskRunForOwner(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		previous := &SpaceRun{}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs r
			WHERE r.id=$1
			AND EXISTS(SELECT 1 FROM personal_agents a WHERE a.id=r.agent_id AND a.owner_user_id=$2 AND a.deleted_at IS NULL)
			AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=r.space_id AND m.user_id=$2)
			FOR UPDATE`, runID, userID), previous); err != nil {
			return err
		}
		legacyFailedTools := false
		if previous.State == "completed" {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_run_steps WHERE run_id=$1 AND state='failed')`, previous.ID).Scan(&legacyFailedTools); err != nil {
				return err
			}
		}
		if previous.State != "failed" && previous.State != "canceled" && previous.State != "completed_with_errors" && !legacyFailedTools {
			return ErrSpaceConflict
		}
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, previous.SpaceID, previous.AgentID); err != nil {
			return err
		}
		var task *SpaceTask
		if previous.SourceTaskID != "" {
			task = &SpaceTask{}
			if err := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE id=$1 AND assignee_agent_id=$2 AND archived_at IS NULL`, previous.SourceTaskID, previous.AgentID), task); err != nil {
				return err
			}
		}
		newID := "run_" + uuid.NewString()
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `INSERT INTO space_runs(
			id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,result,
			requesting_member_id,source_conversation_id,source_type,agent_id,capability_id,outputs,artifacts,retry_of_run_id,
			agent_version_id,attempt,source_task_id,action_envelope,conversation_scope_kind,scope_conversation_id,source_message_id,
			owner_user_id,initial_run_mode,effective_run_mode,agent_version_snapshot,parent_run_id,delegation_depth,context_bindings)
			SELECT $1,r.space_id,'agent',r.agent_id,$2,$2,'retry','queued',r.input,'{}'::jsonb,
			$2,r.source_conversation_id,r.source_type,r.agent_id,r.capability_id,'{}'::jsonb,'[]'::jsonb,r.id,
			r.agent_version_id,1,r.source_task_id,r.action_envelope,r.conversation_scope_kind,r.scope_conversation_id,r.source_message_id,
			$2,r.initial_run_mode,r.initial_run_mode,r.agent_version_snapshot,r.parent_run_id,r.delegation_depth,r.context_bindings
			FROM space_runs r WHERE r.id=$3 RETURNING `+spaceRunColumns, newID, userID, previous.ID), out); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_contexts(id,run_id,owner_user_id,space_id,device_id,kind,opaque_ref,display_name,capabilities,metadata,expires_at)
			SELECT 'context_'||gen_random_uuid(),$1,$2,space_id,device_id,kind,opaque_ref,display_name,capabilities,metadata,NOW()+INTERVAL '24 hours'
			FROM agent_run_contexts WHERE run_id=$3 AND state='attached' AND expires_at>NOW()`, out.ID, userID, previous.ID); err != nil {
			return err
		}
		jobTrigger := "direct_instruction"
		if task != nil {
			jobTrigger = "task_assignment"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_jobs(run_id,space_id,task_id,agent_id,trigger_kind) VALUES($1,$2,NULLIF($3,''),$4,$5)`, out.ID, out.SpaceID, out.SourceTaskID, out.AgentID, jobTrigger); err != nil {
			return err
		}
		if task != nil {
			if _, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: out.SpaceID, TaskID: task.ID, ActorKind: "agent", ActorAgentID: out.AgentID, RunID: out.ID, Kind: "progress", Message: "Queued to retry this task", Metadata: mustJSON(map[string]any{"retry_of_run_id": previous.ID})}); err != nil {
				return err
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, out.SpaceID, userID, "agent.run.queued", out.ID, map[string]any{"agent_id": out.AgentID, "task_id": out.SourceTaskID, "retry_of_run_id": previous.ID})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceNotFound
	}
	return out, err
}

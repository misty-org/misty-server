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

type SpaceTaskActivity struct {
	ID           string          `json:"id"`
	SpaceID      string          `json:"space_id"`
	TaskID       string          `json:"task_id"`
	ActorKind    string          `json:"actor_kind"`
	ActorUserID  string          `json:"actor_user_id,omitempty"`
	ActorAgentID string          `json:"actor_agent_id,omitempty"`
	RunID        string          `json:"run_id,omitempty"`
	Kind         string          `json:"kind"`
	Message      string          `json:"message"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
}

const spaceTaskActivityColumns = `id,space_id,task_id,actor_kind,COALESCE(actor_user_id,''),COALESCE(actor_agent_id,''),COALESCE(run_id,''),kind,message,metadata,created_at`

func scanSpaceTaskActivity(row scanner, out *SpaceTaskActivity) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.TaskID, &out.ActorKind, &out.ActorUserID, &out.ActorAgentID, &out.RunID, &out.Kind, &out.Message, &out.Metadata, &out.CreatedAt)
}

func insertTaskActivityTx(ctx context.Context, tx *sql.Tx, item SpaceTaskActivity) (*SpaceTaskActivity, error) {
	item.Message = strings.TrimSpace(item.Message)
	if item.ID == "" {
		item.ID = "taskactivity_" + uuid.NewString()
	}
	if len(item.Metadata) == 0 {
		item.Metadata = json.RawMessage(`{}`)
	}
	if !validJSONObject(item.Metadata) || len([]rune(item.Message)) > 12_000 {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceTaskActivity{}
	err := scanSpaceTaskActivity(tx.QueryRowContext(ctx, `INSERT INTO space_task_activity(
		id,space_id,task_id,actor_kind,actor_user_id,actor_agent_id,run_id,kind,message,metadata
	) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,$10) RETURNING `+spaceTaskActivityColumns,
		item.ID, item.SpaceID, item.TaskID, item.ActorKind, item.ActorUserID, item.ActorAgentID, item.RunID, item.Kind, item.Message, item.Metadata), out)
	return out, err
}

func (db *Database) AddSpaceTaskAgentActivity(ctx context.Context, taskID, agentID, runID, kind, message string, metadata json.RawMessage) (*SpaceTaskActivity, error) {
	out := &SpaceTaskActivity{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_tasks WHERE id=$1`, taskID).Scan(&spaceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			return err
		}
		var err error
		out, err = insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: spaceID, TaskID: taskID, ActorKind: "agent", ActorAgentID: agentID, RunID: runID, Kind: kind, Message: message, Metadata: metadata})
		return err
	})
	return out, err
}

func (db *Database) SpaceTaskActivity(ctx context.Context, userID, spaceID, taskID string) ([]SpaceTaskActivity, error) {
	items := []SpaceTaskActivity{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, spaceID, "space_tasks", taskID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrSpaceNotFound
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_tasks WHERE id=$1 AND space_id=$2)`, taskID, spaceID).Scan(&exists); err != nil || !exists {
			return ErrSpaceNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceTaskActivityColumns+` FROM space_task_activity WHERE task_id=$1 ORDER BY created_at,id`, taskID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceTaskActivity
			if err := scanSpaceTaskActivity(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) ClaimAssignedAgentTaskRun(ctx context.Context, userID string, task SpaceTask) (*SpaceRun, bool, error) {
	if task.AssigneeAgentID == "" {
		return nil, false, nil
	}
	out := &SpaceRun{}
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, task.SpaceID, task.AssigneeAgentID)
		if err != nil {
			return err
		}
		if !agentMembershipPermission(membership.Permissions, PermissionTasksView) || !agentMembershipPermission(membership.Permissions, PermissionTasksManage) {
			return ErrSpaceForbidden
		}
		var assignmentExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_task_activity WHERE task_id=$1 AND kind='assigned'
			AND actor_agent_id IS NULL AND metadata->>'agent_id'=$2 AND metadata->>'task_version'=$3)`, task.ID, task.AssigneeAgentID, strconv.FormatInt(task.Version, 10)).Scan(&assignmentExists); err != nil {
			return err
		}
		if !assignmentExists {
			return nil
		}
		envelope := mustJSON(map[string]any{
			"trigger": "task_assignment", "task_id": task.ID, "assignment_task_version": task.Version,
			"agent_membership_id": membership.ID, "approved_agent_version_id": membership.ApprovedVersionID,
			"allowed_tools": []string{"tasks.query", "tasks.update_assigned", "task.activity.write", "attached_files.read"},
			"approval_mode": "explicit_assignment",
		})
		var existingID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM space_runs WHERE source_task_id=$1 AND agent_id=$2
			AND action_envelope->>'assignment_task_version'=$3 LIMIT 1`, task.ID, task.AssigneeAgentID, strconv.FormatInt(task.Version, 10)).Scan(&existingID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		input := mustJSON(map[string]any{"task": task, "source_refs": task.SourceRefs})
		out = &SpaceRun{
			ID: "run_" + uuid.NewString(), SpaceID: task.SpaceID, ResourceKind: "agent", ResourceID: task.AssigneeAgentID,
			InitiatedByUserID: userID, BillingUserID: userID, TriggerKind: "task_assignment", State: "queued", Input: input,
			Result: json.RawMessage(`{}`), RequestingMemberID: userID, SourceType: "task", AgentID: task.AssigneeAgentID,
			CapabilityID: "task_assignment", Outputs: json.RawMessage(`{}`), Artifacts: json.RawMessage(`[]`), Attempt: 1,
			SourceTaskID: task.ID, ActionEnvelope: envelope,
		}
		err = scanSpaceRun(tx.QueryRowContext(ctx, `INSERT INTO space_runs(
			id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,result,
			requesting_member_id,source_type,agent_id,capability_id,outputs,artifacts,attempt,source_task_id,action_envelope
		) VALUES($1,$2,'agent',$3,$4,$4,'task_assignment','queued',$5,'{}'::jsonb,$4,'task',$3,'task_assignment','{}'::jsonb,'[]'::jsonb,1,$6,$7)
		ON CONFLICT DO NOTHING
		RETURNING `+spaceRunColumns, out.ID, task.SpaceID, task.AssigneeAgentID, userID, input, task.ID, envelope), out)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_agent_task_run_jobs(run_id,space_id,task_id,agent_id)
			VALUES($1,$2,$3,$4)`, out.ID, task.SpaceID, task.ID, task.AssigneeAgentID); err != nil {
			return err
		}
		if _, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: task.SpaceID, TaskID: task.ID, ActorKind: "agent", ActorAgentID: task.AssigneeAgentID, RunID: out.ID, Kind: "progress", Message: "Queued to work on this task", Metadata: mustJSON(map[string]any{"task_version": task.Version})}); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, task.SpaceID, userID, "agent.run.queued", out.ID, map[string]any{"agent_id": task.AssigneeAgentID, "source_type": "task", "task_id": task.ID})
		claimed = err == nil
		return err
	})
	return out, claimed, err
}

package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (db *Database) SaveSpaceStudioResource(ctx context.Context, userID string, item SpaceStudioResource) (*SpaceStudioResource, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.Version == 0 {
		item.ID = item.Kind + "_" + uuid.NewString()
	} else if item.ID == "" {
		item.ID = item.Kind + "_" + uuid.NewString()
	}
	if len([]rune(item.Name)) < 1 || len([]rune(item.Name)) > 80 || (item.Kind != "agent" && item.Kind != "workflow") {
		return nil, ErrSpaceInvalid
	}
	if len(item.Definition) == 0 {
		item.Definition = json.RawMessage(`{}`)
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionStudioManage); err != nil {
			return err
		}
		if item.Kind == "agent" {
			if len(item.AccessPolicy) == 0 {
				item.AccessPolicy = json.RawMessage(`{"mode":"space","allowedUserIds":[]}`)
			}
			var access workflowv2.AgentAccessPolicy
			if json.Unmarshal(item.AccessPolicy, &access) != nil || !validAgentAccess(access) {
				return ErrSpaceInvalid
			}
			if item.Icon == "" {
				item.Icon = "bot"
			}
			if item.Status == "" {
				item.Status = "draft"
			}
			if item.RuntimeKind == "" {
				item.RuntimeKind = "cloud"
			}
			if item.Version == 0 {
				item.CreatorUserID = userID
				return tx.QueryRowContext(ctx, `INSERT INTO space_agents(id,space_id,creator_user_id,name,description,icon,instructions,enabled,status,runtime_kind,schedules_enabled,active_workflow_version_id,access_policy,updated_by_user_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULL,$12,$3) RETURNING version,created_at,updated_at`, item.ID, item.SpaceID, userID, item.Name, item.Description, item.Icon, item.Instructions, item.Enabled, item.Status, item.RuntimeKind, item.SchedulesEnabled, item.AccessPolicy).Scan(&item.Version, &item.CreatedAt, &item.UpdatedAt)
			}
			var creatorID string
			if err := tx.QueryRowContext(ctx, `SELECT creator_user_id FROM space_agents WHERE id=$1 AND space_id=$2`, item.ID, item.SpaceID).Scan(&creatorID); err != nil {
				return err
			}
			if creatorID != userID {
				return ErrSpaceForbidden
			}
			result, err := tx.ExecContext(ctx, `UPDATE space_agents SET name=$1,description=$2,icon=$3,instructions=$4,enabled=$5,status=$6,schedules_enabled=$7,access_policy=$8,updated_by_user_id=$9,version=version+1,updated_at=NOW() WHERE id=$10 AND space_id=$11 AND version=$12`, item.Name, item.Description, item.Icon, item.Instructions, item.Enabled, item.Status, item.SchedulesEnabled, item.AccessPolicy, userID, item.ID, item.SpaceID, item.Version)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return ErrSpaceConflict
			}
			item.Version++
			return tx.QueryRowContext(ctx, `SELECT creator_user_id,runtime_kind,COALESCE(active_workflow_version_id,''),access_policy,created_at,updated_at FROM space_agents WHERE id=$1`, item.ID).Scan(&item.CreatorUserID, &item.RuntimeKind, &item.ActiveWorkflowVersionID, &item.AccessPolicy, &item.CreatedAt, &item.UpdatedAt)
		}
		if validateWorkflowV2Tx(ctx, tx, item.SpaceID, item.Definition) != nil {
			return ErrSpaceInvalid
		}
		if item.Version == 0 {
			item.CreatorUserID = userID
			item.StableIdentifier = "space." + item.SpaceID + ".workflow." + item.ID
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_workflows(id,space_id,creator_user_id,name,description,definition,enabled,schedules_enabled,stable_identifier,author_name) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'Misty member') RETURNING version,created_at,updated_at`, item.ID, item.SpaceID, userID, item.Name, item.Description, item.Definition, item.Enabled, item.SchedulesEnabled, item.StableIdentifier).Scan(&item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			return nil
		}
		var creatorID string
		if err := tx.QueryRowContext(ctx, `SELECT creator_user_id FROM space_workflows WHERE id=$1 AND space_id=$2`, item.ID, item.SpaceID).Scan(&creatorID); err != nil {
			return err
		}
		if creatorID != userID {
			return ErrSpaceForbidden
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_workflows SET name=$1,description=$2,definition=$3,enabled=$4,schedules_enabled=$5,version=version+1,updated_at=NOW() WHERE id=$6 AND space_id=$7 AND version=$8`, item.Name, item.Description, item.Definition, item.Enabled, item.SchedulesEnabled, item.ID, item.SpaceID, item.Version)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceConflict
		}
		item.Version++
		if err := tx.QueryRowContext(ctx, `SELECT creator_user_id,stable_identifier,created_at,updated_at FROM space_workflows WHERE id=$1`, item.ID).Scan(&item.CreatorUserID, &item.StableIdentifier, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, e := recordSpaceEventTx(ctx, tx, item.SpaceID, userID, item.Kind+".updated", item.ID, item)
		return e
	})
	if item.Kind == "agent" && item.ActiveWorkflowVersionID != "" {
		item.ActiveWorkflow, _ = db.WorkflowVersion(ctx, userID, item.SpaceID, item.ActiveWorkflowVersionID)
	}
	return &item, nil
}

func (db *Database) SpaceStudioResourceByID(ctx context.Context, userID, spaceID, kind, id string) (*SpaceStudioResource, error) {
	out := &SpaceStudioResource{Kind: kind}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioView); err != nil {
			return err
		}
		if kind == "agent" {
			if err := tx.QueryRowContext(ctx, `SELECT id,space_id,creator_user_id,name,description,icon,instructions,enabled,status,runtime_kind,version,schedules_enabled,COALESCE(active_workflow_version_id,''),access_policy,created_at,updated_at FROM space_agents WHERE id=$1 AND space_id=$2`, id, spaceID).Scan(&out.ID, &out.SpaceID, &out.CreatorUserID, &out.Name, &out.Description, &out.Icon, &out.Instructions, &out.Enabled, &out.Status, &out.RuntimeKind, &out.Version, &out.SchedulesEnabled, &out.ActiveWorkflowVersionID, &out.AccessPolicy, &out.CreatedAt, &out.UpdatedAt); err != nil {
				return err
			}
			if out.ActiveWorkflowVersionID != "" {
				workflow, err := loadWorkflowVersionTx(ctx, tx, out.ActiveWorkflowVersionID)
				out.ActiveWorkflow = workflow
				return err
			}
			return nil
		}
		if kind != "workflow" {
			return ErrSpaceInvalid
		}
		if err := tx.QueryRowContext(ctx, `SELECT id,space_id,creator_user_id,name,description,definition,enabled,version,schedules_enabled,stable_identifier,created_at,updated_at FROM space_workflows WHERE id=$1 AND space_id=$2`, id, spaceID).Scan(&out.ID, &out.SpaceID, &out.CreatorUserID, &out.Name, &out.Description, &out.Definition, &out.Enabled, &out.Version, &out.SchedulesEnabled, &out.StableIdentifier, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		workflow, err := loadLatestWorkflowVersionTx(ctx, tx, out.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		out.ActiveWorkflow = workflow
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) CreateSpaceRun(ctx context.Context, userID, spaceID, kind, resourceID, triggerKind, capabilityID string, input json.RawMessage) (*SpaceRun, error) {
	if kind == "agent" {
		sourceType := "direct"
		if triggerKind == "mention" {
			sourceType = "group_mention"
		}
		if triggerKind == "schedule" {
			sourceType = "schedule"
		}
		if triggerKind == "test" {
			sourceType = "studio_test"
		}
		return db.CreateAgentRun(ctx, AgentRunRequest{RequestingMemberID: userID, SpaceID: spaceID, AgentID: resourceID, SourceType: sourceType, CapabilityID: capabilityID, Input: input, TriggerKind: triggerKind})
	}
	// Workflows are immutable plans attached to an Agent. They never execute as
	// standalone principals, including Studio tests.
	return nil, ErrSpaceInvalid
}

func (db *Database) FinishSpaceRun(ctx context.Context, runID, state string, result json.RawMessage, errorCode string) (*SpaceRun, error) {
	if state != "completed" && state != "completed_with_errors" && state != "failed" && state != "canceled" && state != "rejected" {
		return nil, ErrSpaceInvalid
	}
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		progress := 0
		if state == "completed" || state == "completed_with_errors" {
			progress = 100
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state=$1,result=$2,outputs=$2,error_code=NULLIF($3,''),error_message=CASE WHEN $1='failed' THEN COALESCE(($2::jsonb)->>'message','Execution failed') ELSE NULL END,progress=$4,completed_at=NOW(),updated_at=NOW()
			WHERE id=$5 AND state IN ('queued','running','cooldown') RETURNING `+spaceRunColumns, state, result, errorCode, progress, runID), out); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		eventID, err := recordSpaceEventTx(ctx, tx, out.SpaceID, out.InitiatedByUserID, out.ResourceKind+".run."+state, out.ID, out)
		if err != nil {
			return err
		}
		if out.SourceType == "schedule" || out.TriggerKind != "manual" && out.TriggerKind != RunSourceAgentConsole && out.TriggerKind != "mention" && out.TriggerKind != "direct" {
			payload := mustJSON(map[string]any{"run_id": out.ID, "agent_id": out.AgentID, "state": out.State, "outputs": out.Outputs, "error_code": out.ErrorCode})
			_, err = tx.ExecContext(ctx, `INSERT INTO space_inbox_items(user_id,space_id,kind,event_id,payload) VALUES($1,$2,'workflow',$3,$4)`, out.RequestingMemberID, out.SpaceID, eventID, payload)
		}
		return err
	})
	return out, err
}

func (db *Database) DeleteSpaceStudioResource(ctx context.Context, userID, spaceID, kind, id string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioManage); err != nil {
			return err
		}
		table := "space_agents"
		if kind == "workflow" {
			table = "space_workflows"
		} else if kind != "agent" {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_run_approvals SET state='canceled',decided_by_user_id=$1,decided_at=NOW() WHERE state='pending' AND run_id IN (SELECT id FROM space_runs WHERE space_id=$2 AND resource_kind=$3 AND resource_id=$4 AND state IN ('queued','running','awaiting_approval','cooldown'))`, userID, spaceID, kind, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW() WHERE space_id=$1 AND resource_kind=$2 AND resource_id=$3 AND state IN ('queued','running','awaiting_approval','cooldown')`, spaceID, kind, id); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=$1 AND space_id=$2 AND creator_user_id=$3`, id, spaceID, userID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, kind+".deleted", id, map[string]any{})
		return err
	})
}

func (db *Database) SpaceAgentPrompt(ctx context.Context, userID, spaceID, agentID string) (string, string, error) {
	var name, instructions string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT name,instructions FROM space_agents WHERE id=$1 AND space_id=$2 AND enabled`, agentID, spaceID).Scan(&name, &instructions)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrSpaceNotFound
	}
	return name, instructions, err
}

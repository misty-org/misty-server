package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) SaveSpaceStudioResource(ctx context.Context, userID string, item SpaceStudioResource) (*SpaceStudioResource, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.Version == 0 {
		item.ID = item.Kind + "_" + uuid.NewString()
	} else if item.ID == "" {
		item.ID = item.Kind + "_" + uuid.NewString()
	}
	if len([]rune(item.Name)) < 1 || len([]rune(item.Name)) > 80 || item.Kind != "workflow" {
		return nil, ErrSpaceInvalid
	}
	if len(item.Definition) == 0 {
		item.Definition = json.RawMessage(`{}`)
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, item.SpaceID, PermissionStudioManage); err != nil {
			return err
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
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state=$1,result=$2,outputs=$2,error_code=NULLIF($3,''),error_message=CASE WHEN $1 IN ('failed','completed_with_errors') THEN COALESCE(($2::jsonb)->>'message','Execution failed') ELSE NULL END,progress=$4,completed_at=NOW(),updated_at=NOW()
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
		table := "space_workflows"
		if kind != "workflow" {
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

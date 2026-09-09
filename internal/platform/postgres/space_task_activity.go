package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

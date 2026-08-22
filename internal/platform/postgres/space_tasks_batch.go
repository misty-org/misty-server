package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CreateSpaceTaskBatch commits a reviewed AI task set atomically. Task IDs are
// supplied by the artifact, making a replay return the same rows rather than
// creating duplicates.
func (db *Database) CreateSpaceTaskBatch(ctx context.Context, actorUserID, spaceID string, items []SpaceTask) ([]SpaceTask, error) {
	if len(items) < 1 || len(items) > 25 {
		return nil, ErrSpaceInvalid
	}
	for index := range items {
		items[index].SpaceID = spaceID
		if items[index].ID == "" {
			return nil, ErrSpaceInvalid
		}
		if items[index].Status == "" {
			items[index].Status = "todo"
		}
		if items[index].CreatedByUserID == "" {
			items[index].CreatedByUserID = actorUserID
		}
		items[index].AudienceKind = SpaceAudienceSpace
		items[index].AudienceConversationID = ""
		if err := TestingValidateSpaceTask(&items[index]); err != nil {
			return nil, err
		}
	}
	created := make([]SpaceTask, 0, len(items))
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, actorUserID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-task-rank:"+spaceID+":todo"); err != nil {
			return err
		}
		for _, item := range items {
			if err := validateTaskSourceRefsTx(ctx, tx, actorUserID, spaceID, item.ID, item.SourceRefs); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_tasks WHERE id=$1)`, item.ID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				out := SpaceTask{}
				if err := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE id=$1 AND space_id=$2 AND archived_at IS NULL`, item.ID, spaceID), &out); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return ErrSpaceConflict
					}
					return err
				}
				created = append(created, out)
				continue
			}
			if err := tx.QueryRowContext(ctx, `INSERT INTO space_task_counters(space_id,last_number) VALUES($1,1)
				ON CONFLICT(space_id) DO UPDATE SET last_number=space_task_counters.last_number+1 RETURNING last_number`, spaceID).Scan(&item.TaskNumber); err != nil {
				return err
			}
			item.TaskKey = fmt.Sprintf("MST-%d", item.TaskNumber)
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0)+1024 FROM space_tasks WHERE space_id=$1 AND status='todo' AND archived_at IS NULL`, spaceID).Scan(&item.Rank); err != nil {
				return err
			}
			out := SpaceTask{}
			query := `INSERT INTO space_tasks(id,space_id,task_number,task_key,title,notes,status,priority,rank,due_timezone,source_refs,created_by_user_id,audience_kind)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'space') RETURNING ` + spaceTaskColumns
			if err := scanSpaceTask(tx.QueryRowContext(ctx, query, item.ID, spaceID, item.TaskNumber, item.TaskKey, item.Title, item.Notes, item.Status, item.Priority, item.Rank, item.DueTimezone, item.SourceRefs, actorUserID), &out); err != nil {
				return err
			}
			if _, err := recordSpaceEventTx(ctx, tx, spaceID, actorUserID, "task.created", item.ID, map[string]any{"task": &out, "source": "ai_artifact"}); err != nil {
				return err
			}
			created = append(created, out)
		}
		return nil
	})
	return created, err
}

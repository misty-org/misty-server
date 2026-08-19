package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

func TestingDecodeTaskCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 || offset > 1_000_000 {
		return 0, ErrSpaceInvalid
	}
	return offset, nil
}

func (db *Database) CreateSpaceTask(ctx context.Context, actorUserID string, item SpaceTask) (*SpaceTask, error) {
	if item.ID == "" {
		item.ID = "task_" + uuid.NewString()
	}
	if item.Status == "" {
		item.Status = "todo"
	}
	if item.CreatedByUserID == "" && item.CreatedByAgentID == "" {
		item.CreatedByUserID = actorUserID
	}
	audience, audienceErr := NormalizeResourceAudience(item.AudienceKind, item.AudienceConversationID)
	if audienceErr != nil {
		return nil, audienceErr
	}
	item.AudienceKind, item.AudienceConversationID = audience.Kind, audience.ConversationID
	if item.AudienceKind == SpaceAudienceConversation && item.AudienceCreatorUserID == "" {
		item.AudienceCreatorUserID = actorUserID
	}
	if err := TestingValidateSpaceTask(&item); err != nil {
		return nil, err
	}
	out := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, actorUserID, item.SpaceID, PermissionTasksManage); err != nil {
			return err
		}
		if err := validateTaskSourceRefsTx(ctx, tx, actorUserID, item.SpaceID, item.ID, item.SourceRefs); err != nil {
			return err
		}
		if err := validateResourceAudienceTx(ctx, tx, actorUserID, item.SpaceID, audience); err != nil {
			return err
		}
		if item.AssigneeUserID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, item.SpaceID, item.AssigneeUserID); err != nil {
				return ErrSpaceInvalid
			}
		}
		if item.AssigneeAgentID != "" {
			if _, err := activePersonalAgentMembershipTx(ctx, tx, actorUserID, item.SpaceID, item.AssigneeAgentID); err != nil {
				return ErrSpaceInvalid
			}
			if item.Status == "todo" {
				item.Status = "in_progress"
			}
		}
		if item.CreatedByAgentID != "" {
			var allowed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
				SELECT 1 FROM space_agents WHERE id=$1 AND space_id=$2
				UNION ALL SELECT 1 FROM personal_agents a JOIN space_members m ON m.user_id=a.owner_user_id AND m.space_id=$2 WHERE a.id=$1 AND a.enabled AND a.deleted_at IS NULL
			)`, item.CreatedByAgentID, item.SpaceID).Scan(&allowed); err != nil || !allowed {
				return ErrSpaceInvalid
			}
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-task-rank:"+item.SpaceID+":"+item.Status); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_task_counters(space_id,last_number) VALUES($1,1)
			ON CONFLICT(space_id) DO UPDATE SET last_number=space_task_counters.last_number+1 RETURNING last_number`, item.SpaceID).Scan(&item.TaskNumber); err != nil {
			return err
		}
		item.TaskKey = fmt.Sprintf("MST-%d", item.TaskNumber)
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0)+1024 FROM space_tasks WHERE space_id=$1 AND status=$2 AND archived_at IS NULL`, item.SpaceID, item.Status).Scan(&item.Rank); err != nil {
			return err
		}
		completed := item.Status == "done"
		query := `INSERT INTO space_tasks(id,space_id,task_number,task_key,title,notes,status,priority,rank,assignee_user_id,assignee_agent_id,due_at,due_timezone,source_refs,created_by_user_id,created_by_agent_id,source_run_id,audience_kind,audience_conversation_id,audience_creator_user_id,completed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),$12,$13,$14,NULLIF($15,''),NULLIF($16,''),NULLIF($17,''),$18,NULLIF($19,''),NULLIF($20,''),CASE WHEN $21 THEN NOW() END) RETURNING ` + spaceTaskColumns
		if err := scanSpaceTask(tx.QueryRowContext(ctx, query, item.ID, item.SpaceID, item.TaskNumber, item.TaskKey, item.Title, item.Notes, item.Status, item.Priority, item.Rank, item.AssigneeUserID, item.AssigneeAgentID, item.DueAt, item.DueTimezone, item.SourceRefs, item.CreatedByUserID, item.CreatedByAgentID, item.SourceRunID, item.AudienceKind, item.AudienceConversationID, item.AudienceCreatorUserID, completed), out); err != nil {
			return err
		}
		if item.AssigneeAgentID != "" {
			if _, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: item.SpaceID, TaskID: item.ID, ActorKind: "person", ActorUserID: actorUserID, Kind: "assigned", Message: "Assigned to Agent", Metadata: mustJSON(map[string]any{"agent_id": item.AssigneeAgentID, "task_version": out.Version})}); err != nil {
				return err
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, item.SpaceID, actorUserID, "task.created", item.ID, map[string]any{"task": out})
		return err
	})
	return out, err
}

// lockActiveSpaceTaskTx takes the row lock that establishes server-receipt
// order for concurrent writes to one task. Lock acquisition order is the
// authoritative ordering: whichever transaction acquires the lock last writes
// last and wins.
//
// An archived task is a tombstone. It reports not-found so a stale in-flight
// write cannot resurrect it.
func lockActiveSpaceTaskTx(ctx context.Context, tx *sql.Tx, spaceID, taskID string) error {
	var archivedAt sql.NullTime
	err := tx.QueryRowContext(ctx,
		`SELECT archived_at FROM space_tasks WHERE id=$1 AND space_id=$2 FOR UPDATE`,
		taskID, spaceID).Scan(&archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSpaceNotFound
	}
	if err != nil {
		return err
	}
	if archivedAt.Valid {
		return ErrSpaceNotFound
	}
	return nil
}

func (db *Database) UpdateSpaceTask(ctx context.Context, actorUserID string, item SpaceTask) (*SpaceTask, error) {
	if item.ID == "" || item.SpaceID == "" || item.Version < 1 {
		return nil, ErrSpaceInvalid
	}
	if err := TestingValidateSpaceTask(&item); err != nil {
		return nil, err
	}
	out := &SpaceTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, actorUserID, item.SpaceID, PermissionTasksManage); err != nil {
			return err
		}
		if err := validateTaskSourceRefsTx(ctx, tx, actorUserID, item.SpaceID, item.ID, item.SourceRefs); err != nil {
			return err
		}
		if item.AssigneeUserID != "" {
			if _, err := requireSpaceMemberTx(ctx, tx, item.SpaceID, item.AssigneeUserID); err != nil {
				return ErrSpaceInvalid
			}
		}
		if item.AssigneeAgentID != "" {
			if _, err := activePersonalAgentMembershipTx(ctx, tx, actorUserID, item.SpaceID, item.AssigneeAgentID); err != nil {
				return ErrSpaceInvalid
			}
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-task-rank:"+item.SpaceID+":"+item.Status); err != nil {
			return err
		}
		// Active tasks are last-write-wins: the row lock below, not the client's
		// submitted version, decides the order of concurrent writes. The
		// submitted version is still accepted for wire compatibility.
		if err := lockActiveSpaceTaskTx(ctx, tx, item.SpaceID, item.ID); err != nil {
			return err
		}
		var previousAgentID string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(assignee_agent_id,'') FROM space_tasks WHERE id=$1`, item.ID).Scan(&previousAgentID); err != nil {
			return err
		}
		assignmentChanged := previousAgentID != item.AssigneeAgentID
		if assignmentChanged && previousAgentID != "" {
			if _, err := tx.ExecContext(ctx, `WITH canceled AS (
				UPDATE space_runs SET state='canceled',runtime_phase='canceled',error_code='task_unassigned',
					canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
				WHERE source_task_id=$1 AND agent_id=$2 AND state IN ('queued','running','cooldown','awaiting_approval','awaiting_device')
				RETURNING id
			) UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW()
			WHERE run_id IN (SELECT id FROM canceled) AND state IN ('queued','leased','dispatched')`, item.ID, previousAgentID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='denied',decided_at=NOW()
				WHERE run_id IN (SELECT id FROM space_runs WHERE source_task_id=$1 AND agent_id=$2 AND state='canceled' AND error_code='task_unassigned') AND state='pending'`, item.ID, previousAgentID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE agent_run_contexts SET state='detached',updated_at=NOW()
				WHERE run_id IN (SELECT id FROM space_runs WHERE source_task_id=$1 AND agent_id=$2 AND state='canceled' AND error_code='task_unassigned') AND state='attached'`, item.ID, previousAgentID); err != nil {
				return err
			}
		}
		if assignmentChanged && item.AssigneeAgentID != "" && item.Status == "todo" {
			item.Status = "in_progress"
		}
		query := `UPDATE space_tasks SET title=$1,notes=$2,status=$3,priority=$4,assignee_user_id=NULLIF($5,''),assignee_agent_id=NULLIF($6,''),due_at=$7,due_timezone=$8,source_refs=$9,
			rank=CASE WHEN status<>$3 THEN (SELECT COALESCE(MAX(other.rank),0)+1024 FROM space_tasks other WHERE other.space_id=$11 AND other.status=$3 AND other.archived_at IS NULL) ELSE rank END,
			completed_at=CASE WHEN $3='done' THEN COALESCE(completed_at,NOW()) ELSE NULL END,version=version+1,updated_at=NOW()
			WHERE id=$10 AND space_id=$11 AND archived_at IS NULL RETURNING ` + spaceTaskColumns
		err := scanSpaceTask(tx.QueryRowContext(ctx, query, item.Title, item.Notes, item.Status, item.Priority, item.AssigneeUserID, item.AssigneeAgentID, item.DueAt, item.DueTimezone, item.SourceRefs, item.ID, item.SpaceID), out)
		if errors.Is(err, sql.ErrNoRows) {
			// The archived_at guard above means the row was archived between the
			// lock and the write. A tombstone must never be resurrected.
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if assignmentChanged && item.AssigneeAgentID != "" {
			if _, err := insertTaskActivityTx(ctx, tx, SpaceTaskActivity{SpaceID: item.SpaceID, TaskID: item.ID, ActorKind: "person", ActorUserID: actorUserID, Kind: "assigned", Message: "Assigned to Agent", Metadata: mustJSON(map[string]any{"agent_id": item.AssigneeAgentID, "task_version": out.Version})}); err != nil {
				return err
			}
		}
		_, err = recordSpaceEventTx(ctx, tx, item.SpaceID, actorUserID, "task.updated", item.ID, map[string]any{"task": out})
		return err
	})
	return out, err
}

func (db *Database) MoveSpaceTask(ctx context.Context, actorUserID, spaceID, taskID string, move SpaceTaskMove) (*SpaceTaskMoveResult, error) {
	if taskID == "" || move.Version < 1 || move.Status != "todo" && move.Status != "in_progress" && move.Status != "done" && move.Status != "canceled" {
		return nil, ErrSpaceInvalid
	}
	result := &SpaceTaskMoveResult{Reordered: []SpaceTask{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, actorUserID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space-task-rank:"+spaceID+":"+move.Status); err != nil {
			return err
		}
		// The row lock decides which of two concurrent moves is last, so the
		// client's submitted version is not part of the predicate.
		if err := lockActiveSpaceTaskTx(ctx, tx, spaceID, taskID); err != nil {
			return err
		}
		newRank, err := taskRankBefore(ctx, tx, spaceID, taskID, move.Status, move.BeforeTaskID)
		if err != nil {
			return err
		}
		if newRank == 0 {
			if err := rebalanceTaskColumn(ctx, tx, spaceID, move.Status, taskID); err != nil {
				return err
			}
			newRank, err = taskRankBefore(ctx, tx, spaceID, taskID, move.Status, move.BeforeTaskID)
			if err != nil || newRank == 0 {
				return ErrSpaceConflict
			}
		}
		completed := move.Status == "done"
		err = scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks SET status=$1,rank=$2,completed_at=CASE WHEN $3 THEN COALESCE(completed_at,NOW()) ELSE NULL END,version=version+1,updated_at=NOW() WHERE id=$4 AND space_id=$5 AND archived_at IS NULL RETURNING `+spaceTaskColumns, move.Status, newRank, completed, taskID, spaceID), &result.Task)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks WHERE space_id=$1 AND status=$2 AND archived_at IS NULL ORDER BY rank,id`, spaceID, move.Status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var task SpaceTask
			if err := scanSpaceTask(rows, &task); err != nil {
				return err
			}
			result.Reordered = append(result.Reordered, task)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, actorUserID, "task.moved", taskID, map[string]any{"task": result.Task})
		return err
	})
	return result, err
}

func taskRankBefore(ctx context.Context, tx *sql.Tx, spaceID, taskID, status, beforeTaskID string) (int64, error) {
	if beforeTaskID == "" {
		var maxRank int64
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0) FROM space_tasks WHERE space_id=$1 AND status=$2 AND id<>$3 AND archived_at IS NULL`, spaceID, status, taskID).Scan(&maxRank)
		return maxRank + 1024, err
	}
	var beforeRank int64
	if err := tx.QueryRowContext(ctx, `SELECT rank FROM space_tasks WHERE id=$1 AND space_id=$2 AND status=$3 AND id<>$4 AND archived_at IS NULL`, beforeTaskID, spaceID, status, taskID).Scan(&beforeRank); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrSpaceInvalid
		}
		return 0, err
	}
	var previousRank int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0) FROM space_tasks WHERE space_id=$1 AND status=$2 AND id<>$3 AND rank<$4 AND archived_at IS NULL`, spaceID, status, taskID, beforeRank).Scan(&previousRank); err != nil {
		return 0, err
	}
	if beforeRank-previousRank <= 1 {
		return 0, nil
	}
	return previousRank + (beforeRank-previousRank)/2, nil
}

func rebalanceTaskColumn(ctx context.Context, tx *sql.Tx, spaceID, status, movingTaskID string) error {
	_, err := tx.ExecContext(ctx, `WITH ranked AS (SELECT id,ROW_NUMBER() OVER (ORDER BY rank,id)*1024 AS next_rank FROM space_tasks WHERE space_id=$1 AND status=$2 AND id<>$3 AND archived_at IS NULL) UPDATE space_tasks task SET rank=ranked.next_rank FROM ranked WHERE task.id=ranked.id`, spaceID, status, movingTaskID)
	return err
}

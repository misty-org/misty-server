package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// taskRankStep spaces ranks in a board column, so a card usually lands between
// two others without the rest of the column being rewritten.
const taskRankStep = 1024

// SpaceTask is one card on a Space's board. Every member reads and changes it.
type SpaceTask struct {
	ID              string     `json:"id"`
	SpaceID         string     `json:"space_id"`
	TaskNumber      int64      `json:"task_number"`
	Title           string     `json:"title"`
	Notes           string     `json:"notes"`
	Status          string     `json:"status"`
	Priority        string     `json:"priority"`
	Rank            int64      `json:"rank"`
	AssigneeUserID  string     `json:"assignee_user_id,omitempty"`
	AssigneeAgentID string     `json:"assignee_agent_id,omitempty"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	CreatedByUserID string     `json:"created_by_user_id"`
	Version         int64      `json:"version"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// SpaceTaskInput is what a person or their agent sets on a task. A nil field
// is left as it is on an update; ClearDue removes the due date.
type SpaceTaskInput struct {
	Title           *string
	Notes           *string
	Status          *string
	Priority        *string
	AssigneeUserID  *string
	AssigneeAgentID *string
	DueAt           *time.Time
	ClearDue        bool
}

const spaceTaskColumns = `id,space_id,task_number,title,notes,status,priority,rank,
	COALESCE(assignee_user_id,''),assignee_agent_id,due_at,created_by_user_id,version,
	completed_at,archived_at,created_at,updated_at`

func scanSpaceTask(row interface{ Scan(...any) error }, out *SpaceTask) error {
	return row.Scan(
		&out.ID, &out.SpaceID, &out.TaskNumber, &out.Title, &out.Notes, &out.Status,
		&out.Priority, &out.Rank, &out.AssigneeUserID, &out.AssigneeAgentID, &out.DueAt,
		&out.CreatedByUserID, &out.Version, &out.CompletedAt, &out.ArchivedAt,
		&out.CreatedAt, &out.UpdatedAt,
	)
}

func validTaskStatus(status string) bool {
	switch status {
	case "todo", "in_progress", "done", "canceled":
		return true
	}
	return false
}

func validTaskPriority(priority string) bool {
	return priority == "high" || priority == "medium" || priority == "low"
}

// applyTaskInput validates the fields an input sets and writes them onto task.
func applyTaskInput(ctx context.Context, tx *sql.Tx, task *SpaceTask, input SpaceTaskInput) error {
	if input.Title != nil {
		task.Title = strings.TrimSpace(*input.Title)
	}
	if input.Notes != nil {
		task.Notes = *input.Notes
	}
	if input.Status != nil {
		task.Status = *input.Status
	}
	if input.Priority != nil {
		task.Priority = *input.Priority
	}
	if input.AssigneeUserID != nil {
		task.AssigneeUserID = strings.TrimSpace(*input.AssigneeUserID)
		if task.AssigneeUserID != "" {
			task.AssigneeAgentID = ""
		}
	}
	if input.AssigneeAgentID != nil {
		task.AssigneeAgentID = strings.TrimSpace(*input.AssigneeAgentID)
		if task.AssigneeAgentID != "" {
			task.AssigneeUserID = ""
		}
	}
	if input.ClearDue {
		task.DueAt = nil
	} else if input.DueAt != nil {
		due := input.DueAt.UTC()
		task.DueAt = &due
	}
	if task.Title == "" || utf8.RuneCountInString(task.Title) > 240 ||
		utf8.RuneCountInString(task.Notes) > 20000 ||
		utf8.RuneCountInString(task.AssigneeAgentID) > 200 ||
		!validTaskStatus(task.Status) || !validTaskPriority(task.Priority) {
		return ErrSpaceInvalid
	}
	if task.AssigneeUserID != "" {
		if _, err := requireSpaceMemberTx(ctx, tx, task.SpaceID, task.AssigneeUserID); err != nil {
			if errors.Is(err, ErrSpaceForbidden) {
				return ErrSpaceInvalid
			}
			return err
		}
	}
	return nil
}

func recordTaskEventTx(ctx context.Context, tx *sql.Tx, task *SpaceTask, actorUserID, eventType string) error {
	_, err := recordSpaceEventTx(ctx, tx, task.SpaceID, actorUserID, eventType, task.ID,
		map[string]any{"task_id": task.ID, "version": task.Version})
	return err
}

// SpaceTasks lists the tasks on a Space's board, column by column in board order.
func (db *Database) SpaceTasks(ctx context.Context, userID, spaceID string) ([]SpaceTask, error) {
	tasks := []SpaceTask{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks
			WHERE space_id=$1 AND archived_at IS NULL ORDER BY status,rank,id LIMIT 2000`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var task SpaceTask
			if err := scanSpaceTask(rows, &task); err != nil {
				return err
			}
			tasks = append(tasks, task)
		}
		return rows.Err()
	})
	return tasks, err
}

// CreateSpaceTask adds a task at the end of its column.
func (db *Database) CreateSpaceTask(ctx context.Context, userID, spaceID string, input SpaceTaskInput) (*SpaceTask, error) {
	task := &SpaceTask{
		ID: "task_" + uuid.NewString(), SpaceID: spaceID, Status: "todo", Priority: "medium",
		CreatedByUserID: userID,
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := applyTaskInput(ctx, tx, task, input); err != nil {
			return err
		}
		// One writer numbers and ranks a Space's tasks at a time.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "space_tasks:"+spaceID); err != nil {
			return err
		}
		err := scanSpaceTask(tx.QueryRowContext(ctx, `INSERT INTO space_tasks(
				id,space_id,task_number,title,notes,status,priority,rank,assignee_user_id,
				assignee_agent_id,due_at,created_by_user_id,completed_at)
			VALUES($1,$2,
				(SELECT COALESCE(MAX(task_number),0)+1 FROM space_tasks WHERE space_id=$2),
				$3,$4,$5,$6,
				(SELECT COALESCE(MAX(rank),0)+$11 FROM space_tasks WHERE space_id=$2 AND status=$5 AND archived_at IS NULL),
				NULLIF($7,''),$8,$9,$10,CASE WHEN $5='done' THEN NOW() END)
			RETURNING `+spaceTaskColumns,
			task.ID, spaceID, task.Title, task.Notes, task.Status, task.Priority,
			task.AssigneeUserID, task.AssigneeAgentID, task.DueAt, userID, taskRankStep), task)
		if err != nil {
			return err
		}
		return recordTaskEventTx(ctx, tx, task, userID, "task.created")
	})
	if err != nil {
		return nil, err
	}
	return task, nil
}

func lockSpaceTaskTx(ctx context.Context, tx *sql.Tx, spaceID, taskID string) (*SpaceTask, error) {
	task := &SpaceTask{}
	err := scanSpaceTask(tx.QueryRowContext(ctx, `SELECT `+spaceTaskColumns+` FROM space_tasks
		WHERE id=$1 AND space_id=$2 AND archived_at IS NULL FOR UPDATE`, taskID, spaceID), task)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return task, err
}

// UpdateSpaceTask changes the fields the input sets. The last write wins: two
// people editing one task are rare, and losing a title edit beats a conflict.
func (db *Database) UpdateSpaceTask(ctx context.Context, userID, spaceID, taskID string, input SpaceTaskInput) (*SpaceTask, error) {
	var task *SpaceTask
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var err error
		if task, err = lockSpaceTaskTx(ctx, tx, spaceID, taskID); err != nil {
			return err
		}
		previous := task.Status
		if err := applyTaskInput(ctx, tx, task, input); err != nil {
			return err
		}
		if task.Status != previous {
			// A task that changes column through an edit goes to the end of the new one.
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "space_tasks:"+spaceID); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0)+$3 FROM space_tasks
				WHERE space_id=$1 AND status=$2 AND archived_at IS NULL`, spaceID, task.Status, taskRankStep).Scan(&task.Rank); err != nil {
				return err
			}
		}
		if err := scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks SET
				title=$3,notes=$4,status=$5,priority=$6,rank=$7,assignee_user_id=NULLIF($8,''),
				assignee_agent_id=$9,due_at=$10,
				completed_at=CASE WHEN $5='done' THEN COALESCE(completed_at,NOW()) END,
				version=version+1,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 RETURNING `+spaceTaskColumns,
			taskID, spaceID, task.Title, task.Notes, task.Status, task.Priority, task.Rank,
			task.AssigneeUserID, task.AssigneeAgentID, task.DueAt), task); err != nil {
			return err
		}
		return recordTaskEventTx(ctx, tx, task, userID, "task.updated")
	})
	if err != nil {
		return nil, err
	}
	return task, nil
}

// MoveSpaceTask puts a task in a column, before beforeTaskID or at the end.
// It returns the moved task and every task whose rank changed with it.
func (db *Database) MoveSpaceTask(ctx context.Context, userID, spaceID, taskID, status, beforeTaskID string) (*SpaceTask, []SpaceTask, error) {
	if !validTaskStatus(status) || beforeTaskID == taskID {
		return nil, nil, ErrSpaceInvalid
	}
	var moved *SpaceTask
	reordered := []SpaceTask{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "space_tasks:"+spaceID); err != nil {
			return err
		}
		if _, err := lockSpaceTaskTx(ctx, tx, spaceID, taskID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,rank FROM space_tasks
			WHERE space_id=$1 AND status=$2 AND archived_at IS NULL AND id<>$3
			ORDER BY rank,id FOR UPDATE`, spaceID, status, taskID)
		if err != nil {
			return err
		}
		type slot struct {
			id   string
			rank int64
		}
		column := []slot{}
		for rows.Next() {
			var item slot
			if err := rows.Scan(&item.id, &item.rank); err != nil {
				rows.Close()
				return err
			}
			column = append(column, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		index := len(column)
		if beforeTaskID != "" {
			index = -1
			for i, item := range column {
				if item.id == beforeTaskID {
					index = i
				}
			}
			if index < 0 {
				return ErrSpaceInvalid
			}
		}
		// Rank between the two neighbors when there is room; otherwise respace the column.
		var low, high int64
		if index > 0 {
			low = column[index-1].rank
		}
		high = low + 2*taskRankStep
		if index < len(column) {
			high = column[index].rank
		}
		rank := low + (high-low)/2
		if rank <= low || rank >= high {
			column = append(column[:index], append([]slot{{id: taskID}}, column[index:]...)...)
			for i, item := range column {
				next := int64(i+1) * taskRankStep
				if item.id == taskID {
					rank = next
					continue
				}
				if item.rank == next {
					continue
				}
				var changed SpaceTask
				if err := scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks SET rank=$2,version=version+1,updated_at=NOW()
					WHERE id=$1 RETURNING `+spaceTaskColumns, item.id, next), &changed); err != nil {
					return err
				}
				reordered = append(reordered, changed)
			}
		}
		moved = &SpaceTask{}
		if err := scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks SET status=$3,rank=$4,
				completed_at=CASE WHEN $3='done' THEN COALESCE(completed_at,NOW()) END,
				version=version+1,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 RETURNING `+spaceTaskColumns, taskID, spaceID, status, rank), moved); err != nil {
			return err
		}
		reordered = append(reordered, *moved)
		return recordTaskEventTx(ctx, tx, moved, userID, "task.moved")
	})
	if err != nil {
		return nil, nil, err
	}
	return moved, reordered, nil
}

// ArchiveSpaceTask takes a task off the board. Archiving twice is not an error.
func (db *Database) ArchiveSpaceTask(ctx context.Context, userID, spaceID, taskID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		task := &SpaceTask{}
		err := scanSpaceTask(tx.QueryRowContext(ctx, `UPDATE space_tasks SET archived_at=NOW(),version=version+1,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 AND archived_at IS NULL RETURNING `+spaceTaskColumns, taskID, spaceID), task)
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_tasks WHERE id=$1 AND space_id=$2)`, taskID, spaceID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrSpaceNotFound
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive task: %w", err)
		}
		return recordTaskEventTx(ctx, tx, task, userID, "task.archived")
	})
}

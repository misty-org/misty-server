package db

import (
	"context"
	"database/sql"
)

func loadSpaceRoadmapGoalTasksTx(ctx context.Context, tx *sql.Tx, roadmapID string, goals []SpaceRoadmapGoal) error {
	if len(goals) == 0 {
		return nil
	}
	goalIndex := map[string]int{}
	for index := range goals {
		goalIndex[goals[index].ID] = index
	}
	// spaceTaskColumns is unqualified and space_roadmap_goal_tasks also has a
	// space_id, so joining the two tables directly made that column ambiguous
	// and failed the whole roadmap snapshot. The derived table exposes only the
	// three columns this needs, leaving every task column unambiguous.
	rows, err := tx.QueryContext(ctx, `SELECT gt.goal_id,`+spaceTaskColumns+` FROM space_tasks
		JOIN (SELECT goal_id,task_id,added_at FROM space_roadmap_goal_tasks WHERE roadmap_id=$1) gt
			ON space_tasks.id=gt.task_id
		ORDER BY gt.added_at,gt.task_id`, roadmapID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var goalID string
		var task SpaceTask
		// Scanning through scanSpaceTask keeps this in step with
		// spaceTaskColumns; the hand-written list it replaced had fallen three
		// columns behind.
		if err := scanSpaceTask(goalTaskScanner{rows: rows, goalID: &goalID}, &task); err != nil {
			return err
		}
		if index, ok := goalIndex[goalID]; ok {
			goals[index].Tasks = append(goals[index].Tasks, task)
		}
	}
	return rows.Err()
}

// goalTaskScanner lets scanSpaceTask read a row that carries the owning goal id
// ahead of the task's own columns.
type goalTaskScanner struct {
	rows   *sql.Rows
	goalID *string
}

func (scanner goalTaskScanner) Scan(dest ...any) error {
	return scanner.rows.Scan(append([]any{scanner.goalID}, dest...)...)
}

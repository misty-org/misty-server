package db

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func bumpSpaceRoadmapVersionTx(ctx context.Context, tx *sql.Tx, spaceID, roadmapID string, expected int64) (int64, error) {
	if expected < 1 {
		return 0, ErrSpaceInvalid
	}
	var version int64
	err := tx.QueryRowContext(ctx, `UPDATE space_roadmaps SET graph_version=graph_version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2 AND archived_at IS NULL AND graph_version=$3 RETURNING graph_version`, roadmapID, spaceID, expected).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_roadmaps WHERE id=$1 AND space_id=$2 AND archived_at IS NULL)`, roadmapID, spaceID).Scan(&exists); queryErr != nil {
			return 0, queryErr
		}
		if exists {
			return 0, ErrSpaceConflict
		}
		return 0, ErrSpaceNotFound
	}
	return version, err
}

func roadmapMutationTx(ctx context.Context, db *Database, userID, spaceID, roadmapID string, expected int64, mutate func(*sql.Tx, int64) error) (int64, error) {
	var version int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		visible, err := resourceEntityAudienceVisibleTx(ctx, tx, userID, spaceID, "space_roadmaps", roadmapID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrSpaceNotFound
		}
		version, err = bumpSpaceRoadmapVersionTx(ctx, tx, spaceID, roadmapID, expected)
		if err != nil {
			return err
		}
		return mutate(tx, version)
	})
	return version, err
}

func validRoadmapText(value string, maximum int) bool {
	length := len([]rune(strings.TrimSpace(value)))
	return length > 0 && length <= maximum
}

func validRoadmapPosition(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 10000000
}

func (db *Database) UpdateSpaceRoadmap(ctx context.Context, userID, spaceID, roadmapID, name, description string, expected int64) (*SpaceRoadmap, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if !validRoadmapText(name, 160) || len([]rune(description)) > 5000 {
		return nil, ErrSpaceInvalid
	}
	item := &SpaceRoadmap{}
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if err := scanSpaceRoadmap(tx.QueryRowContext(ctx, `UPDATE space_roadmaps SET name=$1,description=$2 WHERE id=$3 AND space_id=$4 RETURNING `+roadmapColumns, name, description, roadmapID, spaceID), item); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.updated", roadmapID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
	item.GraphVersion = version
	return item, err
}

func (db *Database) ArchiveSpaceRoadmap(ctx context.Context, userID, spaceID, roadmapID string, expected int64) (int64, error) {
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if _, err := tx.ExecContext(ctx, `UPDATE space_roadmaps SET archived_at=NOW() WHERE id=$1 AND space_id=$2`, roadmapID, spaceID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.archived", roadmapID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

func (db *Database) CreateSpaceRoadmapMilestone(ctx context.Context, userID, spaceID, roadmapID string, item SpaceRoadmapMilestone, expected int64) (*SpaceRoadmapMilestone, int64, error) {
	item.Title, item.Description = strings.TrimSpace(item.Title), strings.TrimSpace(item.Description)
	if !validRoadmapText(item.Title, 200) || len([]rune(item.Description)) > 10000 || !validRoadmapPosition(item.PositionX) || !validRoadmapPosition(item.PositionY) {
		return nil, 0, ErrSpaceInvalid
	}
	if item.Width == 0 {
		item.Width = 440
	}
	if item.Height == 0 {
		item.Height = 360
	}
	if item.Width < 280 || item.Width > 2400 || item.Height < 220 || item.Height > 2400 {
		return nil, 0, ErrSpaceInvalid
	}
	item.ID, item.SpaceID, item.RoadmapID = "milestone_"+uuid.NewString(), spaceID, roadmapID
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0)+1024 FROM space_roadmap_milestones WHERE roadmap_id=$1 AND archived_at IS NULL`, roadmapID).Scan(&item.Rank); err != nil {
			return err
		}
		if err := scanSpaceRoadmapMilestone(tx.QueryRowContext(ctx, `INSERT INTO space_roadmap_milestones(id,space_id,roadmap_id,title,description,target_date,rank,position_x,position_y,width,height) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+roadmapMilestoneColumns, item.ID, spaceID, roadmapID, item.Title, item.Description, item.TargetDate, item.Rank, item.PositionX, item.PositionY, item.Width, item.Height), &item); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.milestone.created", item.ID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
	return &item, version, err
}

func (db *Database) UpdateSpaceRoadmapMilestone(ctx context.Context, userID, spaceID, roadmapID, milestoneID string, item SpaceRoadmapMilestone, expected int64) (*SpaceRoadmapMilestone, int64, error) {
	item.Title, item.Description = strings.TrimSpace(item.Title), strings.TrimSpace(item.Description)
	if !validRoadmapText(item.Title, 200) || len([]rune(item.Description)) > 10000 {
		return nil, 0, ErrSpaceInvalid
	}
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		err := scanSpaceRoadmapMilestone(tx.QueryRowContext(ctx, `UPDATE space_roadmap_milestones SET title=$1,description=$2,target_date=$3,rank=CASE WHEN $4>0 THEN $4 ELSE rank END,version=version+1,updated_at=NOW() WHERE id=$5 AND roadmap_id=$6 AND space_id=$7 AND archived_at IS NULL RETURNING `+roadmapMilestoneColumns, item.Title, item.Description, item.TargetDate, item.Rank, milestoneID, roadmapID, spaceID), &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.milestone.updated", milestoneID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
	return &item, version, err
}

func (db *Database) ArchiveSpaceRoadmapMilestone(ctx context.Context, userID, spaceID, roadmapID, milestoneID string, expected int64) (int64, error) {
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_roadmap_milestones SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, milestoneID, roadmapID, spaceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrSpaceNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_roadmap_goals SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE milestone_id=$1 AND archived_at IS NULL`, milestoneID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_roadmap_nodes SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE milestone_id=$1 AND archived_at IS NULL`, milestoneID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_roadmap_edges WHERE roadmap_id=$1 AND ((source_kind='milestone' AND source_id=$2) OR (target_kind='milestone' AND target_id=$2) OR (source_kind='node' AND source_id IN (SELECT id FROM space_roadmap_nodes WHERE milestone_id=$2)) OR (target_kind='node' AND target_id IN (SELECT id FROM space_roadmap_nodes WHERE milestone_id=$2)))`, roadmapID, milestoneID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.milestone.archived", milestoneID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

func (db *Database) CreateSpaceRoadmapGoal(ctx context.Context, userID, spaceID, roadmapID string, item SpaceRoadmapGoal, expected int64) (*SpaceRoadmapGoal, int64, error) {
	item.Title, item.Description = strings.TrimSpace(item.Title), strings.TrimSpace(item.Description)
	if !validRoadmapText(item.Title, 240) || len([]rune(item.Description)) > 20000 || item.MilestoneID == "" || !validRoadmapPosition(item.PositionX) || !validRoadmapPosition(item.PositionY) {
		return nil, 0, ErrSpaceInvalid
	}
	item.ID, item.SpaceID, item.RoadmapID, item.Tasks = "goal_"+uuid.NewString(), spaceID, roadmapID, []SpaceTask{}
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rank),0)+1024 FROM space_roadmap_goals WHERE milestone_id=$1 AND archived_at IS NULL`, item.MilestoneID).Scan(&item.Rank); err != nil {
			return err
		}
		err := scanSpaceRoadmapGoal(tx.QueryRowContext(ctx, `INSERT INTO space_roadmap_goals(id,space_id,roadmap_id,milestone_id,title,description,target_date,rank,position_x,position_y) SELECT $1,$2,$3,m.id,$4,$5,$6,$7,$8,$9 FROM space_roadmap_milestones m WHERE m.id=$10 AND m.roadmap_id=$3 AND m.space_id=$2 AND m.archived_at IS NULL RETURNING `+roadmapGoalColumns, item.ID, spaceID, roadmapID, item.Title, item.Description, item.TargetDate, item.Rank, item.PositionX, item.PositionY, item.MilestoneID), &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.goal.created", item.ID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
	return &item, version, err
}

func (db *Database) UpdateSpaceRoadmapGoal(ctx context.Context, userID, spaceID, roadmapID, goalID string, item SpaceRoadmapGoal, completeManually *bool, expected int64) (*SpaceRoadmapGoal, int64, error) {
	item.Title, item.Description = strings.TrimSpace(item.Title), strings.TrimSpace(item.Description)
	if !validRoadmapText(item.Title, 240) || len([]rune(item.Description)) > 20000 || item.MilestoneID == "" {
		return nil, 0, ErrSpaceInvalid
	}
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if completeManually != nil && *completeManually {
			var activeTasks int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_roadmap_goal_tasks gt JOIN space_tasks t ON t.id=gt.task_id WHERE gt.goal_id=$1 AND t.archived_at IS NULL AND t.status<>'canceled'`, goalID).Scan(&activeTasks); err != nil {
				return err
			}
			if activeTasks > 0 {
				return ErrSpaceInvalid
			}
		}
		manualAt, manualBy := item.ManualCompletedAt, item.ManualCompletedByUserID
		if completeManually != nil {
			if *completeManually {
				now := time.Now().UTC()
				manualAt, manualBy = &now, userID
			} else {
				manualAt, manualBy = nil, ""
			}
		}
		var milestoneExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_roadmap_milestones WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL)`, item.MilestoneID, roadmapID, spaceID).Scan(&milestoneExists); err != nil {
			return err
		}
		if !milestoneExists {
			return ErrSpaceNotFound
		}
		err := scanSpaceRoadmapGoal(tx.QueryRowContext(ctx, `UPDATE space_roadmap_goals SET milestone_id=$1,title=$2,description=$3,target_date=$4,rank=CASE WHEN $5>0 THEN $5 ELSE rank END,manual_completed_at=$6,manual_completed_by_user_id=NULLIF($7,''),version=version+1,updated_at=NOW() WHERE id=$8 AND roadmap_id=$9 AND space_id=$10 AND archived_at IS NULL RETURNING `+roadmapGoalColumns, item.MilestoneID, item.Title, item.Description, item.TargetDate, item.Rank, manualAt, manualBy, goalID, roadmapID, spaceID), &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.goal.updated", goalID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
	item.Tasks = []SpaceTask{}
	return &item, version, err
}

func (db *Database) ArchiveSpaceRoadmapGoal(ctx context.Context, userID, spaceID, roadmapID, goalID string, expected int64) (int64, error) {
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_roadmap_goals SET archived_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, goalID, roadmapID, spaceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.goal.archived", goalID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

func (db *Database) SaveSpaceRoadmapEdge(ctx context.Context, userID, spaceID, roadmapID string, item SpaceRoadmapEdge, expected int64) (*SpaceRoadmapEdge, int64, error) {
	item.EdgeType, item.Label = strings.TrimSpace(item.EdgeType), strings.TrimSpace(item.Label)
	if item.EdgeType == "dependency" {
		item.EdgeType = "depends_on"
	}
	if item.Source.ID == "" && item.SourceGoalID != "" {
		item.Source = SpaceRoadmapEdgeEndpoint{Kind: "goal", ID: item.SourceGoalID}
	}
	if item.Target.ID == "" && item.TargetGoalID != "" {
		item.Target = SpaceRoadmapEdgeEndpoint{Kind: "goal", ID: item.TargetGoalID}
	}
	if item.Source.ID == "" || item.Target.ID == "" || item.Source == item.Target || len([]rune(item.Label)) > 120 {
		return nil, 0, ErrSpaceInvalid
	}
	creating := item.ID == ""
	if creating {
		item.ID = "edge_" + uuid.NewString()
	}
	version, err := roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		if err := validateRoadmapEdgeTx(ctx, tx, spaceID, roadmapID, item); err != nil {
			return err
		}
		if causalRoadmapEdge(item.EdgeType) && item.Source.Kind == "goal" && item.Target.Kind == "goal" {
			cycle, err := roadmapDependencyWouldCycleTx(ctx, tx, roadmapID, item.ID, item.Source.ID, item.Target.ID)
			if err != nil {
				return err
			}
			if cycle {
				return ErrSpaceInvalid
			}
		}
		sourceGoalID, targetGoalID := "", ""
		if item.Source.Kind == "goal" {
			sourceGoalID = item.Source.ID
		}
		if item.Target.Kind == "goal" {
			targetGoalID = item.Target.ID
		}
		if creating {
			err := scanSpaceRoadmapEdge(tx.QueryRowContext(ctx, `INSERT INTO space_roadmap_edges(id,space_id,roadmap_id,source_goal_id,target_goal_id,source_kind,source_id,target_kind,target_id,edge_type,label) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9,$10,$11) RETURNING `+roadmapEdgeColumns, item.ID, spaceID, roadmapID, sourceGoalID, targetGoalID, item.Source.Kind, item.Source.ID, item.Target.Kind, item.Target.ID, item.EdgeType, item.Label), &item)
			if err != nil {
				return err
			}
		} else {
			err := scanSpaceRoadmapEdge(tx.QueryRowContext(ctx, `UPDATE space_roadmap_edges SET source_goal_id=NULLIF($1,''),target_goal_id=NULLIF($2,''),source_kind=$3,source_id=$4,target_kind=$5,target_id=$6,edge_type=$7,label=$8,version=version+1,updated_at=NOW() WHERE id=$9 AND roadmap_id=$10 AND space_id=$11 RETURNING `+roadmapEdgeColumns, sourceGoalID, targetGoalID, item.Source.Kind, item.Source.ID, item.Target.Kind, item.Target.ID, item.EdgeType, item.Label, item.ID, roadmapID, spaceID), &item)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceNotFound
			}
			if err != nil {
				return err
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.edge.updated", item.ID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
	return &item, version, err
}

func causalRoadmapEdge(edgeType string) bool {
	return edgeType == "depends_on" || edgeType == "dependency" || edgeType == "blocks" || edgeType == "enables"
}

func roadmapEndpointExistsTx(ctx context.Context, tx *sql.Tx, spaceID, roadmapID string, endpoint SpaceRoadmapEdgeEndpoint) (string, bool, error) {
	var kind string
	switch endpoint.Kind {
	case "milestone":
		err := tx.QueryRowContext(ctx, `SELECT 'milestone' FROM space_roadmap_milestones WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, endpoint.ID, roadmapID, spaceID).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return kind, err == nil, err
	case "goal":
		err := tx.QueryRowContext(ctx, `SELECT 'goal' FROM space_roadmap_goals WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, endpoint.ID, roadmapID, spaceID).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return kind, err == nil, err
	case "node":
		err := tx.QueryRowContext(ctx, `SELECT node_kind FROM space_roadmap_nodes WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL`, endpoint.ID, roadmapID, spaceID).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return kind, err == nil, err
	default:
		return "", false, nil
	}
}

func validateRoadmapEdgeTx(ctx context.Context, tx *sql.Tx, spaceID, roadmapID string, item SpaceRoadmapEdge) error {
	sourceType, sourceOK, err := roadmapEndpointExistsTx(ctx, tx, spaceID, roadmapID, item.Source)
	if err != nil {
		return err
	}
	targetType, targetOK, err := roadmapEndpointExistsTx(ctx, tx, spaceID, roadmapID, item.Target)
	if err != nil {
		return err
	}
	if !sourceOK || !targetOK {
		return ErrSpaceNotFound
	}
	valid := false
	switch item.EdgeType {
	case "depends_on":
		valid = item.Source.Kind == "goal" && item.Target.Kind == "goal"
	case "blocks":
		valid = (item.Source.Kind == "goal" || sourceType == "risk") && (item.Target.Kind == "goal" || item.Target.Kind == "milestone")
	case "enables":
		valid = (item.Source.Kind == "goal" || sourceType == "decision") && (item.Target.Kind == "goal" || item.Target.Kind == "milestone")
	case "contributes_to":
		valid = item.Source.Kind == "node" && (item.Target.Kind == "goal" || item.Target.Kind == "milestone")
	case "measures":
		valid = sourceType == "metric" && (item.Target.Kind == "goal" || item.Target.Kind == "milestone")
	case "documents":
		valid = sourceType == "note"
	case "related":
		valid = true
	}
	if !valid || targetType == "" {
		return ErrSpaceInvalid
	}
	return nil
}

func roadmapDependencyWouldCycleTx(ctx context.Context, tx *sql.Tx, roadmapID, excludedEdgeID, sourceID, targetID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_id,target_id FROM space_roadmap_edges WHERE roadmap_id=$1 AND source_kind='goal' AND target_kind='goal' AND edge_type IN ('dependency','depends_on','blocks','enables') AND id<>$2`, roadmapID, excludedEdgeID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	graph := map[string][]string{sourceID: {targetID}}
	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			return false, err
		}
		graph[source] = append(graph[source], target)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	seen, stack := map[string]bool{}, []string{targetID}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == sourceID {
			return true, nil
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		stack = append(stack, graph[current]...)
	}
	return false, nil
}

func (db *Database) ReplaceSpaceRoadmapGoalTasks(ctx context.Context, userID, spaceID, roadmapID, goalID string, taskIDs []string, expected int64) (int64, error) {
	sortedTaskIDs, valid := sortedUniqueStrings(taskIDs)
	if !valid || len(sortedTaskIDs) > 100 {
		return 0, ErrSpaceInvalid
	}
	taskIDs = sortedTaskIDs
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		var goalExists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_roadmap_goals WHERE id=$1 AND roadmap_id=$2 AND space_id=$3 AND archived_at IS NULL)`, goalID, roadmapID, spaceID).Scan(&goalExists); err != nil || !goalExists {
			if err != nil {
				return err
			}
			return ErrSpaceNotFound
		}
		if len(taskIDs) > 0 {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_tasks WHERE id=ANY($1) AND space_id=$2 AND archived_at IS NULL`, pq.Array(taskIDs), spaceID).Scan(&count); err != nil {
				return err
			}
			if count != len(taskIDs) {
				return ErrSpaceInvalid
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_roadmap_goal_tasks WHERE goal_id=$1`, goalID); err != nil {
			return err
		}
		for _, taskID := range taskIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_roadmap_goal_tasks(space_id,roadmap_id,goal_id,task_id,added_by_user_id) VALUES($1,$2,$3,$4,$5)`, spaceID, roadmapID, goalID, taskID, userID); err != nil {
				return err
			}
		}
		if len(taskIDs) > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE space_roadmap_goals SET manual_completed_at=NULL,manual_completed_by_user_id=NULL,version=version+1,updated_at=NOW() WHERE id=$1`, goalID); err != nil {
				return err
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.goal.tasks.updated", goalID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

func (db *Database) UpdateSpaceRoadmapLayout(ctx context.Context, userID, spaceID, roadmapID string, layout SpaceRoadmapLayout, expected int64) (int64, error) {
	if len(layout.Milestones)+len(layout.Goals)+len(layout.Nodes) > 1000 {
		return 0, ErrSpaceInvalid
	}
	for _, item := range layout.Milestones {
		if item.ID == "" || !validRoadmapPosition(item.PositionX) || !validRoadmapPosition(item.PositionY) || item.Width < 280 || item.Width > 2400 || item.Height < 220 || item.Height > 2400 {
			return 0, ErrSpaceInvalid
		}
	}
	for _, item := range layout.Goals {
		if item.ID == "" || !validRoadmapPosition(item.PositionX) || !validRoadmapPosition(item.PositionY) || item.MilestoneID == "" {
			return 0, ErrSpaceInvalid
		}
	}
	for _, item := range layout.Nodes {
		if item.ID == "" || !validRoadmapPosition(item.PositionX) || !validRoadmapPosition(item.PositionY) {
			return 0, ErrSpaceInvalid
		}
	}
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		for _, item := range layout.Milestones {
			result, err := tx.ExecContext(ctx, `UPDATE space_roadmap_milestones SET position_x=$1,position_y=$2,width=$3,height=$4,version=version+1,updated_at=NOW() WHERE id=$5 AND roadmap_id=$6 AND space_id=$7 AND archived_at IS NULL`, item.PositionX, item.PositionY, item.Width, item.Height, item.ID, roadmapID, spaceID)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrSpaceNotFound
			}
		}
		for _, item := range layout.Goals {
			result, err := tx.ExecContext(ctx, `UPDATE space_roadmap_goals g SET milestone_id=m.id,position_x=$1,position_y=$2,version=g.version+1,updated_at=NOW() FROM space_roadmap_milestones m WHERE g.id=$3 AND g.roadmap_id=$4 AND g.space_id=$5 AND g.archived_at IS NULL AND m.id=$6 AND m.roadmap_id=g.roadmap_id AND m.archived_at IS NULL`, item.PositionX, item.PositionY, item.ID, roadmapID, spaceID, item.MilestoneID)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrSpaceNotFound
			}
		}
		for _, item := range layout.Nodes {
			var result sql.Result
			var err error
			if item.MilestoneID == "" {
				result, err = tx.ExecContext(ctx, `UPDATE space_roadmap_nodes SET milestone_id=NULL,position_x=$1,position_y=$2,version=version+1,updated_at=NOW() WHERE id=$3 AND roadmap_id=$4 AND space_id=$5 AND archived_at IS NULL`, item.PositionX, item.PositionY, item.ID, roadmapID, spaceID)
			} else {
				result, err = tx.ExecContext(ctx, `UPDATE space_roadmap_nodes n SET milestone_id=m.id,position_x=$1,position_y=$2,version=n.version+1,updated_at=NOW() FROM space_roadmap_milestones m WHERE n.id=$3 AND n.roadmap_id=$4 AND n.space_id=$5 AND n.archived_at IS NULL AND m.id=$6 AND m.roadmap_id=n.roadmap_id AND m.archived_at IS NULL`, item.PositionX, item.PositionY, item.ID, roadmapID, spaceID, item.MilestoneID)
			}
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrSpaceNotFound
			}
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.layout.updated", roadmapID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

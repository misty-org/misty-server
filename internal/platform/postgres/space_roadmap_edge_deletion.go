package db

import (
	"context"
	"database/sql"
)

func TestingRoadmapDependencyWouldCycle(edges []SpaceRoadmapEdge, sourceID, targetID string) bool {
	graph := map[string][]string{sourceID: {targetID}}
	for _, edge := range edges {
		if causalRoadmapEdge(edge.EdgeType) {
			source, target := edge.Source.ID, edge.Target.ID
			if source == "" {
				source = edge.SourceGoalID
			}
			if target == "" {
				target = edge.TargetGoalID
			}
			graph[source] = append(graph[source], target)
		}
	}
	seen, stack := map[string]bool{}, []string{targetID}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == sourceID {
			return true
		}
		if !seen[current] {
			seen[current] = true
			stack = append(stack, graph[current]...)
		}
	}
	return false
}

func (db *Database) DeleteSpaceRoadmapEdge(ctx context.Context, userID, spaceID, roadmapID, edgeID string, expected int64) (int64, error) {
	return roadmapMutationTx(ctx, db, userID, spaceID, roadmapID, expected, func(tx *sql.Tx, graphVersion int64) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM space_roadmap_edges WHERE id=$1 AND roadmap_id=$2 AND space_id=$3`, edgeID, roadmapID, spaceID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrSpaceNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.edge.removed", edgeID, map[string]any{"roadmap_id": roadmapID, "graph_version": graphVersion})
		return err
	})
}

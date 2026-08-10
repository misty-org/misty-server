package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SpaceRoadmap struct {
	ID                     string     `json:"id"`
	SpaceID                string     `json:"space_id"`
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	GraphVersion           int64      `json:"graph_version"`
	CreatedByUserID        string     `json:"created_by_user_id"`
	AudienceKind           string     `json:"audience_kind"`
	AudienceConversationID string     `json:"audience_conversation_id,omitempty"`
	ArchivedAt             *time.Time `json:"archived_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type SpaceRoadmapMilestone struct {
	ID          string     `json:"id"`
	SpaceID     string     `json:"space_id"`
	RoadmapID   string     `json:"roadmap_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	TargetDate  *time.Time `json:"target_date,omitempty"`
	Rank        int64      `json:"rank"`
	PositionX   float64    `json:"position_x"`
	PositionY   float64    `json:"position_y"`
	Width       float64    `json:"width"`
	Height      float64    `json:"height"`
	Version     int64      `json:"version"`
	GoalTotal   int        `json:"goal_total"`
	GoalDone    int        `json:"goal_done"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type SpaceRoadmapGoal struct {
	ID                      string      `json:"id"`
	SpaceID                 string      `json:"space_id"`
	RoadmapID               string      `json:"roadmap_id"`
	MilestoneID             string      `json:"milestone_id"`
	Title                   string      `json:"title"`
	Description             string      `json:"description"`
	TargetDate              *time.Time  `json:"target_date,omitempty"`
	Rank                    int64       `json:"rank"`
	PositionX               float64     `json:"position_x"`
	PositionY               float64     `json:"position_y"`
	ManualCompletedAt       *time.Time  `json:"manual_completed_at,omitempty"`
	ManualCompletedByUserID string      `json:"manual_completed_by_user_id,omitempty"`
	Version                 int64       `json:"version"`
	TaskTotal               int         `json:"task_total"`
	TaskDone                int         `json:"task_done"`
	ProgressPercentage      int         `json:"progress_percentage"`
	Status                  string      `json:"status"`
	Tasks                   []SpaceTask `json:"tasks"`
	CreatedAt               time.Time   `json:"created_at"`
	UpdatedAt               time.Time   `json:"updated_at"`
}

type SpaceRoadmapEdge struct {
	ID           string                   `json:"id"`
	SpaceID      string                   `json:"space_id"`
	RoadmapID    string                   `json:"roadmap_id"`
	Source       SpaceRoadmapEdgeEndpoint `json:"source"`
	Target       SpaceRoadmapEdgeEndpoint `json:"target"`
	SourceGoalID string                   `json:"source_goal_id,omitempty"`
	TargetGoalID string                   `json:"target_goal_id,omitempty"`
	EdgeType     string                   `json:"edge_type"`
	Label        string                   `json:"label"`
	Version      int64                    `json:"version"`
	CreatedAt    time.Time                `json:"created_at"`
	UpdatedAt    time.Time                `json:"updated_at"`
}

type SpaceRoadmapEdgeEndpoint struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type SpaceRoadmapNodeDefinition struct {
	ID              string          `json:"id"`
	SpaceID         string          `json:"space_id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Icon            string          `json:"icon"`
	Color           string          `json:"color"`
	AgendaVisible   bool            `json:"agenda_visible"`
	FieldSchema     json.RawMessage `json:"field_schema"`
	Version         int64           `json:"version"`
	CreatedByUserID string          `json:"created_by_user_id"`
	ArchivedAt      *time.Time      `json:"archived_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type SpaceRoadmapNode struct {
	ID           string          `json:"id"`
	SpaceID      string          `json:"space_id"`
	RoadmapID    string          `json:"roadmap_id"`
	MilestoneID  string          `json:"milestone_id,omitempty"`
	DefinitionID string          `json:"definition_id,omitempty"`
	NodeKind     string          `json:"node_kind"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	TargetDate   *time.Time      `json:"target_date,omitempty"`
	PositionX    float64         `json:"position_x"`
	PositionY    float64         `json:"position_y"`
	FieldValues  json.RawMessage `json:"field_values"`
	Version      int64           `json:"version"`
	ArchivedAt   *time.Time      `json:"archived_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type SpaceRoadmapSnapshot struct {
	Roadmap            SpaceRoadmap                 `json:"roadmap"`
	Milestones         []SpaceRoadmapMilestone      `json:"milestones"`
	Goals              []SpaceRoadmapGoal           `json:"goals"`
	Nodes              []SpaceRoadmapNode           `json:"nodes"`
	NodeDefinitions    []SpaceRoadmapNodeDefinition `json:"node_definitions"`
	Edges              []SpaceRoadmapEdge           `json:"edges"`
	GoalTotal          int                          `json:"goal_total"`
	GoalDone           int                          `json:"goal_done"`
	MilestoneTotal     int                          `json:"milestone_total"`
	MilestoneDone      int                          `json:"milestone_done"`
	ProgressPercentage int                          `json:"progress_percentage"`
}

type SpaceRoadmapMutationResult struct {
	GraphVersion int64 `json:"graph_version"`
}

type SpaceRoadmapLayout struct {
	Milestones []SpaceRoadmapMilestone `json:"milestones"`
	Goals      []SpaceRoadmapGoal      `json:"goals"`
	Nodes      []SpaceRoadmapNode      `json:"nodes"`
}

const roadmapColumns = `id,space_id,name,description,graph_version,created_by_user_id,audience_kind,COALESCE(audience_conversation_id,''),archived_at,created_at,updated_at`
const roadmapMilestoneColumns = `id,space_id,roadmap_id,title,description,target_date,rank,position_x,position_y,width,height,version,created_at,updated_at`
const roadmapGoalColumns = `id,space_id,roadmap_id,milestone_id,title,description,target_date,rank,position_x,position_y,manual_completed_at,COALESCE(manual_completed_by_user_id,''),version,created_at,updated_at`
const roadmapEdgeColumns = `id,space_id,roadmap_id,source_kind,source_id,target_kind,target_id,COALESCE(source_goal_id,''),COALESCE(target_goal_id,''),edge_type,label,version,created_at,updated_at`
const roadmapNodeDefinitionColumns = `id,space_id,name,description,icon,color,agenda_visible,field_schema,version,created_by_user_id,archived_at,created_at,updated_at`
const roadmapNodeColumns = `id,space_id,roadmap_id,COALESCE(milestone_id,''),COALESCE(definition_id,''),node_kind,title,description,target_date,position_x,position_y,field_values,version,archived_at,created_at,updated_at`

func scanSpaceRoadmap(scanner interface{ Scan(...any) error }, item *SpaceRoadmap) error {
	return scanner.Scan(&item.ID, &item.SpaceID, &item.Name, &item.Description, &item.GraphVersion, &item.CreatedByUserID, &item.AudienceKind, &item.AudienceConversationID, &item.ArchivedAt, &item.CreatedAt, &item.UpdatedAt)
}

func scanSpaceRoadmapMilestone(scanner interface{ Scan(...any) error }, item *SpaceRoadmapMilestone) error {
	return scanner.Scan(&item.ID, &item.SpaceID, &item.RoadmapID, &item.Title, &item.Description, &item.TargetDate, &item.Rank, &item.PositionX, &item.PositionY, &item.Width, &item.Height, &item.Version, &item.CreatedAt, &item.UpdatedAt)
}

func scanSpaceRoadmapGoal(scanner interface{ Scan(...any) error }, item *SpaceRoadmapGoal) error {
	return scanner.Scan(&item.ID, &item.SpaceID, &item.RoadmapID, &item.MilestoneID, &item.Title, &item.Description, &item.TargetDate, &item.Rank, &item.PositionX, &item.PositionY, &item.ManualCompletedAt, &item.ManualCompletedByUserID, &item.Version, &item.CreatedAt, &item.UpdatedAt)
}

func scanSpaceRoadmapEdge(scanner interface{ Scan(...any) error }, item *SpaceRoadmapEdge) error {
	return scanner.Scan(&item.ID, &item.SpaceID, &item.RoadmapID, &item.Source.Kind, &item.Source.ID, &item.Target.Kind, &item.Target.ID, &item.SourceGoalID, &item.TargetGoalID, &item.EdgeType, &item.Label, &item.Version, &item.CreatedAt, &item.UpdatedAt)
}

func scanSpaceRoadmapNodeDefinition(scanner interface{ Scan(...any) error }, item *SpaceRoadmapNodeDefinition) error {
	return scanner.Scan(&item.ID, &item.SpaceID, &item.Name, &item.Description, &item.Icon, &item.Color, &item.AgendaVisible, &item.FieldSchema, &item.Version, &item.CreatedByUserID, &item.ArchivedAt, &item.CreatedAt, &item.UpdatedAt)
}

func scanSpaceRoadmapNode(scanner interface{ Scan(...any) error }, item *SpaceRoadmapNode) error {
	return scanner.Scan(&item.ID, &item.SpaceID, &item.RoadmapID, &item.MilestoneID, &item.DefinitionID, &item.NodeKind, &item.Title, &item.Description, &item.TargetDate, &item.PositionX, &item.PositionY, &item.FieldValues, &item.Version, &item.ArchivedAt, &item.CreatedAt, &item.UpdatedAt)
}

func (db *Database) SpaceRoadmaps(ctx context.Context, userID, spaceID string) ([]SpaceRoadmap, error) {
	items := []SpaceRoadmap{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+roadmapColumns+` FROM space_roadmaps WHERE space_id=$1 AND archived_at IS NULL AND (audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$2)) ORDER BY updated_at DESC,id`, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceRoadmap
			if err := scanSpaceRoadmap(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CreateSpaceRoadmap(ctx context.Context, userID, spaceID, name, description string) (*SpaceRoadmapSnapshot, error) {
	return db.CreateSpaceRoadmapWithAudience(ctx, userID, spaceID, name, description, SpaceResourceAudience{Kind: SpaceAudienceSpace})
}

func (db *Database) CreateSpaceRoadmapWithAudience(ctx context.Context, userID, spaceID, name, description string, audience SpaceResourceAudience) (*SpaceRoadmapSnapshot, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 160 || len([]rune(description)) > 5000 {
		return nil, ErrSpaceInvalid
	}
	roadmapID := "roadmap_" + uuid.NewString()
	milestoneID := "milestone_" + uuid.NewString()
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksManage); err != nil {
			return err
		}
		normalized, err := NormalizeResourceAudience(audience.Kind, audience.ConversationID)
		if err != nil {
			return err
		}
		if err := validateResourceAudienceTx(ctx, tx, userID, spaceID, normalized); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_roadmaps(id,space_id,name,description,created_by_user_id,audience_kind,audience_conversation_id) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, roadmapID, spaceID, name, description, userID, normalized.Kind, normalized.ConversationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_roadmap_milestones(id,space_id,roadmap_id,title,rank,position_x,position_y) VALUES($1,$2,$3,'First milestone',1024,80,80)`, milestoneID, spaceID, roadmapID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roadmap.created", roadmapID, map[string]any{"roadmap_id": roadmapID})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceRoadmap(ctx, userID, spaceID, roadmapID)
}

func (db *Database) SpaceRoadmap(ctx context.Context, userID, spaceID, roadmapID string) (*SpaceRoadmapSnapshot, error) {
	out := &SpaceRoadmapSnapshot{Milestones: []SpaceRoadmapMilestone{}, Goals: []SpaceRoadmapGoal{}, Nodes: []SpaceRoadmapNode{}, NodeDefinitions: []SpaceRoadmapNodeDefinition{}, Edges: []SpaceRoadmapEdge{}}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionTasksView); err != nil {
			return err
		}
		if err := loadSpaceRoadmapTx(ctx, tx, userID, spaceID, roadmapID, out); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else {
			return err
		}
	})
	return out, err
}

func loadSpaceRoadmapTx(ctx context.Context, tx *sql.Tx, userID, spaceID, roadmapID string, out *SpaceRoadmapSnapshot) error {
	if err := scanSpaceRoadmap(tx.QueryRowContext(ctx, `SELECT `+roadmapColumns+` FROM space_roadmaps WHERE id=$1 AND space_id=$2 AND archived_at IS NULL AND (audience_kind='space' OR EXISTS(SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=audience_conversation_id AND cm.actor_kind='person' AND cm.user_id=$3))`, roadmapID, spaceID, userID), &out.Roadmap); err != nil {
		return err
	}
	milestoneRows, err := tx.QueryContext(ctx, `SELECT `+roadmapMilestoneColumns+` FROM space_roadmap_milestones WHERE roadmap_id=$1 AND space_id=$2 AND archived_at IS NULL ORDER BY rank,id`, roadmapID, spaceID)
	if err != nil {
		return err
	}
	for milestoneRows.Next() {
		var item SpaceRoadmapMilestone
		if err := scanSpaceRoadmapMilestone(milestoneRows, &item); err != nil {
			milestoneRows.Close()
			return err
		}
		out.Milestones = append(out.Milestones, item)
	}
	if err := milestoneRows.Close(); err != nil {
		return err
	}
	goalRows, err := tx.QueryContext(ctx, `SELECT `+roadmapGoalColumns+` FROM space_roadmap_goals WHERE roadmap_id=$1 AND space_id=$2 AND archived_at IS NULL ORDER BY milestone_id,rank,id`, roadmapID, spaceID)
	if err != nil {
		return err
	}
	for goalRows.Next() {
		var item SpaceRoadmapGoal
		item.Tasks = []SpaceTask{}
		if err := scanSpaceRoadmapGoal(goalRows, &item); err != nil {
			goalRows.Close()
			return err
		}
		out.Goals = append(out.Goals, item)
	}
	if err := goalRows.Close(); err != nil {
		return err
	}
	nodeRows, err := tx.QueryContext(ctx, `SELECT `+roadmapNodeColumns+` FROM space_roadmap_nodes WHERE roadmap_id=$1 AND space_id=$2 AND archived_at IS NULL ORDER BY created_at,id`, roadmapID, spaceID)
	if err != nil {
		return err
	}
	for nodeRows.Next() {
		var item SpaceRoadmapNode
		if err := scanSpaceRoadmapNode(nodeRows, &item); err != nil {
			nodeRows.Close()
			return err
		}
		out.Nodes = append(out.Nodes, item)
	}
	if err := nodeRows.Close(); err != nil {
		return err
	}
	definitionRows, err := tx.QueryContext(ctx, `SELECT `+roadmapNodeDefinitionColumns+` FROM space_roadmap_node_definitions d WHERE d.space_id=$1 AND (d.archived_at IS NULL OR EXISTS(SELECT 1 FROM space_roadmap_nodes n WHERE n.definition_id=d.id AND n.roadmap_id=$2 AND n.archived_at IS NULL)) ORDER BY d.name,d.id`, spaceID, roadmapID)
	if err != nil {
		return err
	}
	for definitionRows.Next() {
		var item SpaceRoadmapNodeDefinition
		if err := scanSpaceRoadmapNodeDefinition(definitionRows, &item); err != nil {
			definitionRows.Close()
			return err
		}
		out.NodeDefinitions = append(out.NodeDefinitions, item)
	}
	if err := definitionRows.Close(); err != nil {
		return err
	}
	edgeRows, err := tx.QueryContext(ctx, `SELECT `+roadmapEdgeColumns+` FROM space_roadmap_edges e WHERE roadmap_id=$1 AND space_id=$2 ORDER BY created_at,id`, roadmapID, spaceID)
	if err != nil {
		return err
	}
	for edgeRows.Next() {
		var item SpaceRoadmapEdge
		if err := scanSpaceRoadmapEdge(edgeRows, &item); err != nil {
			edgeRows.Close()
			return err
		}
		if roadmapEndpointVisible(item.Source, out) && roadmapEndpointVisible(item.Target, out) {
			out.Edges = append(out.Edges, item)
		}
	}
	if err := edgeRows.Close(); err != nil {
		return err
	}
	if err := loadSpaceRoadmapGoalTasksTx(ctx, tx, roadmapID, out.Goals); err != nil {
		return err
	}
	calculateSpaceRoadmapProgress(out)
	return nil
}

func roadmapEndpointVisible(endpoint SpaceRoadmapEdgeEndpoint, snapshot *SpaceRoadmapSnapshot) bool {
	switch endpoint.Kind {
	case "milestone":
		for _, item := range snapshot.Milestones {
			if item.ID == endpoint.ID {
				return true
			}
		}
	case "goal":
		for _, item := range snapshot.Goals {
			if item.ID == endpoint.ID {
				return true
			}
		}
	case "node":
		for _, item := range snapshot.Nodes {
			if item.ID == endpoint.ID {
				return true
			}
		}
	}
	return false
}

func calculateSpaceRoadmapProgress(out *SpaceRoadmapSnapshot) {
	milestones := map[string]*SpaceRoadmapMilestone{}
	for index := range out.Milestones {
		out.Milestones[index].Status = "not_started"
		milestones[out.Milestones[index].ID] = &out.Milestones[index]
	}
	for index := range out.Goals {
		goal := &out.Goals[index]
		goal.TaskTotal, goal.TaskDone = 0, 0
		started := false
		for _, task := range goal.Tasks {
			if task.ArchivedAt != nil || task.Status == "canceled" {
				continue
			}
			goal.TaskTotal++
			if task.Status == "done" {
				goal.TaskDone++
				started = true
			} else if task.Status == "in_progress" {
				started = true
			}
		}
		if goal.TaskTotal == 0 {
			if goal.ManualCompletedAt != nil {
				goal.Status, goal.ProgressPercentage = "done", 100
			} else {
				goal.Status = "not_started"
			}
		} else {
			goal.ProgressPercentage = (goal.TaskDone*100 + goal.TaskTotal/2) / goal.TaskTotal
			switch {
			case goal.TaskDone == goal.TaskTotal:
				goal.Status = "done"
			case started:
				goal.Status = "in_progress"
			default:
				goal.Status = "not_started"
			}
		}
		out.GoalTotal++
		if goal.Status == "done" {
			out.GoalDone++
		}
		if milestone := milestones[goal.MilestoneID]; milestone != nil {
			milestone.GoalTotal++
			if goal.Status == "done" {
				milestone.GoalDone++
			}
		}
	}
	for index := range out.Milestones {
		milestone := &out.Milestones[index]
		out.MilestoneTotal++
		if milestone.GoalTotal > 0 && milestone.GoalDone == milestone.GoalTotal {
			milestone.Status = "done"
			out.MilestoneDone++
		} else if milestone.GoalDone > 0 {
			milestone.Status = "in_progress"
		}
	}
	if out.GoalTotal > 0 {
		out.ProgressPercentage = (out.GoalDone*100 + out.GoalTotal/2) / out.GoalTotal
	}
}

func TestingCalculateSpaceRoadmapProgress(out *SpaceRoadmapSnapshot) {
	calculateSpaceRoadmapProgress(out)
}

func sortedUniqueStrings(values []string) ([]string, bool) {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return nil, false
		}
		seen[value] = true
	}
	items := make([]string, 0, len(seen))
	for value := range seen {
		items = append(items, value)
	}
	sort.Strings(items)
	return items, true
}

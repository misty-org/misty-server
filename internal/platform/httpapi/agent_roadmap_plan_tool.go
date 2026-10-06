package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// roadmaps.plan edits a roadmap's milestones and goals in one tool. Each
// change reads the roadmap's current graph version first, so it never
// overwrites a newer edit.
func roadmapPlanToolRegistration(database *db.Database) agenttools.Registration {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "roadmaps.plan", Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
			Description: "Edit a roadmap's milestones and goals in the current Space. Actions: add_milestone (roadmap_id, title), " +
				"update_milestone (roadmap_id, milestone_id), archive_milestone, add_goal (roadmap_id, milestone_id, title), " +
				"update_goal (roadmap_id, goal_id; done marks it complete), archive_goal, set_goal_tasks (roadmap_id, goal_id, task_ids). " +
				"Read the roadmap first with roadmaps_read for ids.",
			InputSchema: agentToolSchema(map[string]any{
				"action":       map[string]any{"type": "string", "enum": []string{"add_milestone", "update_milestone", "archive_milestone", "add_goal", "update_goal", "archive_goal", "set_goal_tasks"}},
				"roadmap_id":   text(200),
				"milestone_id": text(200),
				"goal_id":      text(200),
				"title":        text(200),
				"description":  map[string]any{"type": "string", "maxLength": 5000},
				"target_date":  map[string]any{"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$", "description": "YYYY-MM-DD"},
				"done":         map[string]any{"type": "boolean"},
				"task_ids":     map[string]any{"type": "array", "maxItems": 100, "items": text(200)},
			}, []string{"action", "roadmap_id"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true,
			RequiredPermission: db.PermissionTasksManage, AuditEvent: "roadmap.updated", Sources: agentToolboxSpaceSources,
		},
		Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			return executeRoadmapPlan(ctx, database, invocation, request)
		},
	}
}

type roadmapPlanInput struct {
	Action      string   `json:"action"`
	RoadmapID   string   `json:"roadmap_id"`
	MilestoneID string   `json:"milestone_id"`
	GoalID      string   `json:"goal_id"`
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	TargetDate  *string  `json:"target_date"`
	Done        *bool    `json:"done"`
	TaskIDs     []string `json:"task_ids"`
}

func (input roadmapPlanInput) apply(title, description *string, target **time.Time) error {
	if input.Title != nil {
		*title = strings.TrimSpace(*input.Title)
	}
	if input.Description != nil {
		*description = *input.Description
	}
	if input.TargetDate != nil {
		date, err := time.Parse(time.DateOnly, *input.TargetDate)
		if err != nil {
			return serveragent.ErrInvalidRequest("target_date must be YYYY-MM-DD")
		}
		*target = &date
	}
	return nil
}

func executeRoadmapPlan(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input roadmapPlanInput
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	user, space := invocation.UserID, invocation.SpaceID
	snapshot, err := database.SpaceRoadmap(ctx, user, space, input.RoadmapID)
	if err != nil {
		return nil, roadmapPlanError(err)
	}
	version := snapshot.Roadmap.GraphVersion
	var result any
	switch input.Action {
	case "add_milestone", "update_milestone":
		item := db.SpaceRoadmapMilestone{}
		if input.Action == "update_milestone" {
			found := false
			for _, milestone := range snapshot.Milestones {
				if milestone.ID == input.MilestoneID {
					item, found = milestone, true
				}
			}
			if !found {
				return nil, serveragent.ErrInvalidRequest("no milestone " + input.MilestoneID + " on this roadmap")
			}
		}
		if err := input.apply(&item.Title, &item.Description, &item.TargetDate); err != nil {
			return nil, err
		}
		if item.Title == "" {
			return nil, serveragent.ErrInvalidRequest("a milestone needs a title")
		}
		var saved *db.SpaceRoadmapMilestone
		if input.Action == "add_milestone" {
			saved, _, err = database.CreateSpaceRoadmapMilestone(ctx, user, space, input.RoadmapID, item, version)
		} else {
			saved, _, err = database.UpdateSpaceRoadmapMilestone(ctx, user, space, input.RoadmapID, item.ID, item, version)
		}
		if saved != nil {
			result = map[string]any{"milestone": map[string]any{"id": saved.ID, "title": saved.Title}}
		}
	case "add_goal", "update_goal":
		item := db.SpaceRoadmapGoal{MilestoneID: input.MilestoneID}
		if input.Action == "update_goal" {
			found := false
			for _, goal := range snapshot.Goals {
				if goal.ID == input.GoalID {
					item, found = goal, true
				}
			}
			if !found {
				return nil, serveragent.ErrInvalidRequest("no goal " + input.GoalID + " on this roadmap")
			}
			if input.MilestoneID != "" {
				item.MilestoneID = input.MilestoneID
			}
		}
		if err := input.apply(&item.Title, &item.Description, &item.TargetDate); err != nil {
			return nil, err
		}
		if item.Title == "" || item.MilestoneID == "" {
			return nil, serveragent.ErrInvalidRequest("a goal needs a title and a milestone_id")
		}
		var saved *db.SpaceRoadmapGoal
		if input.Action == "add_goal" {
			saved, _, err = database.CreateSpaceRoadmapGoal(ctx, user, space, input.RoadmapID, item, version)
		} else {
			saved, _, err = database.UpdateSpaceRoadmapGoal(ctx, user, space, input.RoadmapID, item.ID, item, input.Done, version)
			if errors.Is(err, db.ErrSpaceInvalid) && input.Done != nil && *input.Done && item.TaskTotal > item.TaskDone {
				return nil, serveragent.ErrInvalidRequest("this goal has open linked tasks; it completes when they are done, so update those tasks instead")
			}
		}
		if saved != nil {
			result = map[string]any{"goal": map[string]any{"id": saved.ID, "title": saved.Title}}
		}
	case "archive_milestone":
		_, err = database.ArchiveSpaceRoadmapMilestone(ctx, user, space, input.RoadmapID, input.MilestoneID, version)
		result = map[string]any{"archived_milestone": input.MilestoneID}
	case "archive_goal":
		_, err = database.ArchiveSpaceRoadmapGoal(ctx, user, space, input.RoadmapID, input.GoalID, version)
		result = map[string]any{"archived_goal": input.GoalID}
	case "set_goal_tasks":
		_, err = database.ReplaceSpaceRoadmapGoalTasks(ctx, user, space, input.RoadmapID, input.GoalID, input.TaskIDs, version)
		result = map[string]any{"goal_id": input.GoalID, "tasks": len(input.TaskIDs)}
	default:
		return nil, serveragent.ErrInvalidRequest("unknown action " + input.Action)
	}
	if err != nil {
		return nil, roadmapPlanError(err)
	}
	return json.Marshal(result)
}

// roadmapPlanError explains rejections the model can correct; nothing changed.
func roadmapPlanError(err error) error {
	switch {
	case errors.Is(err, db.ErrSpaceNotFound):
		return serveragent.ErrInvalidRequest("that roadmap, milestone, goal or task was not found in this Space; read the roadmap again")
	case errors.Is(err, db.ErrSpaceForbidden):
		return serveragent.ErrInvalidRequest("you do not have permission to edit this roadmap")
	case errors.Is(err, db.ErrSpaceConflict):
		return serveragent.ErrInvalidRequest("the roadmap changed while editing; read it again and retry")
	case errors.Is(err, db.ErrSpaceInvalid):
		return serveragent.ErrInvalidRequest("those roadmap values are not valid")
	}
	return err
}

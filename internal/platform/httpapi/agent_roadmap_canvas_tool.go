package api

import (
	"context"
	"encoding/json"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// roadmaps.canvas adds risks, decisions, metrics and notes to a roadmap and
// links them to goals and milestones. Custom node types stay in the canvas
// editor, where their fields are designed.
func roadmapCanvasToolRegistration(database *db.Database) agenttools.Registration {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	endpoint := map[string]any{"type": "object", "required": []string{"kind", "id"}, "additionalProperties": false, "properties": map[string]any{
		"kind": map[string]any{"type": "string", "enum": []string{"goal", "milestone", "node"}}, "id": text(200),
	}}
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "roadmaps.canvas", Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
			Description: "Add risks, decisions, metrics and notes to a roadmap in the current Space and link them. Actions: add_node (roadmap_id, node_kind, title), " +
				"update_node (roadmap_id, node_id), archive_node (roadmap_id, node_id), link (roadmap_id, source, target, edge_type), unlink (roadmap_id, edge_id). " +
				"Edge types: depends_on (goal to goal), blocks (goal or risk to goal or milestone), enables (goal or decision to goal or milestone), " +
				"contributes_to (node to goal or milestone), measures (metric to goal or milestone), documents (note to anything), related (anything).",
			InputSchema: agentToolSchema(map[string]any{
				"action":       map[string]any{"type": "string", "enum": []string{"add_node", "update_node", "archive_node", "link", "unlink"}},
				"roadmap_id":   text(200),
				"node_id":      text(200),
				"edge_id":      text(200),
				"node_kind":    map[string]any{"type": "string", "enum": []string{"risk", "decision", "metric", "note"}},
				"milestone_id": text(200),
				"title":        text(240),
				"description":  map[string]any{"type": "string", "maxLength": 5000},
				"source":       endpoint,
				"target":       endpoint,
				"edge_type":    map[string]any{"type": "string", "enum": []string{"depends_on", "blocks", "enables", "contributes_to", "measures", "documents", "related"}},
				"label":        map[string]any{"type": "string", "maxLength": 120},
			}, []string{"action", "roadmap_id"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true,
			RequiredPermission: db.PermissionTasksManage, AuditEvent: "roadmap.updated", Sources: agentToolboxSpaceSources,
		},
		Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			return executeRoadmapCanvas(ctx, database, invocation, request)
		},
	}
}

func executeRoadmapCanvas(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Action      string                      `json:"action"`
		RoadmapID   string                      `json:"roadmap_id"`
		NodeID      string                      `json:"node_id"`
		EdgeID      string                      `json:"edge_id"`
		NodeKind    string                      `json:"node_kind"`
		MilestoneID string                      `json:"milestone_id"`
		Title       *string                     `json:"title"`
		Description *string                     `json:"description"`
		Source      db.SpaceRoadmapEdgeEndpoint `json:"source"`
		Target      db.SpaceRoadmapEdgeEndpoint `json:"target"`
		EdgeType    string                      `json:"edge_type"`
		Label       string                      `json:"label"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	user, space, roadmapID := invocation.UserID, invocation.SpaceID, input.RoadmapID
	snapshot, err := database.SpaceRoadmap(ctx, user, space, roadmapID)
	if err != nil {
		return nil, roadmapPlanError(err)
	}
	version := snapshot.Roadmap.GraphVersion
	var result any
	switch input.Action {
	case "add_node":
		item := db.SpaceRoadmapNode{NodeKind: input.NodeKind, MilestoneID: input.MilestoneID}
		if input.Title != nil {
			item.Title = *input.Title
		}
		if input.Description != nil {
			item.Description = *input.Description
		}
		node, _, createErr := database.CreateSpaceRoadmapNode(ctx, user, space, roadmapID, item, version)
		if node != nil {
			result = map[string]any{"node": map[string]any{"id": node.ID, "kind": node.NodeKind, "title": node.Title}}
		}
		err = createErr
	case "update_node":
		var item *db.SpaceRoadmapNode
		for index := range snapshot.Nodes {
			if snapshot.Nodes[index].ID == input.NodeID {
				item = &snapshot.Nodes[index]
			}
		}
		if item == nil {
			return nil, serveragent.ErrInvalidRequest("no node " + input.NodeID + " on this roadmap")
		}
		if input.Title != nil {
			item.Title = *input.Title
		}
		if input.Description != nil {
			item.Description = *input.Description
		}
		node, _, updateErr := database.UpdateSpaceRoadmapNode(ctx, user, space, roadmapID, item.ID, *item, version)
		if node != nil {
			result = map[string]any{"node": map[string]any{"id": node.ID, "title": node.Title}}
		}
		err = updateErr
	case "archive_node":
		_, err = database.ArchiveSpaceRoadmapNode(ctx, user, space, roadmapID, input.NodeID, version)
		result = map[string]any{"archived_node": input.NodeID}
	case "link":
		edge, _, linkErr := database.SaveSpaceRoadmapEdge(ctx, user, space, roadmapID, db.SpaceRoadmapEdge{
			Source: input.Source, Target: input.Target, EdgeType: input.EdgeType, Label: input.Label,
		}, version)
		if edge != nil {
			result = map[string]any{"edge": map[string]any{"id": edge.ID, "type": edge.EdgeType}}
		}
		err = linkErr
	case "unlink":
		_, err = database.DeleteSpaceRoadmapEdge(ctx, user, space, roadmapID, input.EdgeID, version)
		result = map[string]any{"removed_edge": input.EdgeID}
	default:
		return nil, serveragent.ErrInvalidRequest("unknown action " + input.Action)
	}
	if err != nil {
		return nil, roadmapPlanError(err)
	}
	return json.Marshal(result)
}

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

const (
	toolboxDrawingsList   = "drawing.list"
	toolboxDrawingsRead   = "drawing.read"
	toolboxDrawingsCreate = "drawing.create"
	toolboxDrawingsApply  = "drawing.apply"
)

var DrawingCollabConfigProvider = JournalCollabConfigFromEnv

func drawingAgentToolDescriptors() []agenttools.Descriptor {
	elementSchema := map[string]any{
		"type": "object", "required": []string{"id"},
		"properties": map[string]any{
			"id":   map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
			"type": map[string]any{"type": "string", "enum": []string{"rectangle", "diamond", "ellipse", "text", "line", "arrow", "freedraw", "image", "frame", "magicframe", "iframe", "embeddable"}},
			"x":    map[string]any{"type": "number"}, "y": map[string]any{"type": "number"},
			"width": map[string]any{"type": "number", "minimum": 0}, "height": map[string]any{"type": "number", "minimum": 0},
			"text":   map[string]any{"type": "string", "maxLength": 100000},
			"points": map[string]any{"type": "array", "maxItems": 10000},
		},
	}
	listSchema := MustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string", "maxLength": 500},
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
	}})
	readSchema := MustAPIRawJSON(map[string]any{"type": "object", "required": []string{"drawing_id"}, "properties": map[string]any{
		"drawing_id":      map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"include_deleted": map[string]any{"type": "boolean"},
	}})
	createSchema := MustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{
		"title": map[string]any{"type": "string", "maxLength": 200},
	}})
	applySchema := MustAPIRawJSON(map[string]any{
		"type": "object", "required": []string{"drawing_id"},
		"properties": map[string]any{
			"drawing_id":         map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"base_hash":          map[string]any{"type": "string", "minLength": 64, "maxLength": 64},
			"mode":               map[string]any{"type": "string", "enum": []string{"merge", "replace"}},
			"elements":           map[string]any{"type": "array", "maxItems": 500, "items": elementSchema},
			"delete_element_ids": map[string]any{"type": "array", "maxItems": 500, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}},
			"scene": map[string]any{"type": "object", "properties": map[string]any{
				"viewBackgroundColor": map[string]any{"type": "string", "maxLength": 100},
			}},
		},
	})
	return []agenttools.Descriptor{
		{Name: toolboxDrawingsList, Version: 1, Description: "List collaborative Excalidraw drawings visible in the current Space.", Risk: agent.RiskRead, InputSchema: listSchema, OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: toolboxDrawingsRead, Version: 1, Description: "Read one live Excalidraw scene, including native element JSON, background state, revision, and content hash.", Risk: agent.RiskRead, InputSchema: readSchema, OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: toolboxDrawingsCreate, Version: 1, Description: "Create an empty collaborative Excalidraw drawing in the current Space. Call drawing_apply afterward to draw its scene.", Risk: agent.RiskWrite, InputSchema: createSchema, OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, AuditEvent: "drawing.created", Sources: agentToolboxSpaceSources},
		{Name: toolboxDrawingsApply, Version: 1, Description: "Draw or edit a live Excalidraw scene. Merge or replace native elements of any supported Excalidraw type; partial objects update existing IDs, new IDs require type, and delete_element_ids creates collaboration-safe tombstones. Common fields are defaulted; advanced native Excalidraw fields are preserved. Pass the latest drawing_read base_hash to reject conflicting edits. Image elements reference fileId values already uploaded through Misty's drawing asset pipeline.", Risk: agent.RiskWrite, InputSchema: applySchema, OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPublic, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "drawing.scene.applied", Sources: agentToolboxSpaceSources},
	}
}

func executeAgentDrawingTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool agent.ToolRequest) (json.RawMessage, bool, error) {
	switch tool.Name {
	case toolboxDrawingsList:
		var input struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, true, agent.ErrInvalidRequest("drawing list input is invalid")
		}
		if input.Limit < 1 || input.Limit > 100 {
			input.Limit = 50
		}
		drawings, err := database.AccessibleSpaceDrawings(ctx, actor.userID, actor.spaceID)
		if err != nil {
			return nil, true, err
		}
		query := strings.ToLower(strings.TrimSpace(input.Query))
		matches := make([]db.SpaceDrawing, 0, min(input.Limit, len(drawings)))
		for _, drawing := range drawings {
			if query == "" || strings.Contains(strings.ToLower(drawing.Title), query) {
				matches = append(matches, drawing)
				if len(matches) == input.Limit {
					break
				}
			}
		}
		return MustAPIRawJSON(map[string]any{"drawings": matches, "count": len(matches)}), true, nil
	case toolboxDrawingsCreate:
		if !collaborationEnabled() {
			return nil, true, agent.ErrInvalidRequest(ErrCollaborationOff.Error())
		}
		var input struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, true, db.ErrSpaceInvalid
		}
		drawing, err := database.CreateSpaceDrawing(ctx, actor.userID, actor.spaceID, input.Title)
		return MustAPIRawJSON(drawing), true, err
	case toolboxDrawingsRead, toolboxDrawingsApply:
		var input struct {
			DrawingID      string `json:"drawing_id"`
			IncludeDeleted bool   `json:"include_deleted"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.DrawingID) == "" {
			return nil, true, agent.ErrInvalidRequest("drawing_id is required")
		}
		drawing, err := database.SpaceDrawingByID(ctx, actor.userID, input.DrawingID)
		if err != nil || drawing.SpaceID != actor.spaceID {
			if err == nil {
				err = db.ErrSpaceNotFound
			}
			return nil, true, err
		}
		if tool.Name == toolboxDrawingsApply {
			access, accessErr := database.DrawingAccessFor(ctx, actor.userID, drawing.ID)
			if accessErr != nil || !access.CanEdit {
				if accessErr == nil {
					accessErr = db.ErrSpaceForbidden
				}
				return nil, true, accessErr
			}
		}
		config, configErr := DrawingCollabConfigProvider()
		if configErr != nil {
			return nil, true, configErr
		}
		if !config.Enabled {
			return nil, true, agent.ErrInvalidRequest(ErrCollaborationOff.Error())
		}
		service := &SpacesService{database: database, JournalCollab: config}
		payload := tool.Arguments
		command := "drawing_scene_read"
		if tool.Name == toolboxDrawingsRead {
			payload = MustAPIRawJSON(map[string]any{"include_deleted": input.IncludeDeleted})
		} else {
			command = "drawing_scene_apply"
			var values map[string]any
			if json.Unmarshal(tool.Arguments, &values) != nil {
				return nil, true, db.ErrSpaceInvalid
			}
			delete(values, "drawing_id")
			if strings.TrimSpace(tool.ID) != "" {
				digest := sha256.Sum256([]byte(actor.runID + "\n" + tool.ID + "\n" + string(tool.Arguments)))
				values["request_id"] = "agent:" + hex.EncodeToString(digest[:])
			}
			payload = MustAPIRawJSON(values)
		}
		result, requestErr := service.requestCollaborationControlCommand(ctx, "drawing-room", config.DrawingRoomID(drawing.ID), drawing.ID, command, payload)
		if requestErr != nil {
			return nil, true, requestErr
		}
		var response map[string]any
		if json.Unmarshal(result, &response) == nil {
			response["drawing"] = drawing
			result = MustAPIRawJSON(response)
		}
		return result, true, nil
	default:
		return nil, false, nil
	}
}

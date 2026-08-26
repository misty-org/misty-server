package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	toolboxNotesSearch = "notes.search"
	toolboxNotesRead   = "notes.read"
	toolboxNotesCreate = "notes.create"
	toolboxNotesUpdate = "notes.update"
)

func noteAgentToolDescriptors() []agenttools.Descriptor {
	readSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string", "maxLength": 200}, "query": map[string]any{"type": "string", "maxLength": 500}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
	}})
	writeSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{
		"id": map[string]any{"type": "string", "maxLength": 200}, "title": map[string]any{"type": "string", "maxLength": 500}, "markdown": map[string]any{"type": "string", "maxLength": 100000},
	}})
	return []agenttools.Descriptor{
		{Name: toolboxNotesSearch, Version: 1, Description: "Search Notes visible in the current Space.", Risk: serveragent.RiskRead, InputSchema: readSchema, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: toolboxNotesRead, Version: 1, Description: "Read one Note visible in the current Space.", Risk: serveragent.RiskRead, InputSchema: readSchema, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: toolboxNotesCreate, Version: 1, Description: "Create a native Note in the current Space from a title and Markdown body.", Risk: serveragent.RiskWrite, InputSchema: writeSchema, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, AuditEvent: "note.created", Sources: agentToolboxSpaceSources},
		{Name: toolboxNotesUpdate, Version: 1, Description: "Replace the title or Markdown body of an explicitly identified Note.", Risk: serveragent.RiskWrite, InputSchema: writeSchema, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "note.updated", Sources: agentToolboxSpaceSources},
	}
}

func executeAgentNoteTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, bool, error) {
	switch tool.Name {
	case toolboxNotesSearch:
		var input struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, true, serveragent.ErrInvalidRequest("note search input is invalid")
		}
		if input.Limit < 1 || input.Limit > 50 {
			input.Limit = 20
		}
		notes, err := database.AccessibleSpaceNotes(ctx, actor.userID, actor.spaceID)
		if err != nil {
			return nil, true, err
		}
		query := strings.ToLower(strings.TrimSpace(input.Query))
		matches := make([]db.SpaceNote, 0, min(input.Limit, len(notes)))
		for _, summary := range notes {
			if query != "" && !strings.Contains(strings.ToLower(summary.TitleProjection), query) {
				full, readErr := database.SpaceNoteByID(ctx, actor.userID, summary.ID)
				if readErr != nil || !strings.Contains(strings.ToLower(full.PlainTextProjection), query) {
					continue
				}
				summary = *full
			}
			matches = append(matches, summary)
			if len(matches) == input.Limit {
				break
			}
		}
		return TestingMustAPIRawJSON(map[string]any{"notes": matches, "count": len(matches)}), true, nil
	case toolboxNotesRead:
		var input struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("note id is required")
		}
		note, err := database.SpaceNoteByID(ctx, actor.userID, input.ID)
		if err == nil && note.SpaceID != actor.spaceID {
			err = db.ErrSpaceNotFound
		}
		return TestingMustAPIRawJSON(note), true, err
	case toolboxNotesCreate:
		var input struct {
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Markdown) == "" {
			return nil, true, serveragent.ErrInvalidRequest("note title and markdown are required")
		}
		note, err := database.CreateSpaceNoteWithAudience(ctx, actor.userID, actor.spaceID, input.Title, db.SpaceResourceAudience{Kind: db.SpaceAudienceSpace}, input.Markdown)
		return TestingMustAPIRawJSON(note), true, err
	case toolboxNotesUpdate:
		var input struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("note id is required")
		}
		current, err := database.SpaceNoteByID(ctx, actor.userID, input.ID)
		if err != nil || current.SpaceID != actor.spaceID {
			if err == nil {
				err = db.ErrSpaceNotFound
			}
			return nil, true, err
		}
		if err := requireAgentMutationTarget(ctx, database, actor, actor.originalPrompt, "note", current.ID); err != nil {
			return nil, true, err
		}
		if strings.TrimSpace(input.Title) == "" {
			input.Title = current.TitleProjection
		}
		if input.Markdown == "" {
			input.Markdown = current.PlainTextProjection
		}
		note, err := database.UpdateSpaceNoteContent(ctx, actor.userID, input.ID, input.Title, input.Markdown)
		return TestingMustAPIRawJSON(note), true, err
	default:
		return nil, false, nil
	}
}

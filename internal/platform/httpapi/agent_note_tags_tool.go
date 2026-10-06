package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// notes.tags sets a note's shared tags, the ones everyone in the Space sees.
func noteTagsToolRegistration(database *db.Database) agenttools.Registration {
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "notes.tags", Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
			Description: "Replace a note's shared tags in the current Space. Pass the full list; an empty list clears them.",
			InputSchema: agentToolSchema(map[string]any{
				"id":   map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
				"tags": map[string]any{"type": "array", "maxItems": 30, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 60}},
			}, []string{"id", "tags"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, Idempotent: true,
			AuditEvent: "note.tags.updated", Sources: agentToolboxSpaceSources,
		},
		Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			var input struct {
				ID   string   `json:"id"`
				Tags []string `json:"tags"`
			}
			if json.Unmarshal(request.Arguments, &input) != nil {
				return nil, agenttools.ErrArgumentsInvalid
			}
			note, err := database.SpaceNoteByID(ctx, invocation.UserID, strings.TrimSpace(input.ID))
			if err != nil || note.SpaceID != invocation.SpaceID {
				return nil, serveragent.ErrInvalidRequest("no note " + input.ID + " in this Space; call notes_search")
			}
			if err := database.UpdateNoteSharedTags(ctx, invocation.UserID, note.ID, input.Tags); err != nil {
				return nil, serveragent.ErrInvalidRequest("those tags could not be saved; you may not be able to edit this note")
			}
			return json.Marshal(map[string]any{"note_id": note.ID, "tags": input.Tags})
		},
	}
}

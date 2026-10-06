package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// items.delete is the one delete tool for Misty items. Deleting asks first
// when the account asks first, and every delete re-checks the item's own
// permissions in the database.
const itemsDeleteTool = "items.delete"

func (s *SpacesService) deleteToolRegistration() agenttools.Registration {
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: itemsDeleteTool, Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
			Description: "Delete a note, drawing, calendar event, thread or Library album; move a Library item to the trash; or archive a task or roadmap. " +
				"Use only when the user asked to delete that exact item; the user may see an approval card first. Every kind except notes and drawings also needs its Space.",
			InputSchema: agentToolSchema(map[string]any{
				"kind":  map[string]any{"type": "string", "enum": []string{"note", "task", "drawing", "calendar_event", "thread", "album", "roadmap", "library_item", "shared_file"}},
				"id":    map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
				"space": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			}, []string{"kind", "id"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, AuditEvent: "item.deleted",
		},
		Handler: s.executeItemDelete,
	}
}

func (s *SpacesService) executeItemDelete(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Space string `json:"space"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	id := strings.TrimSpace(input.ID)
	notFound := serveragent.ErrInvalidRequest("no " + input.Kind + " " + id + " that you can delete")
	var title, spaceID string
	var remove func() error
	switch input.Kind {
	case "note":
		note, err := s.database.SpaceNoteByID(ctx, invocation.UserID, id)
		if err != nil {
			return nil, notFound
		}
		title, spaceID = note.TitleProjection, note.SpaceID
		remove = func() error { return s.database.DeleteSpaceNote(ctx, invocation.UserID, id) }
	case "drawing":
		drawing, err := s.database.SpaceDrawingByID(ctx, invocation.UserID, id)
		if err != nil {
			return nil, notFound
		}
		title, spaceID = drawing.Title, drawing.SpaceID
		remove = func() error { return s.database.DeleteSpaceDrawing(ctx, invocation.UserID, id) }
	default:
		space, err := s.spaceAdminTarget(ctx, invocation, input.Space)
		if err != nil {
			return nil, err
		}
		title, remove, err = s.spaceDeleteTarget(ctx, invocation.UserID, space.ID, input.Kind, id)
		if err != nil {
			return nil, notFound
		}
		spaceID = space.ID
	}
	if strings.TrimSpace(title) == "" {
		title = "Untitled " + input.Kind
	}
	approval, waiting, err := s.confirmAgentAction(ctx, invocation, "misty.items.delete", []string{input.Kind, id}, "Delete "+input.Kind+" “"+title+"”", "This cannot be undone from the agent.")
	if err != nil || waiting != nil {
		return waiting, err
	}
	if err := s.useAgentActionApproval(ctx, invocation.UserID, approval); err != nil {
		return nil, err
	}
	if err := remove(); err != nil {
		if errors.Is(err, db.ErrSpaceForbidden) || errors.Is(err, db.ErrLibraryForbidden) {
			return nil, serveragent.ErrInvalidRequest("you do not have permission to delete that " + input.Kind)
		}
		return nil, err
	}
	return json.Marshal(map[string]any{"deleted": input.Kind, "id": id, "title": title, "space_id": spaceID})
}

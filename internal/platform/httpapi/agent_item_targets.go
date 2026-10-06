package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// spaceDeleteTarget finds one Space item to delete and returns its title and
// the removal to run after approval. Each removal re-checks permissions and
// the item's current version in the database.
func (s *SpacesService) spaceDeleteTarget(ctx context.Context, userID, spaceID, kind, id string) (string, func() error, error) {
	switch kind {
	case "calendar_event":
		event, err := s.database.NativeCalendarEvent(ctx, userID, spaceID, id)
		if err != nil {
			return "", nil, err
		}
		return event.Title, func() error { return s.database.ArchiveNativeCalendarEvent(ctx, userID, spaceID, id, event.Version) }, nil
	case "thread":
		thread, err := spaceThread(ctx, s.database, userID, spaceID, id)
		if err != nil {
			return "", nil, err
		}
		return thread.Title, func() error { return s.database.DeleteOrClearSpaceConversation(ctx, userID, spaceID, id) }, nil
	case "album":
		album, err := s.database.LibraryAlbum(ctx, userID, spaceID, id)
		if err != nil {
			return "", nil, err
		}
		return album.Name, func() error { return s.database.DeleteLibraryAlbum(ctx, userID, spaceID, id, album.Version) }, nil
	case "roadmap":
		roadmap, err := s.database.SpaceRoadmap(ctx, userID, spaceID, id)
		if err != nil {
			return "", nil, err
		}
		return roadmap.Roadmap.Name, func() error {
			_, err := s.database.ArchiveSpaceRoadmap(ctx, userID, spaceID, id, roadmap.Roadmap.GraphVersion)
			return err
		}, nil
	case "shared_file":
		nodes, err := s.database.SpaceNodes(ctx, userID, spaceID)
		if err != nil {
			return "", nil, err
		}
		for _, node := range nodes {
			if node.ID == id {
				return node.DisplayName, func() error { return s.database.DeleteSpaceNode(ctx, userID, spaceID, id) }, nil
			}
		}
		return "", nil, db.ErrSpaceNotFound
	case "library_item":
		item, err := s.database.LibraryItem(ctx, userID, spaceID, id)
		if err != nil {
			return "", nil, err
		}
		return item.DisplayName, func() error {
			_, err := s.database.TrashLibraryItem(ctx, userID, spaceID, id)
			return err
		}, nil
	}
	task, err := s.database.SpaceTaskForMember(ctx, userID, spaceID, id)
	if err != nil {
		return "", nil, err
	}
	return task.Title, func() error {
		_, err := s.database.ArchiveSpaceTask(ctx, userID, spaceID, id, task.Version)
		return err
	}, nil
}

func spaceThread(ctx context.Context, database *db.Database, userID, spaceID, id string) (*db.SpaceConversation, error) {
	threads, err := database.SpaceConversations(ctx, userID, spaceID)
	if err != nil {
		return nil, err
	}
	for index := range threads {
		if threads[index].ID == id {
			return &threads[index], nil
		}
	}
	return nil, db.ErrSpaceNotFound
}

// items.rename renames a drawing or a thread; other kinds rename through
// their own update tools.
func (s *SpacesService) renameToolRegistration() agenttools.Registration {
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: "items.rename", Version: 1, Risk: serveragent.RiskWrite, Approval: agenttools.ApprovalNone,
			Description: "Rename a drawing or a thread. Threads also need their Space. Notes, tasks, roadmaps and albums rename through their own update tools.",
			InputSchema: agentToolSchema(map[string]any{
				"kind":  map[string]any{"type": "string", "enum": []string{"drawing", "thread"}},
				"id":    map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
				"name":  map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
				"space": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			}, []string{"kind", "id", "name"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, Idempotent: true, AuditEvent: "item.renamed",
		},
		Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			var input struct{ Kind, ID, Name, Space string }
			if json.Unmarshal(request.Arguments, &input) != nil {
				return nil, agenttools.ErrArgumentsInvalid
			}
			id, name := strings.TrimSpace(input.ID), strings.TrimSpace(input.Name)
			notFound := serveragent.ErrInvalidRequest("no " + input.Kind + " " + id + " that you can rename")
			if input.Kind == "drawing" {
				drawing, err := s.database.RenameSpaceDrawing(ctx, invocation.UserID, id, name)
				if err != nil {
					return nil, notFound
				}
				return json.Marshal(map[string]any{"renamed": "drawing", "id": drawing.ID, "name": drawing.Title})
			}
			space, err := s.spaceAdminTarget(ctx, invocation, input.Space)
			if err != nil {
				return nil, err
			}
			thread, err := spaceThread(ctx, s.database, invocation.UserID, space.ID, id)
			if err != nil {
				return nil, notFound
			}
			participants := make([]db.SpaceActorRef, 0, len(thread.Participants))
			for _, participant := range thread.Participants {
				participants = append(participants, participant.SpaceActorRef)
			}
			renamed, err := s.database.UpdateSpaceConversation(ctx, invocation.UserID, space.ID, id, name, participants)
			if err != nil {
				return nil, notFound
			}
			return json.Marshal(map[string]any{"renamed": "thread", "id": renamed.ID, "name": renamed.Title})
		},
	}
}

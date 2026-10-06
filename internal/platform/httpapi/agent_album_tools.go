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

// spaceToolRegistrations are Space-bound tools with their own handlers, beside
// the older Space data tools: threads, Library albums and roadmap plans.
func spaceToolRegistrations(database *db.Database) []agenttools.Registration {
	registrations := append(threadToolRegistrations(database), albumToolRegistrations(database)...)
	return append(registrations, roadmapPlanToolRegistration(database), roadmapCanvasToolRegistration(database), noteTagsToolRegistration(database))
}

// Library albums in one Space: one read tool and one tool for changes, so the
// catalog stays small. Account-level runs reach them with a `space` argument.
func albumToolRegistrations(database *db.Database) []agenttools.Registration {
	base := agenttools.Descriptor{Version: 1, OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer,
		AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Sources: agentToolboxSpaceSources}
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	read, organize := base, base
	read.Name, read.Risk, read.Idempotent, read.RequiredPermission = "library.albums", serveragent.RiskRead, true, db.PermissionLibraryView
	read.Description = "List the albums in the current Space's Library, or pass album_id to list that album's items."
	read.InputSchema = agentToolSchema(map[string]any{"album_id": text(200)}, nil)
	organize.Name, organize.Risk, organize.AuditEvent, organize.RequiredPermission = "library.organize", serveragent.RiskWrite, "library.album.updated", db.PermissionLibraryEdit
	organize.Description = "Organize the current Space's Library albums. Actions: create (name, description), rename (album_id, name, description), " +
		"add_items (album_id, item_ids from library_search), remove_item (album_id, item_id), restore_item (item_id from the trash)."
	organize.InputSchema = agentToolSchema(map[string]any{
		"action":      map[string]any{"type": "string", "enum": []string{"create", "rename", "add_items", "remove_item", "restore_item"}},
		"album_id":    text(200),
		"name":        text(120),
		"description": map[string]any{"type": "string", "maxLength": 2000},
		"item_ids":    map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": text(200)},
		"item_id":     text(200),
	}, []string{"action"})
	return []agenttools.Registration{
		{Descriptor: read, Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			var input struct {
				AlbumID string `json:"album_id"`
			}
			if json.Unmarshal(request.Arguments, &input) != nil {
				return nil, agenttools.ErrArgumentsInvalid
			}
			if input.AlbumID == "" {
				albums, err := database.LibraryAlbums(ctx, invocation.UserID, invocation.SpaceID)
				if err != nil {
					return nil, albumError(err)
				}
				items := make([]map[string]any, 0, len(albums))
				for _, album := range albums {
					items = append(items, map[string]any{"id": album.ID, "name": album.Name, "description": album.Description, "items": album.ItemCount})
				}
				return json.Marshal(map[string]any{"albums": items})
			}
			if _, err := database.LibraryAlbum(ctx, invocation.UserID, invocation.SpaceID, input.AlbumID); err != nil {
				return nil, albumError(err)
			}
			albumItems, err := database.LibraryAlbumItems(ctx, invocation.UserID, invocation.SpaceID, input.AlbumID, 100)
			if err != nil {
				return nil, albumError(err)
			}
			items := make([]map[string]any, 0, len(albumItems))
			for _, item := range albumItems {
				items = append(items, map[string]any{"id": item.ID, "name": item.DisplayName, "caption": item.Caption, "tags": item.Tags})
			}
			return json.Marshal(map[string]any{"album_id": input.AlbumID, "items": items})
		}},
		{Descriptor: organize, Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			return executeAlbumOrganize(ctx, database, invocation, request)
		}},
	}
}

func executeAlbumOrganize(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Action      string   `json:"action"`
		AlbumID     string   `json:"album_id"`
		Name        string   `json:"name"`
		Description *string  `json:"description"`
		ItemIDs     []string `json:"item_ids"`
		ItemID      string   `json:"item_id"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil {
		return nil, agenttools.ErrArgumentsInvalid
	}
	user, space := invocation.UserID, invocation.SpaceID
	switch input.Action {
	case "create":
		if strings.TrimSpace(input.Name) == "" {
			return nil, serveragent.ErrInvalidRequest("create needs a name")
		}
		description := ""
		if input.Description != nil {
			description = *input.Description
		}
		album, err := database.CreateLibraryAlbum(ctx, user, space, input.Name, description)
		if err != nil {
			return nil, albumError(err)
		}
		return json.Marshal(map[string]any{"album": map[string]any{"id": album.ID, "name": album.Name}})
	case "rename":
		current, err := database.LibraryAlbum(ctx, user, space, input.AlbumID)
		if err != nil {
			return nil, albumError(err)
		}
		name, description := current.Name, current.Description
		if strings.TrimSpace(input.Name) != "" {
			name = input.Name
		}
		if input.Description != nil {
			description = *input.Description
		}
		album, err := database.UpdateLibraryAlbum(ctx, user, space, current.ID, current.Version, name, description, current.CoverItemID)
		if err != nil {
			return nil, albumError(err)
		}
		return json.Marshal(map[string]any{"album": map[string]any{"id": album.ID, "name": album.Name}})
	case "add_items":
		if input.AlbumID == "" || len(input.ItemIDs) == 0 {
			return nil, serveragent.ErrInvalidRequest("add_items needs album_id and item_ids")
		}
		if err := database.AddLibraryAlbumItems(ctx, user, space, input.AlbumID, input.ItemIDs); err != nil {
			return nil, albumError(err)
		}
		return json.Marshal(map[string]any{"album_id": input.AlbumID, "added": len(input.ItemIDs)})
	case "remove_item":
		if input.AlbumID == "" || input.ItemID == "" {
			return nil, serveragent.ErrInvalidRequest("remove_item needs album_id and item_id")
		}
		if err := database.RemoveLibraryAlbumItem(ctx, user, space, input.AlbumID, input.ItemID); err != nil {
			return nil, albumError(err)
		}
		return json.Marshal(map[string]any{"album_id": input.AlbumID, "removed": input.ItemID})
	case "restore_item":
		if input.ItemID == "" {
			return nil, serveragent.ErrInvalidRequest("restore_item needs item_id")
		}
		item, err := database.RestoreLibraryItem(ctx, user, space, input.ItemID)
		if err != nil {
			return nil, albumError(err)
		}
		return json.Marshal(map[string]any{"restored": item.ID, "name": item.DisplayName})
	}
	return nil, serveragent.ErrInvalidRequest("unknown action " + input.Action)
}

// albumError explains rejections the model can correct; nothing changed.
func albumError(err error) error {
	switch {
	case errors.Is(err, db.ErrSpaceNotFound), errors.Is(err, db.ErrLibraryNotFound):
		return serveragent.ErrInvalidRequest("that album or item was not found in this Space; call library_albums or library_search")
	case errors.Is(err, db.ErrLibraryForbidden), errors.Is(err, db.ErrSpaceForbidden):
		return serveragent.ErrInvalidRequest("you do not have permission to change this Space's Library albums")
	case errors.Is(err, db.ErrSpaceInvalid), errors.Is(err, db.ErrSpaceConflict):
		return serveragent.ErrInvalidRequest("those album values are not valid, or the album changed; read it again and retry")
	}
	return err
}

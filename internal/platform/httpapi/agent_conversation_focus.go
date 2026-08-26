package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func recordAIConversationFocusFromToolResult(ctx context.Context, database *db.Database, invocation agenttools.Invocation, toolName string, result json.RawMessage) {
	if database == nil || strings.TrimSpace(invocation.SessionID) == "" || len(result) == 0 {
		return
	}
	if pending, err := database.AIConversationPendingAction(ctx, invocation.UserID, invocation.SessionID, invocation.SpaceID); err == nil {
		var candidates []string
		_ = json.Unmarshal(pending.CandidateIntents, &candidates)
		if pending.Intent == toolName || slices.Contains(candidates, toolName) {
			_ = database.ClearAIConversationPendingAction(ctx, invocation.UserID, invocation.SessionID, invocation.SpaceID)
		}
	}
	kind, candidate := focusedEntityFromToolResult(toolName, result)
	if kind == "" || candidate == nil {
		return
	}
	id := firstJSONText(candidate, "id", "user_id")
	if id == "" {
		return
	}
	label := firstJSONText(candidate, "title", "name", "display_name", "task_key")
	metadata := map[string]any{}
	for _, key := range []string{"task_key", "version", "email", "status"} {
		if value, ok := candidate[key]; ok {
			metadata[key] = value
		}
	}
	rawMetadata, _ := json.Marshal(metadata)
	_ = database.UpsertAIConversationFocus(ctx, db.AIConversationFocus{
		UserID: invocation.UserID, ConversationID: invocation.SessionID, SpaceID: invocation.SpaceID,
		EntityKind: kind, EntityID: id, Label: label, Metadata: rawMetadata,
		SourceTool: toolName, SourceRunID: invocation.RunID,
	})
}

func focusedEntityFromToolResult(toolName string, result json.RawMessage) (string, map[string]any) {
	var payload map[string]any
	if json.Unmarshal(result, &payload) != nil {
		return "", nil
	}
	kind := ""
	switch {
	case strings.HasPrefix(toolName, "tasks."):
		kind = "task"
	case toolName == toolboxMembersResolve:
		kind = "person"
	case strings.HasPrefix(toolName, "notes."):
		kind = "note"
	case strings.HasPrefix(toolName, "drawings."):
		kind = "drawing"
	case strings.HasPrefix(toolName, "calendar."):
		kind = "calendar_event"
	case strings.HasPrefix(toolName, "roadmaps."):
		kind = "roadmap"
	case strings.HasPrefix(toolName, "library."):
		kind = "library_item"
	default:
		return "", nil
	}
	if id := firstJSONText(payload, "id", "user_id"); id != "" {
		return kind, payload
	}
	collectionKeys := map[string][]string{
		"task": {"tasks"}, "person": {"matches"}, "note": {"notes"}, "drawing": {"drawings"},
		"calendar_event": {"events"}, "roadmap": {"roadmaps"}, "library_item": {"items"},
	}
	for _, key := range collectionKeys[kind] {
		items, ok := payload[key].([]any)
		if !ok || len(items) != 1 {
			continue
		}
		candidate, _ := items[0].(map[string]any)
		if firstJSONText(candidate, "id", "user_id") != "" {
			return kind, candidate
		}
	}
	for _, key := range []string{"task", "note", "drawing", "event", "roadmap", "item"} {
		candidate, _ := payload[key].(map[string]any)
		if firstJSONText(candidate, "id", "user_id") != "" {
			return kind, candidate
		}
	}
	return "", nil
}

func firstJSONText(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func recordAIConversationFocusFromUIContext(ctx context.Context, database *db.Database, userID, conversationID, spaceID string, references []aiContextReference, resolved []aiResolvedContext) error {
	if database == nil || conversationID == "" || spaceID == "" {
		return nil
	}
	labels := map[string]string{}
	for _, item := range resolved {
		labels[item.Citation.Kind+"\x00"+item.Citation.ID] = item.Citation.Title
	}
	type candidate struct{ kind, id, label string }
	byKind := map[string][]candidate{}
	for _, reference := range references {
		kind := map[string]string{
			"task": "task", "planner.task": "task", "note": "note", "notes": "note",
			"drawing": "drawing", "roadmap": "roadmap", "planner.roadmap": "roadmap",
			"library.item": "library_item",
		}[reference.Kind]
		if kind == "" || reference.SpaceID != "" && reference.SpaceID != spaceID {
			continue
		}
		label := labels[reference.Kind+"\x00"+reference.ID]
		if label == "" {
			for key, value := range labels {
				if strings.HasSuffix(key, "\x00"+reference.ID) {
					label = value
					break
				}
			}
		}
		byKind[kind] = append(byKind[kind], candidate{kind: kind, id: reference.ID, label: label})
	}
	for _, candidates := range byKind {
		if len(candidates) != 1 {
			continue
		}
		item := candidates[0]
		if err := database.UpsertAIConversationFocus(ctx, db.AIConversationFocus{
			UserID: userID, ConversationID: conversationID, SpaceID: spaceID,
			EntityKind: item.kind, EntityID: item.id, Label: item.label, Metadata: json.RawMessage(`{}`),
			SourceTool: "ui.context",
		}); err != nil {
			return err
		}
	}
	return nil
}

func TestingRecordAIConversationFocusFromUIContext(ctx context.Context, database *db.Database, userID, conversationID, spaceID string, referencesJSON json.RawMessage) error {
	var references []aiContextReference
	if json.Unmarshal(referencesJSON, &references) != nil {
		return db.ErrSpaceInvalid
	}
	resolved, err := (aiContextBroker{database: database}).resolve(ctx, userID, references)
	if err != nil {
		return err
	}
	return recordAIConversationFocusFromUIContext(ctx, database, userID, conversationID, spaceID, references, resolved)
}

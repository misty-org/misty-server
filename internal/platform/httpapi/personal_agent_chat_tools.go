package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
)

const toolboxMessagesSend = "message.send"

// permittedSpaceConversationRead reports whether the person may read the
// Space. Their agents read with the same access.
func permittedSpaceConversationRead(ctx context.Context, database *db.Database, userID, spaceID, _ string, permission string) (bool, error) {
	return database.HasSpacePermission(ctx, userID, spaceID, permission)
}

type spaceConversationToolActor struct {
	userID         string
	spaceID        string
	agentID        string
	runID          string
	sessionID      string
	conversationID string
	// memorySpaceID is the Space whose memory tree the run uses (see
	// agenttools.Invocation.MemorySpaceID).
	memorySpaceID string
	// memorySearch ranks memory by meaning; nil lists by recency instead.
	memorySearch memorySearchFunc
	// service runs the browser tools with the server's model client (browser.extract).
	service *SpacesService
}

func executeSpaceConversationTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, originalPrompt string, tool agent.ToolRequest) (json.RawMessage, error) {
	if result, handled, err := executeAgentMemoryTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentNoteTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentDrawingTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if result, handled, err := executeAgentLibraryTool(ctx, database, actor, tool); handled {
		return result, err
	}
	if tool.Name == toolboxContextGet {
		return executeAgentContextGet(ctx, database, actor, originalPrompt)
	}
	if tool.Name == toolboxMembersList || tool.Name == toolboxMembersResolve {
		return executeAgentMembersTool(ctx, database, actor, tool)
	}
	if tool.Name == toolboxMessagesSend {
		return executeAgentMessageSend(ctx, database, actor, originalPrompt, tool)
	}
	if tool.Name == "space.search_messages" || tool.Name == "file.search" {
		return executeSpaceSearchTool(ctx, database, actor, tool)
	}
	return nil, workflow.ErrCapabilityDenied
}

func agentToolTimezone(originalInput string) string {
	var envelope struct {
		Timezone string `json:"timezone"`
	}
	if json.Unmarshal([]byte(originalInput), &envelope) == nil {
		timezone := strings.TrimSpace(envelope.Timezone)
		if timezone != "" {
			if _, err := time.LoadLocation(timezone); err == nil {
				return timezone
			}
		}
	}
	return "UTC"
}

func TestingAgentToolTimezone(originalInput string) string {
	return agentToolTimezone(originalInput)
}

func parseAgentToolTime(value, field, timezone string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	location, locationErr := time.LoadLocation(firstNonEmpty(strings.TrimSpace(timezone), "UTC"))
	if locationErr != nil {
		return nil, agent.ErrInvalidRequest("timezone must be a valid IANA timezone such as America/Los_Angeles")
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if local, localErr := time.ParseInLocation(layout, value, location); localErr == nil {
			local = local.UTC()
			return &local, nil
		}
	}
	if local, localErr := time.ParseInLocation("2006-01-02", value, location); localErr == nil {
		// A date-only due date means the end of that day in the creator's
		// timezone, rather than midnight UTC (which can display a day early).
		local = time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 0, 0, location).UTC()
		return &local, nil
	}
	return nil, agent.ErrInvalidRequest(field + " must be an ISO 8601 date or timestamp")
}

func TestingParseAgentToolTime(value, timezone string) (*time.Time, error) {
	return parseAgentToolTime(value, "dueAt", timezone)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizedAgentMembers(members []db.SpaceMember) []map[string]string {
	items := make([]map[string]string, 0, len(members))
	for _, member := range members {
		items = append(items, map[string]string{"user_id": member.UserID, "name": member.Name, "role": member.Role})
	}
	return items
}

func resolveAgentMembers(members []db.SpaceMember, query string) []db.SpaceMember {
	wanted := normalizeGroundingText(query)
	exact := []db.SpaceMember{}
	partial := []db.SpaceMember{}
	for _, member := range members {
		name := normalizeGroundingText(member.Name)
		email := normalizeGroundingText(member.Email)
		if wanted == name || wanted == email {
			exact = append(exact, member)
			continue
		}
		if strings.Contains(name, wanted) || strings.Contains(email, wanted) {
			partial = append(partial, member)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func (s *SpacesService) explicitMessageFileContext(ctx context.Context, userID string, membership *db.AskExecutionContext, spaceID string, attachmentIDs, libraryItemIDs []string) (string, string, []workflow.ContentRef) {
	refs := make([]explicitAttachmentRef, 0, len(attachmentIDs)+len(libraryItemIDs))
	for _, id := range attachmentIDs {
		refs = append(refs, explicitAttachmentRef{Kind: "chat_attachment", ResourceID: id})
	}
	for _, id := range libraryItemIDs {
		refs = append(refs, explicitAttachmentRef{Kind: "library_item", ResourceID: id})
	}
	return s.explicitAttachmentContext(ctx, userID, spaceID, refs)
}

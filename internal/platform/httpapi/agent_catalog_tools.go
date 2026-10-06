package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Read tools that cover Misty features with no other tool: one search across
// every kind of item, and the account's devices.
const (
	searchAllTool   = "search.all"
	devicesListTool = "devices.list"
)

func agentToolSchema(properties map[string]any, required []string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
}

func searchAllToolRegistration(database *db.Database) agenttools.Registration {
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: searchAllTool, Version: 1, Risk: serveragent.RiskRead, Approval: agenttools.ApprovalNone,
			Description: "Search notes, tasks, roadmaps, calendar events and messages at once, ranked by relevance. Use it when you do not know where something is; then read the item with its own tool.",
			InputSchema: agentToolSchema(map[string]any{
				"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
				"space": map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "Space name or id. Omit to search every Space you can access."},
				"kinds": map[string]any{"type": "array", "maxItems": 5, "items": map[string]any{"type": "string", "enum": []string{"note", "task", "roadmap", "calendar", "message"}}},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			}, []string{"query"}),
			OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, AllowCustomAgent: true, Idempotent: true,
		},
		Handler: func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			return executeSearchAll(ctx, database, invocation, request)
		},
	}
}

func executeSearchAll(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
	var input struct {
		Query string   `json:"query"`
		Space string   `json:"space"`
		Kinds []string `json:"kinds"`
		Limit int      `json:"limit"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Query) == "" {
		return nil, agenttools.ErrArgumentsInvalid
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	spaces, err := database.ListSpaces(ctx, invocation.UserID)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, space := range spaces {
		names[space.ID] = space.Name
	}
	// A Space-bound run searches only its Space; others may name one.
	spaceID := invocation.SpaceID
	if spaceID == "" && strings.TrimSpace(input.Space) != "" {
		targets, err := routedSpaceTargets(spaces, invocation.UserID, strings.TrimSpace(input.Space), true, false)
		if err != nil {
			return nil, err
		}
		spaceID = targets[0].ID
	}
	kinds := map[string]bool{}
	for _, kind := range input.Kinds {
		kinds[kind] = true
	}
	hits, err := database.SearchAIRetrieval(ctx, invocation.UserID, input.Query, nil, 100, spaceID)
	if err != nil {
		return nil, err
	}
	results, seen := []map[string]any{}, map[string]bool{}
	for _, hit := range hits {
		kind := hit.SourceKind
		if kind == "provider" {
			kind = "message"
		}
		key := hit.SourceKind + ":" + hit.SourceID
		if seen[key] || (len(kinds) > 0 && !kinds[kind]) {
			continue
		}
		seen[key] = true
		results = append(results, map[string]any{
			"kind": kind, "id": hit.SourceID, "title": hit.Title, "excerpt": aiExcerpt(hit.Content),
			"space_id": hit.SpaceID, "space": names[hit.SpaceID],
		})
		if len(results) >= input.Limit {
			break
		}
	}
	return json.Marshal(map[string]any{"results": results})
}

func devicesListToolRegistration(database *db.Database) agenttools.Registration {
	return agenttools.Registration{
		Descriptor: agenttools.Descriptor{
			Name: devicesListTool, Version: 1, Risk: serveragent.RiskRead, Approval: agenttools.ApprovalNone,
			Description: "List this account's signed-in devices with their platform and when each was last online.",
			InputSchema: agentToolSchema(map[string]any{}, nil), OutputSchema: agentToolObjectOutputSchema(),
			Locality: agenttools.LocalityServer, AllowCustomAgent: true, Idempotent: true,
		},
		Handler: func(ctx context.Context, invocation agenttools.Invocation, _ serveragent.ToolRequest) (json.RawMessage, error) {
			devices, err := database.TrustedDevices(invocation.UserID)
			if err != nil {
				return nil, err
			}
			items := []map[string]any{}
			for _, device := range devices {
				if device.RevokedAt != nil {
					continue
				}
				items = append(items, map[string]any{
					"id": device.ID, "name": device.Name, "platform": device.Platform,
					"last_seen": device.LastSeenAt.UTC().Format(time.RFC3339),
				})
			}
			return json.Marshal(map[string]any{"devices": items})
		},
	}
}

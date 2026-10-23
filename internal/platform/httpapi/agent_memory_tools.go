package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
)

const (
	toolboxMemoryRemember = "memory.pref.remember"
	toolboxMemoryForget   = "memory.pref.forget"
)

func memoryAgentToolDescriptors() []agenttools.Descriptor {
	return []agenttools.Descriptor{
		{Name: "memory.pref.list", Version: 1, Description: "Review what this agent remembers for the user in this Space. With a query, returns the memories closest in meaning first, so \"seat preference\" finds \"I like aisle seats\".", Risk: agent.RiskRead, InputSchema: MustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "maxLength": 500}}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPrivate, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: "memory.pref.update", Version: 1, Description: "Correct a durable preference only when the user explicitly asks to change what is remembered. Preserve the existing scope. Use the memory ID from memory_pref_list.", Risk: agent.RiskWrite, InputSchema: MustAPIRawJSON(map[string]any{"type": "object", "required": []string{"memoryId", "content"}, "properties": map[string]any{"memoryId": map[string]any{"type": "string", "maxLength": 200}, "content": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000}}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema(), Layer: agenttools.LayerPrivate, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources, AuditEvent: "misty.memory.updated"},
		{
			Name: toolboxMemoryRemember, Version: 1,
			Description: "Remember a concise fact, preference, or standing instruction only when the user explicitly asks Misty to remember it. Never store credentials, secrets, financial identifiers, health records, or inferred sensitive traits.",
			Risk:        agent.RiskWrite,
			InputSchema: MustAPIRawJSON(map[string]any{
				"type": "object", "required": []string{"content", "kind", "scope"},
				"properties": map[string]any{
					"content": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
					"kind":    map[string]any{"type": "string", "enum": []string{"fact", "preference", "instruction"}},
					"scope":   map[string]any{"type": "string", "enum": []string{"personal"}, "description": "Memories are private to the user and kept in the Space this run is in."},
					"reason":  map[string]any{"type": "string", "maxLength": 500},
				}, "additionalProperties": false,
			}),
			OutputSchema: agentToolObjectOutputSchema(),
			Layer:        agenttools.LayerPrivate, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "misty.memory.remembered",
			Sources: agentToolboxSpaceSources,
		},
		{
			Name: toolboxMemoryForget, Version: 1,
			Description: "Forget one remembered item when the user explicitly asks. Use the exact memory ID provided in remembered context.",
			Risk:        agent.RiskWrite,
			InputSchema: MustAPIRawJSON(map[string]any{
				"type": "object", "required": []string{"memoryId"},
				"properties":           map[string]any{"memoryId": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}},
				"additionalProperties": false,
			}),
			OutputSchema: agentToolObjectOutputSchema(),
			Layer:        agenttools.LayerPrivate, Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "misty.memory.forgotten",
			Sources: agentToolboxSpaceSources,
		},
	}
}

func executeAgentMemoryTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool agent.ToolRequest) (json.RawMessage, bool, error) {
	if tool.Name != toolboxMemoryRemember && tool.Name != toolboxMemoryForget && tool.Name != "memory.pref.list" && tool.Name != "memory.pref.update" {
		return nil, false, nil
	}
	if database == nil {
		return nil, true, workflow.ErrCapabilityDenied
	}
	// Every memory tool reads and writes only the run's Space.
	space, err := actorMemorySpace(ctx, database, actor)
	if err != nil {
		return nil, true, err
	}
	if tool.Name == "memory.pref.list" {
		var input struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(tool.Arguments, &input)
		items, err := database.MistyMemories(ctx, actor.userID, space, 100, actor.agentID)
		if err != nil {
			return nil, true, err
		}
		if query := strings.TrimSpace(input.Query); query != "" && actor.memorySearch != nil {
			hits, err := actor.memorySearch(ctx, actor.userID, space, []string{db.MemoryFacts}, query, 20)
			if err != nil {
				return nil, true, err
			}
			items = factsInHitOrder(items, hits)
		}
		result, err := json.Marshal(map[string]any{"memories": items})
		return result, true, err
	}
	if tool.Name == "memory.pref.update" {
		var input struct {
			MemoryID string `json:"memoryId"`
			Content  string `json:"content"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || mistyMemoryLooksSensitive(input.Content) {
			return nil, true, db.ErrSpaceInvalid
		}
		if err := database.UpdateAgentMemory(ctx, actor.userID, actor.agentID, space, input.MemoryID, input.Content); err != nil {
			return nil, true, err
		}
		return MustAPIRawJSON(map[string]any{"updated": true, "memory_id": input.MemoryID}), true, nil
	}
	if tool.Name == toolboxMemoryForget {
		var input struct {
			MemoryID string `json:"memoryId"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.MemoryID) == "" {
			return nil, true, db.ErrSpaceInvalid
		}
		if err := database.ForgetMistyMemory(ctx, actor.userID, space, input.MemoryID, actor.agentID); err != nil {
			return nil, true, err
		}
		return MustAPIRawJSON(map[string]any{"forgotten": true, "memory_id": input.MemoryID}), true, nil
	}
	var input struct {
		Content string `json:"content"`
		Kind    string `json:"kind"`
		Scope   string `json:"scope"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, true, db.ErrSpaceInvalid
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Scope != "" && input.Scope != "personal" {
		return nil, true, db.ErrSpaceInvalid
	}
	if mistyMemoryLooksSensitive(input.Content) {
		return nil, true, agent.ErrInvalidRequest("secrets and credentials are never remembered")
	}
	item, err := database.RememberMistyMemory(ctx, actor.userID, db.RememberMistyMemoryInput{
		AgentID: actor.agentID, SpaceID: space, Kind: input.Kind, Content: input.Content, Reason: input.Reason,
		SourceConversationID: actor.sessionID, SourceInvocationID: actor.runID,
	})
	if err != nil {
		return nil, true, err
	}
	return MustAPIRawJSON(map[string]any{
		"remembered": true, "memory_id": item.ID, "scope": input.Scope, "kind": item.Kind,
	}), true, nil
}

// factsInHitOrder keeps the memories search found, nearest in meaning first.
// While vectors are still being made it returns them all, newest first.
func factsInHitOrder(items []db.MistyMemory, hits []db.MemoryHit) []db.MistyMemory {
	if len(hits) == 0 {
		return items
	}
	byID := make(map[string]db.MistyMemory, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	ordered := make([]db.MistyMemory, 0, len(hits))
	for _, hit := range hits {
		if item, ok := byID[hit.RecordID]; ok {
			ordered = append(ordered, item)
		}
	}
	return ordered
}

func mistyMemoryLooksSensitive(content string) bool {
	value := strings.ToLower(content)
	for _, marker := range []string{"password", "passcode", "api key", "secret key", "access token", "private key", "seed phrase", "credit card", "social security", "ssn"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func TestingMistyMemoryLooksSensitive(content string) bool {
	return mistyMemoryLooksSensitive(content)
}

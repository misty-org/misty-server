package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

const (
	toolboxMemoryRemember = "memory.remember"
	toolboxMemoryForget   = "memory.forget"
)

func memoryAgentToolDescriptors() []agenttools.Descriptor {
	return []agenttools.Descriptor{
		{Name: "memory.list", Version: 1, Description: "Review this agent's account-wide remembered preferences.", Risk: serveragent.RiskRead, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: "memory.update", Version: 1, Description: "Correct a durable preference only when the user explicitly asks to change what is remembered. Preserve the existing scope. Use the memory ID from memory_list.", Risk: serveragent.RiskWrite, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "required": []string{"memoryId", "content"}, "properties": map[string]any{"memoryId": map[string]any{"type": "string", "maxLength": 200}, "content": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000}}, "additionalProperties": false}), OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalExplicitIntent, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources, AuditEvent: "misty.memory.updated"},
		{
			Name: toolboxMemoryRemember, Version: 1,
			Description: "Remember a concise fact, preference, or standing instruction only when the user explicitly asks Misty to remember it. Never store credentials, secrets, financial identifiers, health records, or inferred sensitive traits.",
			Risk:        serveragent.RiskWrite,
			InputSchema: TestingMustAPIRawJSON(map[string]any{
				"type": "object", "required": []string{"content", "kind", "scope"},
				"properties": map[string]any{
					"content": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
					"kind":    map[string]any{"type": "string", "enum": []string{"fact", "preference", "instruction"}},
					"scope":   map[string]any{"type": "string", "enum": []string{"personal"}},
					"reason":  map[string]any{"type": "string", "maxLength": 500},
				}, "additionalProperties": false,
			}),
			OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalExplicitIntent,
			Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "misty.memory.remembered",
			Sources: agentToolboxSpaceSources,
		},
		{
			Name: toolboxMemoryForget, Version: 1,
			Description: "Forget one remembered item when the user explicitly asks. Use the exact memory ID provided in remembered context.",
			Risk:        serveragent.RiskWrite,
			InputSchema: TestingMustAPIRawJSON(map[string]any{
				"type": "object", "required": []string{"memoryId"},
				"properties":           map[string]any{"memoryId": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}},
				"additionalProperties": false,
			}),
			OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalExplicitIntent,
			Locality: agenttools.LocalityServer, Idempotent: true, AuditEvent: "misty.memory.forgotten",
			Sources: agentToolboxSpaceSources,
		},
	}
}

func executeAgentMemoryTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, bool, error) {
	if tool.Name != toolboxMemoryRemember && tool.Name != toolboxMemoryForget && tool.Name != "memory.list" && tool.Name != "memory.update" {
		return nil, false, nil
	}
	if database == nil {
		return nil, true, workflowv2.ErrCapabilityDenied
	}
	if tool.Name == "memory.list" {
		items, err := database.MistyMemories(ctx, actor.userID, actor.spaceID, 100, actor.agentID)
		if err != nil {
			return nil, true, err
		}
		result, err := json.Marshal(map[string]any{"memories": items})
		return result, true, err
	}
	if tool.Name == "memory.update" {
		var input struct {
			MemoryID string `json:"memoryId"`
			Content  string `json:"content"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || mistyMemoryLooksSensitive(input.Content) {
			return nil, true, db.ErrSpaceInvalid
		}
		if err := database.UpdateAgentMemory(ctx, actor.userID, actor.agentID, actor.spaceID, input.MemoryID, input.Content); err != nil {
			return nil, true, err
		}
		return TestingMustAPIRawJSON(map[string]any{"updated": true, "memory_id": input.MemoryID}), true, nil
	}
	if tool.Name == toolboxMemoryForget {
		var input struct {
			MemoryID string `json:"memoryId"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.MemoryID) == "" {
			return nil, true, db.ErrSpaceInvalid
		}
		if err := database.ForgetMistyMemory(ctx, actor.userID, input.MemoryID, actor.agentID); err != nil {
			return nil, true, err
		}
		return TestingMustAPIRawJSON(map[string]any{"forgotten": true, "memory_id": input.MemoryID}), true, nil
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
		return nil, true, serveragent.ErrInvalidRequest("secrets and credentials are never remembered")
	}
	spaceID := ""
	if input.Scope == "space" {
		spaceID = actor.spaceID
	}
	item, err := database.RememberMistyMemory(ctx, actor.userID, db.RememberMistyMemoryInput{
		AgentID: actor.agentID, SpaceID: spaceID, Kind: input.Kind, Content: input.Content, Reason: input.Reason,
		SourceConversationID: actor.sessionID, SourceInvocationID: actor.runID,
	})
	if err != nil {
		return nil, true, err
	}
	return TestingMustAPIRawJSON(map[string]any{
		"remembered": true, "memory_id": item.ID, "scope": input.Scope, "kind": item.Kind,
	}), true, nil
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

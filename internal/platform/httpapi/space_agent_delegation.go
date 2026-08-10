package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

const maxSpaceAgentDelegationDepth = 3

func (s *SpacesService) spaceAgentDelegationHandler(
	requestingUserID, spaceID, conversationID, sourceMessageID, delegatingAgentID string,
	depth int,
) agenttools.Handler {
	return func(ctx context.Context, invocation agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
		if depth > maxSpaceAgentDelegationDepth {
			return nil, workflowv2.ErrCapabilityDenied
		}
		var input struct {
			Prompt    string `json:"prompt"`
			AgentID   string `json:"agent_id"`
			AgentName string `json:"agent_name"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, db.ErrSpaceInvalid
		}
		input.Prompt = strings.TrimSpace(input.Prompt)
		input.AgentID = strings.TrimSpace(input.AgentID)
		input.AgentName = strings.TrimSpace(input.AgentName)
		if input.Prompt == "" {
			return nil, db.ErrSpaceInvalid
		}
		membership, choices, err := resolveSpaceDelegationTarget(
			ctx, s.database, requestingUserID, spaceID, input.AgentID, input.AgentName, invocation.OriginalInput,
		)
		if err != nil {
			return nil, err
		}
		if membership == nil {
			return TestingMustAPIRawJSON(map[string]any{
				"status": "needs_clarification", "question": "Which Space agent should handle this?", "agents": choices,
			}), nil
		}
		if membership.AgentID == delegatingAgentID || !delegatedAgentTargetGrounded(invocation.OriginalInput, membership) {
			return nil, workflowv2.ErrCapabilityDenied
		}
		reply, runID, err := s.runMentionedAgentAtDepth(
			ctx, requestingUserID, spaceID, conversationID, membership.AgentID, sourceMessageID, "delegation",
			[]db.MessageSpan{{Type: "text", Text: input.Prompt}}, nil, nil, nil, depth,
		)
		if err != nil {
			return nil, err
		}
		return TestingMustAPIRawJSON(map[string]any{
			"status": "completed", "agent_id": membership.AgentID, "agent_name": membership.Name,
			"run_id": runID, "message_id": reply.ID,
		}), nil
	}
}

func resolveSpaceDelegationTarget(
	ctx context.Context,
	database *db.Database,
	userID, spaceID, requestedID, requestedName, originalPrompt string,
) (*db.SpaceAgentMembership, []map[string]string, error) {
	memberships, err := database.SpaceAgentMemberships(ctx, userID, spaceID)
	if err != nil {
		return nil, nil, err
	}
	active := make([]db.SpaceAgentMembership, 0, len(memberships))
	for _, membership := range memberships {
		if !membership.Enabled {
			continue
		}
		if _, accessErr := database.PersonalAgentForSpace(ctx, userID, spaceID, membership.AgentID); accessErr != nil {
			if errors.Is(accessErr, db.ErrPersonalAgentNotFound) || errors.Is(accessErr, db.ErrSpaceForbidden) {
				continue
			}
			return nil, nil, accessErr
		}
		active = append(active, membership)
	}
	for index := range active {
		if requestedID != "" && active[index].AgentID == requestedID || requestedName != "" && strings.EqualFold(active[index].Name, requestedName) {
			return &active[index], nil, nil
		}
	}
	if requestedID != "" || requestedName != "" {
		return nil, nil, workflowv2.ErrCapabilityDenied
	}
	matched := make([]db.SpaceAgentMembership, 0, len(active))
	for _, membership := range active {
		if containsGroundingPhrase(originalPrompt, membership.Name) || containsGroundingPhrase(originalPrompt, membership.AgentID) {
			matched = append(matched, membership)
		}
	}
	if len(matched) == 1 {
		return &matched[0], nil, nil
	}
	if len(active) == 1 {
		return &active[0], nil, nil
	}
	choices := make([]map[string]string, 0, len(active))
	for _, membership := range active {
		choices = append(choices, map[string]string{
			"agent_id": membership.AgentID, "name": membership.Name, "role": membership.SpaceRole,
		})
	}
	return nil, choices, nil
}

func delegatedAgentTargetGrounded(prompt string, membership *db.SpaceAgentMembership) bool {
	if membership == nil || !explicitAgentDelegationIntent(strings.ToLower(prompt)) {
		return false
	}
	return containsGroundingPhrase(prompt, membership.Name) ||
		containsGroundingPhrase(prompt, membership.AgentID) ||
		strings.Contains(strings.ToLower(prompt), "the agent") ||
		strings.Contains(strings.ToLower(prompt), "the teammate")
}

func TestingResolveSpaceDelegationTarget(
	ctx context.Context,
	database *db.Database,
	userID, spaceID, requestedID, requestedName, originalPrompt string,
) (*db.SpaceAgentMembership, []map[string]string, error) {
	return resolveSpaceDelegationTarget(ctx, database, userID, spaceID, requestedID, requestedName, originalPrompt)
}

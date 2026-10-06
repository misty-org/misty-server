package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// appsApproval waits for the user's decision on one exact action when the
// account asks first. It returns the approval to consume, or a result that
// tells the model the card is still waiting.
func (s *SpacesService) appsApproval(ctx context.Context, invocation agenttools.Invocation, info composio.ToolInfo, call appsCall) (*db.AgentAppRequest, json.RawMessage, error) {
	settings, _, err := s.database.AISettings(ctx, invocation.UserID)
	if err != nil {
		return nil, nil, err
	}
	if !settings.AppActionsAsk {
		return nil, nil, nil
	}
	title, summary := appsActionSummary(info, call)
	pending, err := s.database.OpenAgentAppRequest(ctx, db.AgentAppRequest{
		OwnerUserID: invocation.UserID, RunID: invocation.RunID, Kind: "approve", Subject: info.Slug,
		ArgumentsHash: appsArgumentsHash(call), Title: title, Summary: summary,
	}, appsRequestTTL)
	if err != nil {
		return nil, nil, err
	}
	if pending.State == "pending" {
		s.showAppRequest(ctx, invocation.UserID, invocation.RunID, pending)
		if _, err := appsWaitFor(ctx, appsWait, time.Second, func() (bool, error) {
			current, err := s.database.AgentAppRequest(ctx, invocation.UserID, pending.ID)
			if err == nil {
				pending = current
			}
			return pending.State != "pending", err
		}); err != nil {
			return nil, nil, err
		}
	}
	switch pending.State {
	case "approved":
		return pending, nil, nil
	case "pending":
		return nil, TestingMustAPIRawJSON(map[string]any{
			"status": "awaiting_approval", "request": pending.ID,
			"message":      "An approval card for “" + title + "” is showing in the chat. This run ends here and Misty continues after the user decides.",
			"user_message": "“" + title + "” is waiting for your approval above. Misty continues after you decide.",
		}), nil
	case "declined":
		return nil, nil, serveragent.ErrInvalidRequest("The user declined “" + title + "”. Do not retry it; continue without it or ask the user.")
	}
	return nil, nil, serveragent.ErrInvalidRequest("The approval request expired. Call apps_execute again to ask the user again.")
}

// appsActionSummary is the approval card: the app and action, then the exact
// arguments the user is approving.
func appsActionSummary(info composio.ToolInfo, call appsCall) (string, string) {
	app := strings.TrimSpace(info.Toolkit.Name)
	if app == "" {
		app = info.Toolkit.Slug
	}
	action := strings.TrimSpace(info.Name)
	if action == "" {
		action = strings.ToLower(strings.ReplaceAll(info.Slug, "_", " "))
	}
	title := truncateAgentRuntimeText(app+" · "+action, 200)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(call.Arguments, &fields)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []string{}
	for index, key := range keys {
		if index == 10 {
			lines = append(lines, fmt.Sprintf("…and %d more fields", len(keys)-index))
			break
		}
		var text string
		if json.Unmarshal(fields[key], &text) != nil {
			text = string(fields[key])
		}
		lines = append(lines, key+": "+truncateAgentRuntimeText(strings.TrimSpace(text), 400))
	}
	if call.Account != "" {
		lines = append(lines, "account: "+call.Account)
	}
	return title, truncateAgentRuntimeText(strings.Join(lines, "\n"), 3800)
}

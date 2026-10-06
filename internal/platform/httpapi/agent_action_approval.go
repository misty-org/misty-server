package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// confirmAgentAction applies the account's "Ask before acting for you" setting
// to Misty's own consequential actions: invites, member removals, leaving a
// Space and deletes. It shows the same approval card connected apps use and
// holds the call while the user decides. It returns the approval to consume
// before acting, a result telling the model the card is still waiting, or
// nothing when the account does not ask first.
func (s *SpacesService) confirmAgentAction(ctx context.Context, invocation agenttools.Invocation, subject string, arguments any, title, summary string) (*db.AgentAppRequest, json.RawMessage, error) {
	settings, _, err := s.database.AISettings(ctx, invocation.UserID)
	if err != nil {
		return nil, nil, err
	}
	if !settings.AppActionsAsk {
		return nil, nil, nil
	}
	canonical, _ := json.Marshal([]any{subject, arguments})
	digest := sha256.Sum256(canonical)
	pending, err := s.database.OpenAgentAppRequest(ctx, db.AgentAppRequest{
		OwnerUserID: invocation.UserID, RunID: invocation.RunID, Kind: "approve", Subject: subject,
		ArgumentsHash: hex.EncodeToString(digest[:]), Title: truncateAgentRuntimeText(title, 200), Summary: truncateAgentRuntimeText(summary, 3800),
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
			"message":      "An approval card for “" + title + "” is showing in the chat. This run ends here and continues after the user decides.",
			"user_message": "“" + title + "” is waiting for your approval above.",
		}), nil
	case "declined":
		return nil, nil, serveragent.ErrInvalidRequest("The user declined “" + title + "”. Do not retry it; continue without it or ask the user.")
	}
	return nil, nil, serveragent.ErrInvalidRequest("The approval request expired. Call the tool again to ask the user again.")
}

// useAgentActionApproval consumes an approval right before the action runs,
// so one approval covers exactly one action.
func (s *SpacesService) useAgentActionApproval(ctx context.Context, userID string, approval *db.AgentAppRequest) error {
	if approval == nil {
		return nil
	}
	used, err := s.database.ResolveAgentAppRequest(ctx, userID, approval.ID, "approved", "used")
	if err != nil {
		return err
	}
	if !used {
		return serveragent.ErrInvalidRequest("that approval was already used; call the tool again to ask the user again")
	}
	return nil
}

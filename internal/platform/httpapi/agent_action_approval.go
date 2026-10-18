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

// confirmAgentAction applies the run's Ask or Auto to one of Misty's own
// consequential actions: invites, member removals, leaving a Space, deletes and
// on-screen actions. need names the action and the one resource it touches.
// It returns the approval to consume before acting, a result telling the model
// the card is still waiting, or nothing when the action may continue.
func (s *SpacesService) confirmAgentAction(ctx context.Context, invocation agenttools.Invocation, need grantNeed, subject string, arguments any, title, summary string) (*db.AgentAppRequest, json.RawMessage, error) {
	canonical, _ := json.Marshal([]any{subject, arguments})
	digest := sha256.Sum256(canonical)
	return s.gateAction(ctx, invocation, need, subject, hex.EncodeToString(digest[:]), title, summary)
}

// gateAction decides one action and, when it pauses, shows the approval card
// and holds the call while the person decides. The card records what
// remembering it would grant, derived here from the call, never from the answer.
func (s *SpacesService) gateAction(ctx context.Context, invocation agenttools.Invocation, need grantNeed, subject, argumentsHash, title, summary string) (*db.AgentAppRequest, json.RawMessage, error) {
	decided, err := s.decideGrant(ctx, invocation, need)
	if err != nil {
		return nil, nil, err
	}
	switch decided.decision {
	case serveragent.DecisionAllow:
		return nil, nil, nil
	case serveragent.DecisionDeny:
		return nil, nil, serveragent.ErrInvalidRequest("“" + title + "” is not allowed for this agent. Do not retry it; continue without it or ask the user.")
	}
	title, summary = truncateAgentRuntimeText(title, 200), truncateAgentRuntimeText(summary, 3800)
	request := db.AgentAppRequest{
		OwnerUserID: invocation.UserID, RunID: invocation.RunID, Kind: "approve", Subject: subject,
		ArgumentsHash: argumentsHash, Title: title, Summary: summary, PermissionMode: decided.mode,
	}
	if decided.agentID != "" && need.rememberable() {
		request.AgentID, request.Rememberable = decided.agentID, true
		request.GrantAction, request.GrantScopeKind, request.GrantScopeID = string(need.action), string(need.scopeKind), need.scopeID
		request.GrantConstraints, request.RememberLabel = need.constraints(), truncateAgentRuntimeText(need.label, 300)
	}
	pending, err := s.database.OpenAgentAppRequest(ctx, request, appsRequestTTL)
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
	// An approval that timed out counts as declined.
	return nil, nil, serveragent.ErrInvalidRequest("The approval request for “" + title + "” expired without an answer, which counts as declined. Call the tool again to ask the user again.")
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

package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// grantNeed is what one action would need from the person: the action, the
// one resource it touches, and for payments the amount. The server derives it
// from the call it is about to run; nothing here comes from the person.
type grantNeed struct {
	action    serveragent.GrantAction
	scopeKind serveragent.ScopeKind
	scopeID   string
	// label names the scope for people, e.g. "push to misty-org/misty".
	label  string
	amount *grantAmount
}

// grantAmount is a payment's amount, and a payment grant's per-payment cap.
type grantAmount struct {
	AmountMinor int64  `json:"max_amount_minor"`
	Currency    string `json:"currency"`
}

func grantAmountFrom(raw json.RawMessage) *grantAmount {
	var out grantAmount
	if json.Unmarshal(raw, &out) != nil || out.Currency == "" || out.AmountMinor <= 0 {
		return nil
	}
	return &out
}

// rememberable reports whether this need can become a grant: a concrete scope
// of the right kind, and a payment with a known amount to cap it at.
func (need grantNeed) rememberable() bool {
	if !serveragent.ValidGrantScope(need.action, need.scopeKind, need.scopeID) {
		return false
	}
	return need.action != serveragent.GrantPayment || (need.amount != nil && need.amount.AmountMinor > 0 && need.amount.Currency != "")
}

func (need grantNeed) constraints() json.RawMessage {
	if need.action == serveragent.GrantPayment && need.amount != nil {
		raw, _ := json.Marshal(need.amount)
		return raw
	}
	return json.RawMessage(`{}`)
}

// covers reports whether a stored grant's constraints allow this exact action.
func (need grantNeed) covers(grant *db.AgentGrant) bool {
	if grant == nil {
		return false
	}
	if need.action != serveragent.GrantPayment {
		return true
	}
	limit := grantAmountFrom(grant.Constraints)
	return limit != nil && need.amount != nil && strings.EqualFold(limit.Currency, need.amount.Currency) && need.amount.AmountMinor <= limit.AmountMinor
}

// grantDecision is Decide's answer plus what a pause should record.
type grantDecision struct {
	decision serveragent.Decision
	mode     string
	agentID  string
}

// decideGrant applies the permission rule to one action. The grantor is the
// person whose resources the run acts on (invocation.UserID: the run owner, or
// the Space owner for another member's request), never whoever pays for it.
// Grants are read fresh on every call so a revoke applies immediately.
func (s *SpacesService) decideGrant(ctx context.Context, invocation agenttools.Invocation, need grantNeed) (grantDecision, error) {
	out := grantDecision{mode: s.invocationPermissionMode(ctx, invocation)}
	out.agentID = s.grantAgentID(ctx, invocation)
	class := serveragent.ClassOf(need.action)
	covered := false
	if class == serveragent.ClassPermission && out.mode == serveragent.PermissionAuto && out.agentID != "" && need.rememberable() {
		grant, err := s.database.ActiveAgentGrant(ctx, invocation.UserID, out.agentID, string(need.action), string(need.scopeKind), need.scopeID)
		if err != nil {
			return out, err
		}
		if covered = need.covers(grant); covered {
			_ = s.database.TouchAgentGrant(ctx, invocation.UserID, grant.ID)
		}
	}
	out.decision = serveragent.Decide(out.mode, class, covered)
	return out, nil
}

// invocationPermissionMode is the run's frozen Ask or Auto, or the agent's
// default for runs admitted before modes were frozen. Unknown means Ask.
func (s *SpacesService) invocationPermissionMode(ctx context.Context, invocation agenttools.Invocation) string {
	if mode := serveragent.NormalizePermissionMode(invocation.PermissionMode); mode != "" {
		return mode
	}
	return s.agentPermissionMode(ctx, invocation.UserID, invocation.AgentID)
}

// agentPermissionMode is an agent's default Ask or Auto. Chats without an
// agent use the person's built-in Misty agent; a failed read fails closed.
func (s *SpacesService) agentPermissionMode(ctx context.Context, user, agentID string) string {
	return agentPermissionModeFrom(ctx, s.database, user, agentID)
}

func agentPermissionModeFrom(ctx context.Context, database *db.Database, user, agentID string) string {
	if database == nil {
		return serveragent.PermissionAsk
	}
	var err error
	if agentID == "" || agentID == "misty-unified" {
		if agentID, err = database.ManagedMistyAgentID(ctx, user); err != nil {
			return serveragent.PermissionAsk
		}
		if agentID == "" {
			return serveragent.PermissionAuto
		}
	}
	mode, err := database.AgentDefaultRunMode(ctx, user, agentID)
	if err != nil {
		return serveragent.PermissionAsk
	}
	if mode = serveragent.NormalizePermissionMode(mode); mode != "" {
		return mode
	}
	return serveragent.PermissionAuto
}

// grantAgentID is the agent a grant is held by: the run's agent, or the
// person's built-in Misty agent for chats that name none.
func (s *SpacesService) grantAgentID(ctx context.Context, invocation agenttools.Invocation) string {
	if invocation.AgentID != "" && invocation.AgentID != "misty-unified" {
		return invocation.AgentID
	}
	id, _ := s.database.ManagedMistyAgentID(ctx, invocation.UserID)
	return id
}

// admitPermissionMode freezes a new turn's Ask or Auto: the turn's own choice
// (saved to the chat), else the chat's, else the agent's default.
func admitPermissionMode(ctx context.Context, database *db.Database, user string, body *aiInvocationInput) error {
	if body.PermissionMode != "" {
		if body.ConversationID != "" {
			return database.SetConversationPermissionMode(ctx, user, body.ConversationID, body.PermissionMode)
		}
		return nil
	}
	if body.ConversationID != "" {
		mode, err := database.ConversationPermissionMode(ctx, user, body.ConversationID)
		if err != nil {
			return err
		}
		body.PermissionMode = mode
	}
	if body.PermissionMode == "" {
		body.PermissionMode = agentPermissionModeFrom(ctx, database, user, body.AgentID)
	}
	return nil
}

// continuationPermissionMode is a goal continuation's mode: the chat's current
// mode, but never looser than the turn it continues.
func (s *SpacesService) continuationPermissionMode(ctx context.Context, user, conversation, agentID, previous string) string {
	mode, err := s.database.ConversationPermissionMode(ctx, user, conversation)
	if err != nil {
		return serveragent.PermissionAsk
	}
	if mode == "" {
		mode = s.agentPermissionMode(ctx, user, agentID)
	}
	if serveragent.NormalizePermissionMode(previous) == serveragent.PermissionAsk {
		return serveragent.PermissionAsk
	}
	return mode
}

// spaceLabel names a Space for a remember label.
func (s *SpacesService) spaceLabel(ctx context.Context, user, spaceID string) string {
	if spaceID == "" {
		return "your library"
	}
	if space, err := s.database.SpaceByID(ctx, user, spaceID); err == nil && strings.TrimSpace(space.Name) != "" {
		return space.Name
	}
	return "this Space"
}

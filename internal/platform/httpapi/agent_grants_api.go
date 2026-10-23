package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/spaces"
)

// AgentGrants lists the person's remembered approvals, optionally for one agent,
// and revokes every grant for one agent.
//
//	GET    /me/agent-grants[?agent_id=]
//	DELETE /me/agent-grants?agent_id=
func (s *SpacesService) AgentGrants() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		if r.Method == http.MethodDelete {
			revoked, err := s.database.RevokeAgentGrants(r.Context(), user, agentID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"revoked": revoked})
			return
		}
		grants, err := s.database.AgentGrants(r.Context(), user, agentID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		views := make([]agentGrantView, 0, len(grants))
		for _, grant := range grants {
			views = append(views, newAgentGrantView(grant))
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": views})
	}
}

// AgentGrant revokes one remembered approval: DELETE /me/agent-grants/{grantID}.
func (s *SpacesService) AgentGrant() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if err := s.database.RevokeAgentGrant(r.Context(), user, chi.URLParam(r, "grantID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// agentGrantView is a grant as Settings shows it, with a server-written label.
type agentGrantView struct {
	db.AgentGrant
	Label string `json:"label"`
}

func newAgentGrantView(grant db.AgentGrant) agentGrantView {
	return agentGrantView{AgentGrant: grant, Label: grantLabel(grant)}
}

// grantLabel describes a grant in plain words, e.g. "Push to misty-org/misty".
func grantLabel(grant db.AgentGrant) string {
	scope := grant.ScopeID
	if grant.ScopeKind == string(agent.ScopeConnectedAccount) {
		scope = strings.TrimSuffix(strings.ReplaceAll(scope, ":", " · "), " · default")
	}
	switch agent.GrantAction(grant.Action) {
	case agent.GrantAppAction:
		return "Act in " + scope
	case agent.GrantBrowserSend:
		return "Send on " + scope
	case agent.GrantBrowserDelete:
		return "Delete on " + scope
	case agent.GrantBrowserPublish:
		return "Publish on " + scope
	case agent.GrantPayment:
		if limit := grantAmountFrom(grant.Constraints); limit != nil {
			return "Pay through " + scope + " up to " + formatGrantAmount(*limit)
		}
		return "Pay through " + scope
	case agent.GrantSpaceAdmin:
		return "Manage members of a Space"
	case agent.GrantItemDelete:
		return "Delete items in a Space"
	case agent.GrantScheduleRemove:
		return "Remove a workflow schedule"
	case agent.GrantProjectEdit:
		return "Edit files in " + scope
	case agent.GrantProjectInstall:
		return "Install packages in " + scope
	case agent.GrantGitPush:
		return "Push to " + scope
	case agent.GrantRecallRead:
		return "Search your Memory"
	}
	return grant.Action
}

// validateGrantScope checks, when the person remembers an approval, that the
// scope is one of their own resources: a Space they may administer, their own
// schedule, a site origin, an app account or merchant. Project and repository
// grants are created by the coding flow, which checks the device and remote.
func (s *SpacesService) validateGrantScope(ctx context.Context, user string, request *db.AgentAppRequest) error {
	action := agent.GrantAction(request.GrantAction)
	kind := agent.ScopeKind(request.GrantScopeKind)
	if !agent.ValidGrantScope(action, kind, request.GrantScopeID) {
		return db.ErrSpaceInvalid
	}
	if action == agent.GrantPayment && grantAmountFrom(request.GrantConstraints) == nil {
		return db.ErrSpaceInvalid
	}
	switch kind {
	case agent.ScopeSpace:
		permission := spaces.PermissionLibraryView
		if action == agent.GrantSpaceAdmin {
			permission = spaces.PermissionSpaceInvite
		}
		allowed, err := s.database.HasSpacePermission(ctx, user, request.GrantScopeID, permission)
		if err != nil {
			return err
		}
		if !allowed {
			return db.ErrSpaceForbidden
		}
	case agent.ScopeSchedule:
		if _, err := s.database.AgentMethodByID(ctx, user, request.GrantScopeID); err != nil {
			return db.ErrSpaceForbidden
		}
	case agent.ScopeOrigin:
		parsed, err := url.Parse(request.GrantScopeID)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" {
			return db.ErrSpaceInvalid
		}
	case agent.ScopeProject, agent.ScopeRepo:
		return s.validateCodingScope(ctx, user, kind, request.GrantScopeID)
	case agent.ScopeRecall:
		if request.GrantScopeID != agent.RecallScopeID {
			return db.ErrSpaceInvalid
		}
	}
	return nil
}

// rememberAppRequest approves a pending card and stores its grant. Everything
// the grant holds comes from the stored request; the person only says yes.
func (s *SpacesService) rememberAppRequest(ctx context.Context, user, id string) (*db.AgentAppRequest, error) {
	request, err := s.database.AgentAppRequest(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if !request.Rememberable || request.State != "pending" {
		return nil, db.ErrSpaceConflict
	}
	if err := s.validateGrantScope(ctx, user, request); err != nil {
		return nil, err
	}
	approved, _, err := s.database.RememberAgentAppRequest(ctx, user, id)
	return approved, err
}

// AgentSiteGrant remembers a page approval the person gave in their device's
// island: this agent may send, delete or publish on this exact site in Auto.
// Payments are never remembered from the island, since the device cannot cap
// them. POST /me/agent-grants/site {agent_id, kind, origin}.
func (s *SpacesService) AgentSiteGrant() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			AgentID string `json:"agent_id"`
			Kind    string `json:"kind"`
			Origin  string `json:"origin"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		action := map[string]agent.GrantAction{"send": agent.GrantBrowserSend, "delete": agent.GrantBrowserDelete,
			"publish": agent.GrantBrowserPublish}[body.Kind]
		request := &db.AgentAppRequest{GrantAction: string(action), GrantScopeKind: string(agent.ScopeOrigin), GrantScopeID: strings.TrimSpace(body.Origin)}
		if action == "" || s.validateGrantScope(r.Context(), user, request) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_grant"})
			return
		}
		agentID := strings.TrimSpace(body.AgentID)
		if agentID == "" || agentID == "misty-unified" {
			agentID, _ = s.database.ManagedMistyAgentID(r.Context(), user)
		}
		grant, err := s.database.CreateAgentGrant(r.Context(), user, agentID, request.GrantAction, request.GrantScopeKind, request.GrantScopeID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"grant": newAgentGrantView(*grant)})
	}
}

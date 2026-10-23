package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
)

// AgentProjectGrants carries a device's old per-project switches ("allow
// changes", "allow installs", "allow pushes") over as remembered approvals for
// the person's Misty agent, once: POST /me/agent-grants/project
// {scope_id, actions, repo}. The project must have been attached to one of the
// person's chats from their own device; until then the device tries again later.
func (s *SpacesService) AgentProjectGrants() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		if !requireCoding(w, r, s.database, user, "projects.grants") {
			return
		}
		var body struct {
			ScopeID string   `json:"scope_id"`
			Actions []string `json:"actions"`
			Repo    string   `json:"repo"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		scope, repo := strings.TrimSpace(body.ScopeID), strings.TrimSpace(body.Repo)
		if err := s.validateCodingScope(r.Context(), user, agent.ScopeProject, scope); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "project_not_attached"})
			return
		}
		agentID, err := s.database.ManagedMistyAgentID(r.Context(), user)
		if err != nil || agentID == "" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "agent_not_ready"})
			return
		}
		created := 0
		for _, action := range body.Actions {
			kind, id := agent.ScopeProject, scope
			switch agent.GrantAction(action) {
			case agent.GrantProjectEdit, agent.GrantProjectInstall:
			case agent.GrantGitPush:
				kind, id = agent.ScopeRepo, repo
				if s.validateCodingScope(r.Context(), user, kind, id) != nil {
					continue
				}
			default:
				continue
			}
			if _, err := s.database.CreateAgentGrant(r.Context(), user, agentID, action, string(kind), id); err != nil {
				writeSpaceError(w, err)
				return
			}
			created++
		}
		writeJSON(w, http.StatusOK, map[string]any{"created": created})
	}
}

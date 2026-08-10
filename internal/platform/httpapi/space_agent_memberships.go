package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) SpaceAgentMemberships() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID := chi.URLParam(r, "spaceID")
		switch r.Method {
		case http.MethodGet:
			items, err := s.database.SpaceAgentMemberships(r.Context(), userID, spaceID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"agents": items})
		case http.MethodPost:
			var body db.SpaceAgentMembershipInput
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.AddSpaceAgentMembership(r.Context(), userID, spaceID, body)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// SpaceAgentToolbox is the public, permission-checked capability manual for an
// installed Agent. It describes effective actions without exposing private
// instructions, memories, prompts, or the owner's raw policy document.
func (s *SpacesService) SpaceAgentToolbox() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), strings.TrimSpace(chi.URLParam(r, "agentID"))
		membership, err := s.database.SpaceAgentMembership(r.Context(), userID, spaceID, agentID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		policy, err := s.database.EffectivePersonalAgentToolPermissions(r.Context(), userID, spaceID, agentID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		items := personalAgentToolboxItems(policy)
		for index := range items {
			item := &items[index]
			if item.Name == toolboxMessagesSend {
				item.RequiredPermission = db.PermissionMessagesWrite
			}
			if !membership.Enabled {
				item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "agent_disabled", Message: "This Agent is disabled in this Space."})
			}
			if item.RequiredPermission != "" && membership.Enabled {
				allowed, permissionErr := s.database.EffectiveAgentSpacePermission(r.Context(), userID, spaceID, agentID, item.RequiredPermission)
				if permissionErr != nil {
					writeSpaceError(w, permissionErr)
					return
				}
				if !allowed {
					item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "member_permission_required", Message: "Your Space role or this Agent's Space grants do not allow this action."})
				}
			}
			item.Available = len(item.Reasons) == 0
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"actions": items, "recent_activity": []db.AgentToolboxActionAudit{},
			"context": publicAgentContextSummary(membership.Permissions),
		})
	}
}

func publicAgentContextSummary(permissions json.RawMessage) []string {
	var allowed map[string]bool
	_ = json.Unmarshal(permissions, &allowed)
	items := []string{}
	if allowed[db.PermissionMessagesRead] {
		items = append(items, "Space chat")
	}
	if allowed[db.PermissionTasksView] {
		items = append(items, "Planner tasks and task notes")
	}
	if allowed["attached_files.read"] {
		items = append(items, "Files attached to assigned work")
	}
	return items
}

func (s *SpacesService) SpaceAgentMembership() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), strings.TrimSpace(chi.URLParam(r, "agentID"))
		switch r.Method {
		case http.MethodPatch:
			var body db.SpaceAgentMembershipInput
			if decodeJSON(w, r, &body) != nil {
				return
			}
			item, err := s.database.UpdateSpaceAgentMembership(r.Context(), userID, spaceID, agentID, body)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			if err := s.database.RemoveSpaceAgentMembership(r.Context(), userID, spaceID, agentID); err != nil {
				writeSpaceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *SpacesService) ApproveSpaceAgentVersion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, err := s.database.ApproveSpaceAgentVersion(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

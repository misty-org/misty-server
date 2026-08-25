package api

import (
	"encoding/json"
	"net/http"
	"sort"
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
		items, err := s.database.SpaceAgentMemberships(r.Context(), userID, spaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": items})
	}
}

// personalAgentToolboxItems remains private to the read-only Space membership
// view. It does not expose a route for configuring or invoking an Agent.
func personalAgentToolboxItems(policy json.RawMessage) []agentToolboxCatalogItem {
	descriptors := personalAgentToolboxCatalogDescriptors()
	items := make([]agentToolboxCatalogItem, 0, len(descriptors))
	for _, descriptor := range descriptors {
		granted := personalAgentToolPolicyAllows(policy, descriptor)
		item := agentToolboxCatalogItem{
			Name: descriptor.Name, Description: descriptor.Description, Risk: descriptor.Risk,
			Approval: descriptor.Approval, Locality: descriptor.Locality, Idempotent: descriptor.Idempotent,
			AuditEvent: descriptor.AuditEvent, RequiredPermission: descriptor.RequiredPermission,
			Granted: granted, Available: granted, Reasons: []agentToolboxAvailabilityReason{},
		}
		if !granted {
			item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "grant_required", Message: "This action is not enabled for this Agent."})
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
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

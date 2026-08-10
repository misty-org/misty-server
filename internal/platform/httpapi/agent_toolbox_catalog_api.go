package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type agentToolboxAvailabilityReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type agentToolboxCatalogItem struct {
	Name               string                           `json:"name"`
	Description        string                           `json:"description"`
	Risk               string                           `json:"risk"`
	Approval           agenttools.ApprovalPolicy        `json:"approval"`
	Locality           agenttools.Locality              `json:"locality"`
	Idempotent         bool                             `json:"idempotent"`
	AuditEvent         string                           `json:"audit_event,omitempty"`
	RequiredPermission string                           `json:"required_permission,omitempty"`
	Granted            bool                             `json:"granted"`
	Available          bool                             `json:"available"`
	Reasons            []agentToolboxAvailabilityReason `json:"reasons"`
}

func (s *SpacesService) AgentInstanceToolbox() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		instance, err := s.database.AgentInstanceByID(r.Context(), userID, chi.URLParam(r, "instanceID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		connected := map[string]bool{}
		resources, err := s.database.ProviderSharedResources(r.Context(), userID, instance.SpaceID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		for _, resource := range resources {
			if resource.Status == "active" {
				connected[strings.TrimSpace(resource.Provider)] = true
			}
		}
		if sources, sourceErr := s.database.SpaceCalendarSources(r.Context(), userID, instance.SpaceID); sourceErr == nil && len(sources) > 0 {
			connected["google"] = true
		}

		descriptors := canonicalAgentToolboxCatalogDescriptors()
		items := make([]agentToolboxCatalogItem, 0, len(descriptors))
		for _, descriptor := range descriptors {
			item := agentToolboxCatalogItem{
				Name: descriptor.Name, Description: descriptor.Description, Risk: descriptor.Risk,
				Approval: descriptor.Approval, Locality: descriptor.Locality, Idempotent: descriptor.Idempotent,
				AuditEvent: descriptor.AuditEvent, RequiredPermission: descriptor.RequiredPermission,
				Granted: db.AgentCapabilityGranted(instance.CapabilityGrants, descriptor.Name, descriptor.Risk),
				Reasons: []agentToolboxAvailabilityReason{},
			}
			if policy, exists := descriptor.ApprovalBySource[canonicalAgentToolSource]; exists {
				item.Approval = policy
			}
			if !item.Granted {
				item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "grant_required", Message: "This action is not enabled for this Agent."})
			}
			if descriptor.RequiredPermission != "" {
				allowed, permissionErr := s.database.HasSpacePermission(r.Context(), userID, instance.SpaceID, descriptor.RequiredPermission)
				if permissionErr != nil {
					writeSpaceError(w, permissionErr)
					return
				}
				if !allowed {
					item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "member_permission_required", Message: "Your Space role does not allow this action."})
				}
			}
			if provider := providerFromToolName(descriptor.Name); provider != "" && !connected[provider] {
				item.Reasons = append(item.Reasons, agentToolboxAvailabilityReason{Code: "connection_required", Message: "Connect and share " + provider + " with this Space to use this action."})
			}
			item.Available = len(item.Reasons) == 0
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		audits, err := s.database.AgentToolboxActionAudits(r.Context(), userID, instance.ID, 50)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"instance": instance, "actions": items, "recent_activity": audits})
	}
}

func providerFromToolName(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) == 3 && parts[0] == "provider" {
		return parts[1]
	}
	return ""
}

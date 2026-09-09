package api

import (
	"encoding/json"
	"sort"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

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

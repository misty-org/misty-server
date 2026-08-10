package api

import (
	"encoding/json"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var agentToolboxSpaceSources = []string{"canonical_run", "space_conversation"}
var canonicalAgentToolboxProviders = []string{"discord", "google", "notion", "slack"}

func agentToolObjectOutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func messagesSearchToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxMessagesSearch, Version: 1, Description: "Search messages visible to the member in the current Space.",
		Risk: serveragent.RiskRead, InputSchema: spaceSearchAgentToolSchema(), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionMessagesRead,
		AgentPermission: db.PermissionMessagesRead, AllowCustomAgent: true, Approval: agenttools.ApprovalNone,
		Locality: agenttools.LocalityServer, Idempotent: true, Aliases: []string{"space.search_messages"},
		Sources: agentToolboxSpaceSources,
	}
}

func messagesSendToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxMessagesSend, Version: 1,
		Description: "Send an exact member-provided message to the current Space-wide chat.",
		Risk:        serveragent.RiskWrite,
		InputSchema: TestingMustAPIRawJSON(map[string]any{
			"type": "object", "properties": map[string]any{
				"message": map[string]any{"type": "string", "maxLength": db.MaxMessageChars},
			}, "required": []string{"message"},
		}),
		OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionMessagesWrite,
		AgentPermission: db.PermissionMessagesWrite, AllowCustomAgent: true,
		Approval: agenttools.ApprovalExplicitIntent,
		ApprovalBySource: map[string]agenttools.ApprovalPolicy{
			canonicalAgentToolSource: agenttools.ApprovalInteractive,
		},
		Locality: agenttools.LocalityServer, AuditEvent: "space.message.created",
		Sources: agentToolboxSpaceSources,
	}
}

func librarySearchToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxLibrarySearch, Version: 1, Description: "Search visible Library items in the current Space.",
		Risk: serveragent.RiskRead, InputSchema: spaceSearchAgentToolSchema(), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionLibraryView,
		AgentPermission: db.PermissionLibraryView, AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true,
		Sources: agentToolboxSpaceSources,
	}
}

func tasksQueryToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxTasksQuery, Version: 1, Description: "Query Tasks visible in the current Space.",
		Risk: serveragent.RiskRead, InputSchema: taskAgentToolSchema(false), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksView,
		AgentPermission: db.PermissionTasksView, AllowCustomAgent: true, Approval: agenttools.ApprovalNone,
		Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources,
	}
}

func tasksCreateToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxTasksCreate, Version: 1, Description: "Create a Task in the current Space.",
		Risk: serveragent.RiskWrite, InputSchema: taskAgentToolSchema(true), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksManage,
		AgentPermission: db.PermissionTasksManage, AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent,
		ApprovalBySource: map[string]agenttools.ApprovalPolicy{canonicalAgentToolSource: agenttools.ApprovalInteractive},
		Locality:         agenttools.LocalityServer, AuditEvent: "task.created", Sources: agentToolboxSpaceSources,
	}
}

func tasksUpdateToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxTasksUpdate, Version: 1, Description: "Update an explicitly identified Task in the current Space.",
		Risk: serveragent.RiskWrite, InputSchema: taskAgentToolSchema(true), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksManage,
		AgentPermission: db.PermissionTasksManage, AllowCustomAgent: true, Approval: agenttools.ApprovalExplicitIntent,
		ApprovalBySource: map[string]agenttools.ApprovalPolicy{canonicalAgentToolSource: agenttools.ApprovalInteractive},
		Locality:         agenttools.LocalityServer, Idempotent: true, AuditEvent: "task.updated", Sources: agentToolboxSpaceSources,
	}
}

func calendarQueryToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "calendar.query", Version: 1, Description: "Query the current Space calendar.",
		Risk: serveragent.RiskRead, InputSchema: taskAgentToolSchema(false), OutputSchema: agentToolObjectOutputSchema(), RequiredPermission: db.PermissionTasksView,
		AgentPermission: db.PermissionTasksView, AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources,
	}
}

func canonicalAgentToolboxCatalogDescriptors() []agenttools.Descriptor {
	descriptors := []agenttools.Descriptor{
		messagesSearchToolDescriptor(), messagesSendToolDescriptor(), librarySearchToolDescriptor(), tasksQueryToolDescriptor(),
		calendarQueryToolDescriptor(), tasksCreateToolDescriptor(), tasksUpdateToolDescriptor(),
	}
	for _, provider := range canonicalAgentToolboxProviders {
		descriptors = append(descriptors, canonicalProviderToolDescriptor(provider, false))
		if providerSupportsWrite(provider) {
			descriptors = append(descriptors, canonicalProviderToolDescriptor(provider, true))
		}
	}
	return descriptors
}

func personalAgentToolboxCatalogDescriptors() []agenttools.Descriptor {
	descriptors := canonicalAgentToolboxCatalogDescriptors()
	return append(descriptors, assignedTasksUpdateToolDescriptor(), assignedTaskActivityToolDescriptor(), agentDelegationToolDescriptor())
}

func TestingPersonalAgentToolboxDescriptors() []agenttools.Descriptor {
	return personalAgentToolboxCatalogDescriptors()
}

package api

import (
	"encoding/json"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var agentToolboxSpaceSources = []string{"canonical_run", "space_conversation"}
var canonicalAgentToolboxProviders = []string{"discord", "figma", "github", "google", "notion", "slack"}

func agentToolObjectOutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func contextGetToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxContextGet, Version: 1, Description: "Get authoritative current time, timezone, and Space identity for this run.",
		Risk: serveragent.RiskRead, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}}), OutputSchema: agentToolObjectOutputSchema(),
		AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources,
	}
}

func membersListToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxMembersList, Version: 1, Description: "List members of the current Space with stable user IDs and roles.",
		Risk: serveragent.RiskRead, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}}), OutputSchema: agentToolObjectOutputSchema(),
		AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources,
	}
}

func membersResolveToolDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: toolboxMembersResolve, Version: 1, Description: "Resolve a member name or email in the current Space. Ambiguous matches are returned without guessing.",
		Risk: serveragent.RiskRead, InputSchema: TestingMustAPIRawJSON(map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 320}}}), OutputSchema: agentToolObjectOutputSchema(),
		AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources,
	}
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

func browserToolDescriptors() []agenttools.Descriptor {
	definitions := []struct {
		name, description, risk, audit string
		schema                         json.RawMessage
		idempotent                     bool
	}{
		{
			name: "browser.inspect", description: "Inspect the current untrusted page text and actionable elements in an explicitly granted browser tab.",
			risk: serveragent.RiskRead, audit: "browser.page.inspected", idempotent: true,
			schema: browserAgentToolSchema("inspect"),
		},
		{
			name: "browser.navigate", description: "Navigate an explicitly granted browser tab to an http or https URL.",
			risk: serveragent.RiskWrite, audit: "browser.page.navigated", idempotent: false,
			schema: browserAgentToolSchema("navigate"),
		},
		{
			name: "browser.click", description: "Click an element reference returned by the latest inspection of an explicitly granted browser tab.",
			risk: serveragent.RiskWrite, audit: "browser.element.clicked", idempotent: false,
			schema: browserAgentToolSchema("click"),
		},
		{
			name: "browser.downloads.list", description: "List recent downloads for an explicitly granted browser tab.",
			risk: serveragent.RiskRead, audit: "browser.downloads.inspected", idempotent: true,
			schema: browserAgentToolSchema("downloads"),
		},
	}
	descriptors := make([]agenttools.Descriptor, 0, len(definitions))
	for _, definition := range definitions {
		descriptors = append(descriptors, agenttools.Descriptor{
			Name: definition.name, Version: 1, Description: definition.description,
			Risk: definition.risk, InputSchema: definition.schema, OutputSchema: agentToolObjectOutputSchema(),
			AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityDevice,
			Idempotent: definition.idempotent, AuditEvent: definition.audit,
			Sources: []string{canonicalAgentToolSource, "space_conversation", "task_assignment"},
		})
	}
	return descriptors
}

func browserAgentToolSchema(kind string) json.RawMessage {
	properties := map[string]any{
		"scopeId": map[string]any{"type": "string", "minLength": 8, "maxLength": 256},
	}
	required := []string{"scopeId"}
	switch kind {
	case "navigate":
		properties["url"] = map[string]any{"type": "string", "maxLength": 4096}
		required = append(required, "url")
	case "click":
		properties["elementRef"] = map[string]any{"type": "string", "maxLength": 128}
		properties["expectDownload"] = map[string]any{"type": "boolean"}
		required = append(required, "elementRef")
	}
	return TestingMustAPIRawJSON(map[string]any{
		"type": "object", "properties": properties, "required": required, "additionalProperties": false,
	})
}

func canonicalAgentToolboxCatalogDescriptors() []agenttools.Descriptor {
	descriptors := []agenttools.Descriptor{
		contextGetToolDescriptor(), membersListToolDescriptor(), membersResolveToolDescriptor(),
		messagesSearchToolDescriptor(), messagesSendToolDescriptor(), librarySearchToolDescriptor(), tasksQueryToolDescriptor(),
		calendarQueryToolDescriptor(), tasksCreateToolDescriptor(), tasksUpdateToolDescriptor(),
	}
	descriptors = append(descriptors, noteAgentToolDescriptors()...)
	descriptors = append(descriptors, calendarWriteToolDescriptors()...)
	descriptors = append(descriptors, roadmapAgentToolDescriptors()...)
	descriptors = append(descriptors, libraryMutationToolDescriptors()...)
	descriptors = append(descriptors, companionReadToolDescriptors()...)
	descriptors = append(descriptors, browserToolDescriptors()...)
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

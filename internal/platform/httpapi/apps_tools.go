package api

import (
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
)

// appsToolRegistrations give a run the invoking user's connected apps. Every
// call acts through that user's own Composio session.
func (s *SpacesService) appsToolRegistrations() []agenttools.Registration {
	if !appsAvailable() {
		return nil
	}
	slug := map[string]any{"type": "string", "pattern": "^[A-Z][A-Z0-9_]{2,127}$"}
	return []agenttools.Registration{
		{Descriptor: appsDescriptor(appsSearchTool, serveragent.RiskRead,
			"Find tools in the user's apps: Gmail, Google Drive, Google Calendar, Slack, Notion, GitHub, Linear and hundreds more. Describe each task in plain words, one per query. Returns exact tool slugs with input schemas, and whether each app is connected.",
			map[string]any{"queries": map[string]any{"type": "array", "minItems": 1, "maxItems": 5, "items": map[string]any{"type": "string", "minLength": 3, "maxLength": 500}}}, "queries"),
			Handler: s.appsSearch},
		{Descriptor: appsDescriptor(appsSchemasTool, serveragent.RiskRead,
			"Get full input schemas for exact tool slugs from apps_search when a result lacks one.",
			map[string]any{"tool_slugs": map[string]any{"type": "array", "minItems": 1, "maxItems": 10, "items": slug}}, "tool_slugs"),
			Handler: s.appsSchemas},
		{Descriptor: appsDescriptor(appsConnectedTool, serveragent.RiskRead,
			"List the apps and accounts the user has connected.", map[string]any{}),
			Handler: s.appsConnected},
		{Descriptor: appsDescriptor(appsConnectTool, serveragent.RiskRead,
			"Ask the user to connect an app that apps_search reports as not connected. Shows a Connect card in the chat and waits up to 40 seconds while the user signs in; call again to keep waiting. Returns connected once the account is ready.",
			map[string]any{"app": map[string]any{"type": "string", "pattern": "^[a-z0-9][a-z0-9_-]{1,63}$", "description": "App slug from apps_search, such as googledrive"}}, "app"),
			Handler: s.appsConnect},
		{Descriptor: appsDescriptor(appsExecuteTool, serveragent.RiskWrite,
			"Run one app tool by its exact slug from apps_search, with arguments matching its input schema. Sending, sharing, deleting and payments may wait for the user's approval in the chat; call again with the same arguments to keep waiting. Pass account to choose among several connected accounts.",
			map[string]any{"tool_slug": slug, "arguments": map[string]any{"type": "object"}, "account": map[string]any{"type": "string", "maxLength": 160}}, "tool_slug", "arguments"),
			Handler: s.appsExecute},
	}
}

func appsDescriptor(name, risk, description string, properties map[string]any, required ...string) agenttools.Descriptor {
	if required == nil {
		required = []string{}
	}
	return agenttools.Descriptor{
		Name: name, Version: 1, Description: description, Risk: risk,
		InputSchema:  TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}),
		OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityProvider,
		Idempotent: risk == serveragent.RiskRead, AllowCustomAgent: true, AuditEvent: name,
	}
}

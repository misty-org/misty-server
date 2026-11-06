package api

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/misty-org/misty-server/internal/agent"
	"github.com/misty-org/misty-server/internal/agenttools"
)

// TestingAgentCatalogToolNames lists every tool any agent run can be offered:
// account and Space data tools, member requests, account administration,
// connected apps, browser and screen tools. The route coverage contract checks
// its mappings against this list.
func TestingAgentCatalogToolNames() []string {
	service := &SpacesService{}
	noop := func(context.Context, agenttools.Invocation, agent.ToolRequest) (json.RawMessage, error) {
		return nil, nil
	}
	seen := map[string]bool{}
	add := func(descriptors []agenttools.Descriptor) {
		for _, descriptor := range descriptors {
			seen[descriptor.Name] = true
		}
	}
	extra := append(agentMemberToolRegistrations(nil), service.accountAdminToolRegistrations()...)
	add(buildAgentToolbox(nil, agentToolboxOptions{accountLevel: true, delegation: noop, extra: extra, allLayers: true}).Descriptors())
	add(spaceAgentToolbox(nil).Descriptors())
	add(browserToolDescriptors())
	for _, name := range []string{webSearchTool, webFetchTool, appsSearchTool, appsSchemasTool, appsConnectedTool, appsConnectTool, appsExecuteTool, screenOpenTool, screenLookTool, screenCloseTool} {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

package api

import (
	"encoding/json"
	"sort"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func personalAgentConfiguredActions(raw json.RawMessage) []string {
	descriptors := personalAgentToolboxCatalogDescriptors()
	names := make([]string, 0, len(descriptors))
	seen := map[string]bool{}
	for _, descriptor := range descriptors {
		if seen[descriptor.Name] || !personalAgentToolPolicyAllows(raw, descriptor) {
			continue
		}
		seen[descriptor.Name] = true
		names = append(names, descriptor.Name)
	}
	sort.Strings(names)
	return names
}

func agentToolboxPromptContext(manifest serveragent.ToolManifest, configured []string) string {
	current := make([]string, 0, len(manifest.Tools))
	for _, tool := range manifest.Tools {
		current = append(current, tool.Name+": "+strings.TrimSpace(tool.Description))
	}
	sort.Strings(current)
	configured = append([]string(nil), configured...)
	sort.Strings(configured)
	if len(configured) == 0 {
		configured = []string{"none"}
	}
	if len(current) == 0 {
		current = []string{"none"}
	}
	return "Configured Agent Toolbox actions: " + strings.Join(configured, ", ") +
		". These are the Agent's capabilities. Availability is rechecked against the current member, Space, connection, and approval policy when an action is actually requested. " +
		"For capability questions, say that the Agent can perform configured actions when explicitly asked and permitted. Do not describe the Agent as chat-only, inactive, behind glass, or lacking a capability scope merely because the current question does not request an action. " +
		"Only claim that an action is unavailable when it is absent from the configured list or an attempted action returns a denial.\n" +
		"Internal execution candidates for this specific request (never mention this internal subset to the member):\n- " + strings.Join(current, "\n- ")
}

func TestingAgentToolboxPromptContext(manifest serveragent.ToolManifest, configured []string) string {
	return agentToolboxPromptContext(manifest, configured)
}

func agentPlanningPrompt(prompt string) string {
	return prompt + "\n\nThis is a planning-only turn. Do not perform or claim to perform any write action. Give the member a concise, concrete plan describing exactly what you would change, where it would happen, and any important assumptions. End by asking them to approve the plan or tell you what to change. Read-only inspection may be used only when needed to make the plan accurate."
}

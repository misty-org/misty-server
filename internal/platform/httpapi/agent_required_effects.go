package api

import "time"

type preparedAIInvocationRuntime struct {
	body               aiInvocationInput
	resolved           []aiResolvedContext
	spaceID            string
	spaceName          string
	spaceKind          string
	members            []map[string]string
	modelID            string
	reasoning          string
	system             string
	prompt             string
	timezone           string
	currentTime        time.Time
	allowedTools       []string
	requiredTools      []string
	previousUserPrompt string
	previousAgentReply string
}

func requiredAgentMutationTools(candidates []string) []string {
	writes := map[string]bool{
		toolboxMessagesSend:             true,
		toolboxTasksCreate:              true,
		toolboxTasksUpdate:              true,
		toolboxAgentsDelegate:           true,
		toolboxMemoryRemember:           true,
		toolboxMemoryForget:             true,
		toolboxNotesCreate:              true,
		toolboxNotesUpdate:              true,
		toolboxDrawingsCreate:           true,
		toolboxDrawingsApply:            true,
		toolboxCalendarCreate:           true,
		toolboxCalendarUpdate:           true,
		toolboxRoadmapsCreate:           true,
		toolboxRoadmapsUpdate:           true,
		toolboxLibraryUpdate:            true,
		toolboxLibraryPromoteAttachment: true,
	}
	required := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, name := range candidates {
		if writes[name] && !seen[name] {
			required = append(required, name)
			seen[name] = true
		}
	}
	return required
}

func TestingRequiredAgentMutationTools(prompt string) []string {
	return requiredAgentMutationTools(TestingCompileAgentIntent(prompt))
}

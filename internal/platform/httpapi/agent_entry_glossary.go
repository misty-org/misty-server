package api

import "strings"

// agent.entry and the glossary (docs/agents/harness.md, Agent vocabulary). The
// entry says where a request came from and how its reply is read; the glossary
// maps the agent's own names to the app's labels today, so renaming a section
// changes one line here and nothing in the agent.

// agentEntry names the run's entry point: composer, cursor, schedule, email,
// or integration.<app>.
func agentEntry(body aiInvocationInput) string {
	switch {
	case body.Mode == "companion":
		return "cursor"
	case body.Trigger == scheduledRunTrigger:
		return "schedule"
	case body.Trigger == inboundTaskTrigger && strings.HasPrefix(body.Prompt, "Task from email"):
		return "email"
	case body.Trigger == inboundTaskTrigger:
		return "integration.slack"
	default:
		return "composer"
	}
}

// entryGuidance says how the reply is delivered. The cursor's own voice and
// pointing rules come from the companion prompt.
func entryGuidance(entry string) string {
	switch entry {
	case "composer":
		return "\n\nWhere this reply goes: the conversation in Misty. Format it for reading: short paragraphs, lists and tables where they help, links to sources."
	case "schedule":
		return "\n\nWhere this reply goes: a scheduled run the person reads later. Nobody is watching: don't ask questions, write a self-contained report of what you found and did."
	case "email":
		return "\n\nWhere this reply goes: an email reply to the person. Write plain, self-contained text that reads well in email; nobody is watching the run."
	case "integration.slack":
		return "\n\nWhere this reply goes: a Slack reply to the person. Keep it short and self-contained; Slack shows simple formatting only."
	}
	return ""
}

// uiLabels are the app's names for what the agent's tools act on, by tool
// family. Only families present in the run are listed.
var uiLabels = []struct{ family, label string }{
	{"space.", "Spaces are what the app calls a space (space_*)."},
	{"file.", "The Library holds files (file_*)."},
	{"memory.history.", "Settings › Memory and the Journal day show history (memory_history_*): pages visited, dictations and captures."},
	{"memory.pref.", "Memory the person asks you to keep is memory_pref_*."},
	{"memory.workflow.", "The Workflows page shows saved workflows (memory_workflow_*)."},
	{"log.work.", "The Notebook card in the conversation shows log_work_*."},
	{"integration.", "Integrations are the person's connected apps (integration_*)."},
	{"folder.", "Shared folders are folders on the person's computer (folder_*)."},
}

// glossaryGuidance lets the agent match the person's words to its own names.
func glossaryGuidance(tools []string) string {
	present := func(prefix string) bool {
		for _, name := range tools {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	}
	var lines []string
	for _, item := range uiLabels {
		if present(item.family) {
			lines = append(lines, item.label)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nWords in the app: " + strings.Join(lines, " ") + " Match the person's words to these."
}

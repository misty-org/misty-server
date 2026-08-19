package api

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/kannachi323/misty/server/internal/agenttools"
)

func companionToolImpact(name string) string {
	if strings.HasSuffix(name, ".query") || strings.HasSuffix(name, ".search") || strings.HasSuffix(name, ".read") || name == "browser.inspect" || name == "browser.downloads.list" {
		return "observe"
	}
	if strings.HasPrefix(name, "provider.") && strings.HasSuffix(name, ".write") {
		return "consequential"
	}
	switch name {
	case "messages.send", "git.commit", "project.publish", "connections.write":
		return "consequential"
	case "git.push", "files.delete", "members.update", "members.remove", "browser.click", "browser.confirm_high_risk", "terminal.execute_unsandboxed":
		return "dangerous"
	default:
		return "routine"
	}
}

func companionToolNeedsApproval(mode, impact string) bool {
	if impact == "observe" {
		return false
	}
	if impact == "dangerous" {
		return true
	}
	if mode == "ask" {
		return true
	}
	return mode == "auto" && impact == "consequential"
}

func companionToolApprovalSummary(name string, arguments json.RawMessage) string {
	var values map[string]any
	_ = json.Unmarshal(arguments, &values)
	stringValue := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
				value = strings.TrimSpace(value)
				if len([]rune(value)) > 160 {
					value = string([]rune(value)[:159]) + "…"
				}
				return value
			}
		}
		return ""
	}
	switch name {
	case toolboxMessagesSend:
		return "Send to Space chat: “" + stringValue("message") + "”"
	case toolboxTasksCreate:
		return "Create task “" + stringValue("title") + "”"
	case toolboxTasksUpdate:
		return "Update task " + stringValue("id", "title")
	case toolboxNotesCreate:
		return "Create note “" + stringValue("title") + "”"
	case toolboxNotesUpdate:
		return "Update note " + stringValue("id", "title")
	case toolboxCalendarCreate:
		return "Create calendar event “" + stringValue("title") + "”"
	case toolboxCalendarUpdate:
		return "Update calendar event " + stringValue("id", "title")
	case toolboxRoadmapsCreate:
		return "Create roadmap “" + stringValue("name") + "”"
	case toolboxRoadmapsUpdate:
		return "Update roadmap " + stringValue("id", "name")
	case toolboxLibraryUpdate:
		return "Update Library item " + stringValue("displayName", "id")
	case toolboxLibraryPromoteAttachment:
		return "Save the selected attachment to the Space Library"
	case toolboxAgentsDelegate:
		return "Ask " + stringValue("agent_name", "agent_id") + " to help with this work"
	case "browser.click":
		return "Click the selected element in the attached browser tab"
	}
	if strings.HasPrefix(name, "provider.") && strings.HasSuffix(name, ".write") {
		return "Send through " + strings.TrimPrefix(strings.TrimSuffix(name, ".write"), "provider.") + " to " + stringValue("destination")
	}
	return "Allow " + strings.ReplaceAll(name, ".", " ")
}

func TestingCompanionToolApprovalSummary(name string, arguments json.RawMessage) string {
	return companionToolApprovalSummary(name, arguments)
}

func TestingCompanionToolNeedsApproval(mode, impact string) bool {
	return companionToolNeedsApproval(mode, impact)
}

func TestingCompanionToolImpact(name string) string {
	return companionToolImpact(name)
}

func personalAgentToolPolicyAllows(raw json.RawMessage, descriptor agenttools.Descriptor) bool {
	var policy struct {
		Mode string `json:"mode"`
	}
	return json.Unmarshal(raw, &policy) == nil && policy.Mode == "inherit_creator" && personalAgentToolSurface(descriptor) != ""
}

func personalAgentToolSurface(descriptor agenttools.Descriptor) string {
	if descriptor.Locality == agenttools.LocalityProvider || strings.HasPrefix(descriptor.Name, "provider.") {
		return "connections"
	}
	name := strings.ToLower(descriptor.Name)
	switch {
	case strings.HasPrefix(name, "browser."):
		return "browser"
	case strings.HasPrefix(name, "files.") || strings.HasPrefix(name, "library."):
		return "files"
	case strings.HasPrefix(name, "terminal."):
		return "terminal"
	case strings.HasPrefix(name, "code.") || strings.HasPrefix(name, "editor."):
		return "code_editor"
	case strings.HasPrefix(name, "agents."):
		return "agents"
	case strings.HasPrefix(name, "extensions."):
		return "extensions"
	default:
		return "spaces"
	}
}

func TestingPersonalAgentCapabilityAllowed(raw json.RawMessage, name, risk string) bool {
	return personalAgentToolPolicyAllows(raw, agenttools.Descriptor{Name: name, Risk: risk, Locality: agenttools.LocalityServer})
}

func normalizeGroundingText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"", "—", "-", "–", "-").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func containsGroundingPhrase(value, phrase string) bool {
	words := func(input string) string {
		input = normalizeGroundingText(input)
		input = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return ' '
		}, input)
		return strings.Join(strings.Fields(input), " ")
	}
	value, phrase = words(value), words(phrase)
	return phrase != "" && strings.Contains(" "+value+" ", " "+phrase+" ")
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

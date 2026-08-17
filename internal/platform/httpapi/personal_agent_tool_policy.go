package api

import (
	"encoding/json"
	"strings"
	"unicode"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func personalAgentToolPolicyAllows(raw json.RawMessage, descriptor agenttools.Descriptor) bool {
	var policy struct {
		Mode         string                     `json:"mode"`
		Disabled     []string                   `json:"disabled_surfaces"`
		Read         bool                       `json:"read"`
		Write        bool                       `json:"write"`
		Integrations []string                   `json:"integrations"`
		Grants       *[]db.AgentCapabilityGrant `json:"grants"`
	}
	if json.Unmarshal(raw, &policy) != nil {
		return false
	}
	if policy.Mode == "inherit_invoker" {
		surface := personalAgentToolSurface(descriptor)
		return surface != "" && !containsString(policy.Disabled, surface)
	}
	if descriptor.Locality == agenttools.LocalityProvider {
		provider := ""
		parts := strings.Split(descriptor.Name, ".")
		if len(parts) == 3 && parts[0] == "provider" {
			provider = parts[1]
		}
		if provider == "" || !containsString(policy.Integrations, provider) {
			return false
		}
	}
	if policy.Grants != nil {
		for _, grant := range *policy.Grants {
			if grant.Capability == descriptor.Name && grant.Risk == descriptor.Risk {
				return true
			}
		}
		return false
	}
	switch descriptor.Risk {
	case serveragent.RiskRead:
		return policy.Read
	case serveragent.RiskWrite:
		return policy.Read && policy.Write
	default:
		return false
	}
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

func TestingPersonalAgentToolPolicyAllows(raw json.RawMessage, risk string) bool {
	return personalAgentToolPolicyAllows(raw, agenttools.Descriptor{Name: "test.action", Risk: risk, Locality: agenttools.LocalityServer})
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

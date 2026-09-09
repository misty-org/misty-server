package db

import (
	"encoding/json"
	"sort"
	"strings"
)

type AgentCapabilityGrant struct {
	Capability string            `json:"capability"`
	Risk       string            `json:"risk"`
	Scopes     map[string]string `json:"scopes,omitempty"`
}

var defaultAgentCapabilityGrants = []AgentCapabilityGrant{
	{Capability: "calendar.query", Risk: "read"},
	{Capability: "library.search", Risk: "read"},
	{Capability: "messages.search", Risk: "read"},
	{Capability: "tasks.create", Risk: "write"},
	{Capability: "tasks.query", Risk: "read"},
	{Capability: "tasks.update", Risk: "write"},
}

func DefaultAgentCapabilityGrants() json.RawMessage {
	raw, _ := json.Marshal(defaultAgentCapabilityGrants)
	return raw
}

func normalizeAgentCapabilityGrants(raw json.RawMessage) (json.RawMessage, error) {
	var grants []AgentCapabilityGrant
	if len(raw) == 0 || json.Unmarshal(raw, &grants) != nil || grants == nil {
		return nil, ErrSpaceInvalid
	}
	seen := map[string]bool{}
	for index := range grants {
		grant := &grants[index]
		grant.Capability = strings.TrimSpace(grant.Capability)
		grant.Risk = strings.TrimSpace(grant.Risk)
		if !validWorkflowToken(grant.Capability, 160) || grant.Risk != "read" && grant.Risk != "write" && grant.Risk != "dangerous" || seen[grant.Capability] {
			return nil, ErrSpaceInvalid
		}
		seen[grant.Capability] = true
		normalizedScopes := make(map[string]string, len(grant.Scopes))
		for key, value := range grant.Scopes {
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if !validWorkflowToken(key, 80) || value == "" || len(value) > 500 {
				return nil, ErrSpaceInvalid
			}
			normalizedScopes[key] = value
		}
		if len(normalizedScopes) > 0 {
			grant.Scopes = normalizedScopes
		} else {
			grant.Scopes = nil
		}
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].Capability < grants[j].Capability })
	return json.Marshal(grants)
}

func TestingNormalizeAgentCapabilityGrants(raw json.RawMessage) (json.RawMessage, error) {
	return normalizeAgentCapabilityGrants(raw)
}

func AgentCapabilityGranted(raw json.RawMessage, capability, risk string) bool {
	var grants []AgentCapabilityGrant
	if json.Unmarshal(raw, &grants) != nil {
		return false
	}
	capability, risk = strings.TrimSpace(capability), strings.TrimSpace(risk)
	for _, grant := range grants {
		if grant.Capability == capability && grant.Risk == risk {
			return true
		}
	}
	return false
}

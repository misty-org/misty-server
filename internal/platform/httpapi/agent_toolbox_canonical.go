package api

import (
	"encoding/json"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const canonicalAgentToolSource = "canonical_run"

func activeBrowserCapability(grants []db.AgentDeviceGrant, capability string) bool {
	for _, grant := range grants {
		if grant.RevokedAt != nil || !grant.ExpiresAt.After(time.Now()) {
			continue
		}
		var capabilities []string
		if json.Unmarshal(grant.Capabilities, &capabilities) == nil && containsString(capabilities, capability) {
			return true
		}
	}
	return false
}

func activeBrowserGrantTabs(grants []db.AgentDeviceGrant) []string {
	tabs := []string{}
	for _, grant := range grants {
		if grant.RevokedAt != nil || !grant.ExpiresAt.After(time.Now()) {
			continue
		}
		var capabilities []string
		if json.Unmarshal(grant.Capabilities, &capabilities) != nil || !containsString(capabilities, "browser.inspect") {
			continue
		}
		var metadata struct {
			Kind   string `json:"kind"`
			Label  string `json:"label"`
			Origin string `json:"origin"`
		}
		_ = json.Unmarshal(grant.Metadata, &metadata)
		if metadata.Kind != "browser_tab" {
			continue
		}
		label := strings.TrimSpace(metadata.Label)
		if label == "" {
			label = strings.TrimSpace(metadata.Origin)
		}
		if label == "" {
			label = "Browser tab"
		}
		tabs = append(tabs, label+" (scopeId "+grant.ScopeID+")")
	}
	return tabs
}





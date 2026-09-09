package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
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

func canonicalAgentToolRegistrations(handler agenttools.Handler) []agenttools.Registration {
	registrations := []agenttools.Registration{
		{Descriptor: contextGetToolDescriptor(), Handler: handler},
		{Descriptor: membersListToolDescriptor(), Handler: handler},
		{Descriptor: membersResolveToolDescriptor(), Handler: handler},
		{Descriptor: messagesSearchToolDescriptor(), Handler: handler},
		{Descriptor: messagesSendToolDescriptor(), Handler: handler},
		{Descriptor: librarySearchToolDescriptor(), Handler: handler},
		{Descriptor: tasksQueryToolDescriptor(), Handler: handler},
		{Descriptor: calendarQueryToolDescriptor(), Handler: handler},
		{Descriptor: tasksCreateToolDescriptor(), Handler: handler},
		{Descriptor: tasksUpdateToolDescriptor(), Handler: handler},
	}
	for _, descriptor := range noteAgentToolDescriptors() {
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: handler})
	}
	for _, descriptor := range drawingAgentToolDescriptors() {
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: handler})
	}
	for _, descriptor := range calendarWriteToolDescriptors() {
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: handler})
	}
	for _, descriptor := range roadmapAgentToolDescriptors() {
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: handler})
	}
	for _, descriptor := range libraryMutationToolDescriptors() {
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: handler})
	}
	for _, descriptor := range memoryAgentToolDescriptors() {
		registrations = append(registrations, agenttools.Registration{Descriptor: descriptor, Handler: handler})
	}
	return registrations
}

func canonicalProviderToolRegistration(provider string, write bool, handler agenttools.Handler) agenttools.Registration {
	return agenttools.Registration{Descriptor: canonicalProviderToolDescriptor(provider, write), Handler: handler}
}

func canonicalProviderToolDescriptor(provider string, write bool) agenttools.Descriptor {
	operation, operationLabel, risk, approval, audit := "query", "Query", serveragent.RiskRead, agenttools.ApprovalNone, ""
	if write {
		operation, operationLabel, risk, approval, audit = "write", "Write to", serveragent.RiskWrite, agenttools.ApprovalInteractive, "provider.write"
	}
	descriptor := agenttools.Descriptor{
		Name: "provider." + provider + "." + operation, Version: 1,
		Description: operationLabel + " the " + provider + " provider shared with this Space.",
		Risk:        risk, InputSchema: providerAgentToolSchema(provider, write), OutputSchema: agentToolObjectOutputSchema(), Approval: approval,
		Locality: agenttools.LocalityProvider, Idempotent: !write, AuditEvent: audit, Sources: []string{canonicalAgentToolSource},
	}
	if provider == "github" && write {
		descriptor.RequiredPermission = db.PermissionIntegrationsManage
		descriptor.AgentPermission = db.PermissionIntegrationsManage
	}
	if provider == "figma" && write {
		descriptor.RequiredPermission = db.PermissionIntegrationsManage
		descriptor.AgentPermission = db.PermissionIntegrationsManage
	}
	return descriptor
}

func TestingCanonicalAgentToolboxDescriptors(providers ...string) []agenttools.Descriptor {
	handler := func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	registrations := canonicalAgentToolRegistrations(handler)
	for _, provider := range providers {
		registrations = append(registrations, canonicalProviderToolRegistration(provider, false, handler))
		if providerSupportsWrite(provider) {
			registrations = append(registrations, canonicalProviderToolRegistration(provider, true, handler))
		}
	}
	return agenttools.MustNew(registrations...).Descriptors()
}

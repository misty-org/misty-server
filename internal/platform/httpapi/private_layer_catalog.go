package api

import (
	"context"
	"encoding/json"
	"fmt"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// validateAgentToolLayers runs at startup: every tool any run can be offered
// must declare its layer, or the server doesn't start. Toolboxes are built
// per run, so a missing layer would otherwise surface only when a run
// reached that tool.
func validateAgentToolLayers() (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("agent tool catalog: %v", recovered)
		}
	}()
	for _, descriptor := range agentToolCatalogDescriptors() {
		if descriptor.Layer != agenttools.LayerPublic && descriptor.Layer != agenttools.LayerPrivate {
			return fmt.Errorf("%w: tool %s declares no layer", agenttools.ErrInvalidRegistration, descriptor.Name)
		}
	}
	return nil
}

// agentToolCatalogDescriptors is every tool a run can be offered, built from
// stand-in runs: the Space and account toolboxes, member requests, connected
// apps, devices, screens, the run notebook, sub-agents and coding.
func agentToolCatalogDescriptors() []agenttools.Descriptor {
	service := &SpacesService{}
	noop := func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return nil, nil
	}
	record := &db.AIInvocationRecord{ID: "layer-check", UserID: "layer-check", ConversationID: "layer-check",
		RequestPayload: json.RawMessage(`{"window_label":"main","agent_id":"layer-check"}`)}
	var body aiInvocationInput
	_ = json.Unmarshal(record.RequestPayload, &body)
	grants := []deviceGrant{{scopeID: "layer-check", name: "Folder", deviceID: "device", deviceName: "Device", fetch: true}}

	// The toolboxes check their own registrations as they build; the
	// connected tools are checked as declared, since runs mix them freely.
	extra := append(agentMemberToolRegistrations(nil), service.accountAdminToolRegistrations()...)
	extra = append(extra, recallToolRegistrationsFor(service)...)
	descriptors := buildAgentToolbox(nil, agentToolboxOptions{accountLevel: true, delegation: noop, extra: extra, allLayers: true}).Descriptors()
	descriptors = append(descriptors, spaceAgentToolbox(nil).Descriptors()...)
	descriptors = append(descriptors, buildAgentToolbox(nil, agentToolboxOptions{memberRequest: true, allLayers: true}).Descriptors()...)
	connected := append(service.appsToolRegistrations(), service.webSearchToolRegistrations(context.Background(), "")...)
	connected = append(connected, webFetchWorkerRegistrations()...)
	connected = append(connected, service.webFetchDeviceRegistration(grants)...)
	connected = append(connected, service.folderToolRegistrations(grants)...)
	connected = append(connected, service.workspaceToolRegistrations(grants)...)
	connected = append(connected, service.screenToolRegistrations(record)...)
	connected = append(connected, service.notebookToolRegistrations(record, body)...)
	connected = append(connected, service.delegationToolRegistrations(record, body)...)
	connected = append(connected, service.collaborationToolRegistrations(record, collaborationState{})...)
	connected = append(connected, service.voiceToolRegistrations()...)
	for _, registration := range connected {
		descriptors = append(descriptors, registration.Descriptor)
	}
	descriptors = append(descriptors, browserToolDescriptors()...)
	return append(descriptors, codeDelegationDescriptors()...)
}

// TestingAgentToolCatalog exposes the startup catalog to the layer tests.
func TestingAgentToolCatalog() []agenttools.Descriptor {
	return agentToolCatalogDescriptors()
}

// TestingValidateAgentToolLayers runs the startup check.
func TestingValidateAgentToolLayers() error {
	return validateAgentToolLayers()
}

// TestingMemberRequestToolNames lists the tools a member request offers, for
// a teammate's ask (askerUserID differs from the agent's owner) or the
// owner's own.
func TestingMemberRequestToolNames(askerUserID, agentOwnerUserID string) []string {
	toolbox := buildAgentToolbox(nil, agentToolboxOptions{memberRequest: true, run: layerRun{askerUserID: askerUserID, agentOwnerUserID: agentOwnerUserID}})
	names := []string{}
	for _, descriptor := range toolbox.Descriptors() {
		names = append(names, descriptor.Name)
	}
	return names
}

// TestingRunToolLayers lists a full run's tools and their layers when the
// run's asker and agent owner are the ones given.
func TestingRunToolLayers(askerUserID, agentOwnerUserID string) map[string]agenttools.Layer {
	service := &SpacesService{}
	grants := []deviceGrant{{scopeID: "layer-check", name: "Folder", deviceID: "device", deviceName: "Device", fetch: true}}
	extra := append(service.appsToolRegistrations(), service.folderToolRegistrations(grants)...)
	extra = append(extra, service.workspaceToolRegistrations(grants)...)
	toolbox := buildAgentToolbox(nil, agentToolboxOptions{accountLevel: true, extra: extra,
		run: layerRun{askerUserID: askerUserID, agentOwnerUserID: agentOwnerUserID}})
	layers := map[string]agenttools.Layer{}
	for _, descriptor := range toolbox.Descriptors() {
		layers[descriptor.Name] = descriptor.Layer
	}
	return layers
}

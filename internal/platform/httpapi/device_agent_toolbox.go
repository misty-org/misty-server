package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

const deviceAgentToolSource = "device_conversation"

func resolveDeviceAgentToolbox(ctx context.Context, request serveragent.AgentMessageRequest) (serveragent.ToolManifest, error) {
	scope := strings.TrimSpace(request.ToolScope)
	requested := []string{}
	switch scope {
	case "":
	case "files":
		requested = []string{serveragent.ToolListDirectory, serveragent.ToolValidateFilePlan, serveragent.ToolApplyFilePlan}
	case "cleanup":
		requested = []string{serveragent.ToolListDirectory, serveragent.ToolSearchFiles, serveragent.ToolValidateFilePlan}
		if agentDocumentsEnabled() {
			requested = append(requested, serveragent.ToolPreviewFile)
		}
	case "search":
		requested = []string{serveragent.ToolListDirectory, serveragent.ToolSearchFiles}
		if agentDocumentsEnabled() {
			requested = append(requested, serveragent.ToolPreviewFile)
		}
	default:
		return serveragent.ToolManifest{}, serveragent.ErrInvalidRequest("tool_scope is invalid")
	}
	if len(requested) > 0 && strings.TrimSpace(request.ActiveRoot) == "" {
		return serveragent.ToolManifest{}, serveragent.ErrInvalidRequest("active_root scope is required for device tools")
	}
	toolbox := deviceAgentToolbox()
	invocation := agenttools.Invocation{Source: deviceAgentToolSource, Trigger: "message", OriginalInput: request.UserMessage}
	return toolbox.Resolve(ctx, invocation, requested, nil)
}

func deviceAgentToolbox() *agenttools.Registry {
	unavailable := func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return nil, workflowv2.ErrDeviceUnavailable
	}
	source := []string{deviceAgentToolSource}
	read := func(name, description string, schema json.RawMessage) agenttools.Registration {
		return agenttools.Registration{Descriptor: agenttools.Descriptor{
			Name: name, Version: 1, Description: description, Risk: serveragent.RiskRead, InputSchema: schema, OutputSchema: agentToolObjectOutputSchema(),
			Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityDevice, Idempotent: true, Sources: source,
		}, Handler: unavailable}
	}
	return agenttools.MustNew(
		read(serveragent.ToolListDirectory, "List one directory inside the active opaque device scope.", TestingMustAPIRawJSON(map[string]any{
			"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "additionalProperties": false,
		})),
		read(serveragent.ToolSearchFiles, "Search file metadata inside the active opaque device scope.", TestingMustAPIRawJSON(map[string]any{
			"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}, "additionalProperties": false,
		})),
		read(serveragent.ToolPreviewFile, "Read a supported document inside the active opaque device scope.", TestingMustAPIRawJSON(map[string]any{
			"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "additionalProperties": false,
		})),
		read(serveragent.ToolValidateFilePlan, "Validate a proposed file plan without changing device files.", filePlanToolSchema()),
		agenttools.Registration{Descriptor: agenttools.Descriptor{
			Name: serveragent.ToolApplyFilePlan, Version: 1, Description: "Apply an approved mkdir, move, or rename plan inside the active device scope.",
			Risk: serveragent.RiskWrite, InputSchema: filePlanToolSchema(), OutputSchema: agentToolObjectOutputSchema(), Approval: agenttools.ApprovalInteractive,
			Locality: agenttools.LocalityDevice, Idempotent: false, AuditEvent: "device.file_plan.applied", Sources: source,
		}, Handler: unavailable},
	)
}

func filePlanToolSchema() json.RawMessage {
	return TestingMustAPIRawJSON(map[string]any{
		"type": "object", "required": []string{"plan"}, "additionalProperties": false,
		"properties": map[string]any{"plan": map[string]any{"type": "object"}},
	})
}

func TestingDeviceAgentToolboxDescriptors() []agenttools.Descriptor {
	return deviceAgentToolbox().Descriptors()
}

func TestingDeviceAgentToolNames(ctx context.Context, scope, activeRoot string) ([]string, error) {
	manifest, err := resolveDeviceAgentToolbox(ctx, serveragent.AgentMessageRequest{
		ToolScope: scope, ActiveRoot: activeRoot,
		Capabilities: serveragent.ToolManifest{Tools: []serveragent.ToolDefinition{{Name: "client.injected", Risk: serveragent.RiskDangerous}}},
	})
	return manifestToolNames(manifest), err
}

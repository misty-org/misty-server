package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) executeWorkflowNodeV2(ctx context.Context, run *db.SpaceRun, agent *db.SpaceStudioResource, descriptor workflowv2.NodeDescriptor, invocation workflowv2.Invocation, prompt string, toolProviders map[string]workflowv2.NodeDescriptor) (json.RawMessage, error) {
	readResult := func() (json.RawMessage, error) {
		switch descriptor.Kind {
		case "manual_trigger", "chat_trigger", "cron_trigger", "file_changes", "library_changes", "message_trigger", "connector_trigger", "task_change_trigger", "transform", "join", "debounce", "for_each", "call_workflow":
			var value any
			if json.Unmarshal(invocation.Input, &value) != nil {
				return nil, workflowv2.ErrOutputInvalid
			}
			return TestingMustAPIRawJSON(map[string]any{"value": value, "node": descriptor.Kind}), nil
		case "condition", "switch":
			return TestingEvaluateControlBranch(descriptor.Kind, invocation)
		case "delay":
			var config struct {
				Seconds int `json:"seconds"`
			}
			_ = json.Unmarshal(invocation.Config, &config)
			if config.Seconds < 0 || config.Seconds > 86400 {
				return nil, workflowv2.ErrOutputInvalid
			}
			if config.Seconds > 0 {
				timer := time.NewTimer(time.Duration(config.Seconds) * time.Second)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			return TestingMustAPIRawJSON(map[string]any{"value": json.RawMessage(invocation.Input), "delayedSeconds": config.Seconds}), nil
		case "source_query":
			providerConfig := decodeProviderNodeConfig(invocation.Config)
			if providerConfig.Provider != "" {
				return s.providerQueryNode(ctx, run, invocation)
			}
			return s.sourceQueryNode(ctx, run, invocation)
		case "read_metadata":
			return s.readMetadataNode(ctx, run, invocation)
		case "task_query":
			return s.taskQueryNode(ctx, run, invocation)
		case "calendar_query":
			return s.calendarQueryNode(ctx, run, invocation)
		case "changed_files":
			return s.changedFilesNode(ctx, run, invocation)
		case "read_content":
			prepared, err := s.prepareContentInvocation(ctx, run, invocation)
			if err != nil {
				if errors.Is(err, workflowv2.ErrDeviceUnavailable) {
					return s.executeLeasedDeviceNode(ctx, run, descriptor, invocation)
				}
				return nil, err
			}
			return TestingNormalizeContentPage(run, prepared)
		case "agent_task":
			if s.agent == nil {
				return nil, errors.New("Agent runtime is unavailable")
			}
			var config struct {
				Instructions string `json:"instructions"`
			}
			_ = json.Unmarshal(invocation.Config, &config)
			identity := fmt.Sprintf("You are %s. Follow the pinned Agent instructions:\n%s\n\nThe available capabilities are limited to the published workflow envelope. Return a concise result grounded in the supplied input.", agent.Name, agent.Instructions)
			request := fmt.Sprintf("Complete workflow node %s. Required outcome:\n%s\n\nInput:\n%s\n\nOriginal user request:\n%s", invocation.NodeID, config.Instructions, string(invocation.Input), strings.TrimSpace(prompt))
			toolNames := make([]string, 0, len(toolProviders))
			for name := range toolProviders {
				toolNames = append(toolNames, name)
			}
			sort.Strings(toolNames)
			registrations := make([]agenttools.Registration, 0, len(toolNames))
			for _, name := range toolNames {
				provider := toolProviders[name]
				approval := agenttools.ApprovalNone
				if provider.Risk != workflowv2.RiskRead {
					approval = agenttools.ApprovalInteractive
				}
				locality := agenttools.LocalityServer
				if provider.Location == workflowv2.LocationDevice {
					locality = agenttools.LocalityDevice
				}
				registrations = append(registrations, agenttools.Registration{Descriptor: agenttools.Descriptor{
					Name: name, Version: 1, Description: "Execute the published workflow action " + provider.Kind + ".",
					Risk: agentToolRisk(provider.Risk), InputSchema: TestingMustAPIRawJSON(provider.ToolSchema), OutputSchema: TestingMustAPIRawJSON(provider.OutputSchema),
					Approval: approval, Locality: locality, Idempotent: true, AuditEvent: "workflow." + provider.Kind,
					Sources: []string{"workflow_agent_task"},
				}, Handler: func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
					config, input := workflowToolArguments(tool.Arguments)
					digest := sha256.Sum256(append([]byte(tool.Name+":"), tool.Arguments...))
					toolInvocation := workflowv2.Invocation{RunID: run.ID, NodeID: invocation.NodeID + ".tool_" + fmt.Sprintf("%x", digest[:8]), Attempt: invocation.Attempt, IdempotencyKey: fmt.Sprintf("%x", digest[:]), UserID: run.RequestingMemberID, SpaceID: run.SpaceID, Config: config, Input: input}
					return provider.Execute(toolCtx, toolInvocation)
				}})
			}
			toolbox, err := agenttools.New(registrations...)
			if err != nil {
				return nil, err
			}
			toolboxInvocation := agenttools.Invocation{
				UserID: run.RequestingMemberID, SpaceID: run.SpaceID, AgentID: run.AgentID, AgentInstanceID: run.AgentInstanceID, RunID: run.ID,
				Source: "workflow_agent_task", Trigger: run.TriggerKind, OriginalInput: prompt, DelegatedApproval: true,
			}
			manifest, err := toolbox.Resolve(ctx, toolboxInvocation, toolNames, nil)
			if err != nil {
				return nil, err
			}
			completion, err := s.agent.CompleteWithToolsContext(ctx, run.RequestingMemberID, run.BillingUserID, identity, request, serveragent.TierLow, manifest, func(toolCtx context.Context, tool serveragent.ToolRequest) (json.RawMessage, error) {
				output, toolErr := toolbox.ExecuteWithMiddleware(toolCtx, toolboxInvocation, tool, nil, agentToolboxExecutionJournal(s.database))
				if errors.Is(toolErr, agenttools.ErrToolNotFound) || errors.Is(toolErr, agenttools.ErrCapabilityDenied) || errors.Is(toolErr, agenttools.ErrApprovalRequired) {
					return nil, workflowv2.ErrCapabilityDenied
				}
				return output, toolErr
			})
			if err != nil {
				return nil, err
			}
			if structured := TestingDecodeJSONObject(completion.Text); structured != nil {
				return structured, nil
			}
			return TestingMustAPIRawJSON(map[string]any{"text": strings.TrimSpace(completion.Text), "citations": completion.Citations, "toolCalls": completion.ToolCalls}), nil
		case "notify_private":
			eventID, err := s.database.NotifyWorkflowResult(ctx, run.ID, invocation.NodeID, invocation.Input)
			return TestingMustAPIRawJSON(map[string]any{"notified": err == nil, "eventId": eventID}), err
		case "memory_write":
			id, err := s.database.WriteAgentMemoryEvent(ctx, run.AgentInstanceID, "workflow", invocation.Input)
			return TestingMustAPIRawJSON(map[string]any{"written": err == nil, "memoryEventId": id}), err
		case "create_task":
			return s.createTaskNode(ctx, run, agent, invocation)
		case "update_task":
			return s.updateTaskNode(ctx, run, invocation)
		case "http_request":
			return s.executeOutboundHTTPNode(ctx, run, invocation)
		case "write_library_artifact":
			if s.library == nil {
				return nil, workflowv2.ErrProviderMissing
			}
			var config struct {
				Filename   string `json:"filename"`
				Provenance string `json:"provenance"`
			}
			_ = json.Unmarshal(invocation.Config, &config)
			if strings.TrimSpace(config.Filename) == "" {
				config.Filename = safeGeneratedArtifactName(agent.Name, invocation.NodeID)
			}
			item, err := s.library.WriteGeneratedTextArtifact(ctx, run.RequestingMemberID, run.SpaceID, config.Filename, extractWorkflowText(invocation.Input), map[string]any{
				"kind": config.Provenance, "runId": run.ID, "nodeId": invocation.NodeID, "agentVersionId": run.AgentVersionID, "workflowVersionId": run.WorkflowVersionID,
			})
			if err != nil {
				return nil, err
			}
			return TestingMustAPIRawJSON(map[string]any{"written": true, "itemId": item.ID, "displayName": item.DisplayName, "version": item.Version}), nil
		case "update_metadata":
			return s.updateMetadataNode(ctx, run, invocation)
		case "exact_tool":
			var config struct {
				Operation string `json:"operation"`
				Provider  string `json:"provider"`
			}
			_ = json.Unmarshal(invocation.Config, &config)
			// Proposal-only tools intentionally do not mutate a provider. Any exact
			// operation that can change state must be backed by a registered adapter.
			if config.Operation == "propose_organization" {
				return TestingMustAPIRawJSON(map[string]any{"executed": false, "proposal": json.RawMessage(invocation.Input), "approvalRequired": true}), nil
			}
			if config.Provider != "" {
				return s.providerWriteNode(ctx, run, invocation)
			}
			return nil, workflowv2.ErrProviderMissing
		case "post_reply":
			var config struct {
				Destination string `json:"destination"`
				Mode        string `json:"mode"`
			}
			_ = json.Unmarshal(invocation.Config, &config)
			if config.Mode == "draft" || config.Destination == "private" {
				return TestingMustAPIRawJSON(map[string]any{"posted": false, "messageId": "", "draft": extractWorkflowText(invocation.Input)}), nil
			}
			if config.Destination != "space_chat" {
				return nil, workflowv2.ErrProviderMissing
			}
			message, err := s.database.CreateSpaceAgentMessage(ctx, run.BillingUserID, run.SpaceID, agent.ID, extractWorkflowText(invocation.Input))
			if err != nil {
				return nil, err
			}
			return TestingMustAPIRawJSON(map[string]any{"posted": true, "messageId": message.ID}), nil
		default:
			if descriptor.Location == workflowv2.LocationDevice {
				return s.executeLeasedDeviceNode(ctx, run, descriptor, invocation)
			}
			// Never acknowledge a mutation that has no concrete provider adapter.
			// A false success here would poison the idempotency journal and make a
			// later retry appear completed even though no external state changed.
			if descriptor.Risk != workflowv2.RiskRead {
				return nil, workflowv2.ErrProviderMissing
			}
			return TestingMustAPIRawJSON(map[string]any{"accepted": true, "node": descriptor.Kind, "input": json.RawMessage(invocation.Input)}), nil
		}
	}
	if descriptor.Risk == workflowv2.RiskRead {
		return readResult()
	}
	if descriptor.Risk == workflowv2.RiskDestructive {
		approvalInput := TestingWorkflowApprovalEnvelope(run, descriptor.Kind, "", "", "", invocation.Input)
		approved, err := s.database.EnsureWorkflowNodeApproval(ctx, run.ID, invocation.NodeID, descriptor.Kind, approvalInput)
		if err != nil {
			return nil, err
		}
		if !approved {
			return nil, workflowv2.ErrAwaitingApproval
		}
	}
	if descriptor.Risk == workflowv2.RiskWrite && descriptor.Kind != "notify_private" && descriptor.Kind != "memory_write" {
		var config struct {
			Provider     string `json:"provider"`
			ConnectionID string `json:"connectionId"`
			Destination  string `json:"destination"`
		}
		_ = json.Unmarshal(invocation.Config, &config)
		if config.Destination == "" {
			var rawConfig map[string]any
			_ = json.Unmarshal(invocation.Config, &rawConfig)
			for _, key := range []string{"outputDirectory", "filename"} {
				if value, _ := rawConfig[key].(string); value != "" {
					config.Destination = value
					break
				}
			}
		}
		if config.ConnectionID == "" && config.Provider != "" {
			config.ConnectionID, _ = s.database.ResolveAgentProviderConnection(ctx, run.RequestingMemberID, run.SpaceID, run.AgentInstanceID, config.Provider)
		}
		authorized := false
		if config.Provider != "slack" && config.Provider != "discord" && descriptor.Kind != "update_task" {
			var authErr error
			authorized, authErr = s.database.WorkflowWritePreauthorized(ctx, run.RequestingMemberID, run.AgentInstanceID, run.WorkflowVersionID, invocation.NodeID, config.Provider, config.ConnectionID, config.Destination)
			if authErr != nil {
				return nil, authErr
			}
		}
		if !authorized {
			approvalInput := TestingWorkflowApprovalEnvelope(run, descriptor.Kind, config.Provider, config.ConnectionID, config.Destination, invocation.Input)
			approved, approvalErr := s.database.EnsureWorkflowNodeApproval(ctx, run.ID, invocation.NodeID, descriptor.Kind, approvalInput)
			if approvalErr != nil {
				return nil, approvalErr
			}
			if !approved {
				return nil, workflowv2.ErrAwaitingApproval
			}
		}
	}
	resourceKey, fingerprint := TestingWorkflowResourceIdentity(invocation.Config, invocation.Input)
	if resourceKey != "" {
		for {
			acquired, err := s.database.AcquireWorkflowResourceLease(ctx, run.ID, invocation.NodeID, resourceKey, fingerprint, 2*time.Minute)
			if err != nil {
				return nil, err
			}
			if acquired {
				break
			}
			timer := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		defer func() {
			_ = s.database.ReleaseWorkflowResourceLease(context.Background(), run.ID, invocation.NodeID, resourceKey)
		}()
	}
	return s.database.JournalWorkflowAction(ctx, run.ID, invocation.NodeID, invocation.IdempotencyKey, descriptor.Kind, descriptor.Risk, invocation.Input, readResult)
}

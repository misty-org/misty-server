package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type agentRuntimeToolCall struct {
	RuntimeRunID      string
	CallID            string
	Name              string
	Arguments         json.RawMessage
	ApprovalHookToken string
	DeviceHookToken   string
}

type agentRuntimeToolOutcome struct {
	Result     json.RawMessage
	Approval   any
	DeviceWait bool
}

func managedMistyRun(run *db.SpaceRun) bool {
	if run == nil {
		return false
	}
	var snapshot struct {
		SystemManaged bool `json:"system_managed"`
	}
	return json.Unmarshal(run.AgentVersionSnapshot, &snapshot) == nil && snapshot.SystemManaged
}

func (s *SpacesService) resolvePersonalAgentRuntimeToolbox(ctx context.Context, run *db.SpaceRun) (*agenttools.Registry, agenttools.Invocation, agenttools.Authorizer, error) {
	if run.SourceTaskID != "" {
		toolbox, invocation, _, err := s.resolveAssignedTaskToolbox(ctx, run)
		return toolbox, invocation, authorizePersonalAgentTaskTool(s.database), err
	}

	personalAgent, err := s.database.PersonalAgentByID(ctx, run.OwnerUserID, run.AgentID)
	if err != nil {
		return nil, agenttools.Invocation{}, nil, err
	}
	delegationHandler := func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
		var input struct {
			Prompt    string `json:"prompt"`
			AgentID   string `json:"agent_id"`
			AgentName string `json:"agent_name"`
		}
		if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Prompt) == "" {
			return nil, db.ErrSpaceInvalid
		}
		targetID := strings.TrimSpace(input.AgentID)
		if personalAgent.SystemManaged {
			targetID = run.AgentID
		} else if targetID == "" {
			agents, err := s.database.AccessiblePersonalAgents(ctx, run.OwnerUserID, run.SpaceID)
			if err != nil {
				return nil, err
			}
			for _, agent := range agents {
				if strings.EqualFold(agent.Name, strings.TrimSpace(input.AgentName)) {
					if targetID != "" {
						return nil, db.ErrSpaceConflict
					}
					targetID = agent.ID
				}
			}
		}
		if targetID == "" {
			return nil, db.ErrPersonalAgentNotFound
		}
		child, err := s.database.CreateCreatorAgentRun(ctx, run.OwnerUserID, run.SpaceID, targetID, db.CreatorAgentRunInput{
			Instruction: input.Prompt,
			Mode:        run.InitialRunMode,
			ParentRunID: run.ID,
		})
		if err != nil {
			return nil, err
		}
		if personalAgent.SystemManaged {
			return TestingMustAPIRawJSON(map[string]any{"run_id": child.ID, "state": child.State, "worker": "background"}), nil
		}
		return TestingMustAPIRawJSON(map[string]any{"run_id": child.ID, "state": child.State, "agent_id": child.AgentID}), nil
	}

	browserTabs := []string{}
	browserCapabilities := map[string]bool{}
	if contexts, err := s.database.AgentRunDeviceGrants(ctx, run.OwnerUserID, run.ID); err == nil {
		browserTabs = activeBrowserGrantTabs(contexts)
		for _, descriptor := range browserToolDescriptors() {
			browserCapabilities[descriptor.Name] = activeBrowserCapability(contexts, descriptor.Name)
		}
	}
	providers := s.companionRunProviders(ctx, run)
	providerHandler := func(ctx context.Context, _ agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
		return s.executeCompanionProviderTool(ctx, run, request)
	}
	mcpHandler := func(toolCtx context.Context, _ agenttools.Invocation, tool serveragent.ToolRequest) (json.RawMessage, error) {
		return s.executeMCPAgentTool(toolCtx, run, tool, false, "space_conversation")
	}
	mcpRegistrations, _ := s.appendPersonalAgentMCPTools(ctx, run.OwnerUserID, run.AgentID, nil, nil, mcpHandler)
	toolbox := spaceAgentToolboxWithBrowserProvidersAndExtra(s.database, browserTabs, browserCapabilities, providers, providerHandler, mcpRegistrations, delegationHandler)
	names := make([]string, 0, len(toolbox.Descriptors()))
	explicit := map[string]bool{}
	for _, descriptor := range toolbox.Descriptors() {
		names = append(names, descriptor.Name)
		explicit[descriptor.Name] = true
	}
	invocation := agenttools.Invocation{
		UserID:                run.OwnerUserID,
		SpaceID:               run.SpaceID,
		AgentID:               run.AgentID,
		RunID:                 run.ID,
		Source:                "space_conversation",
		Trigger:               "message",
		OriginalInput:         string(run.Input),
		ExplicitTools:         explicit,
		DelegatedApproval:     true,
		ConversationScopeKind: db.ConversationScopeEveryone,
	}
	authorize := authorizeSpaceAgentTool(s.database)
	if _, err := toolbox.Resolve(ctx, invocation, names, authorize); err != nil {
		return nil, agenttools.Invocation{}, nil, err
	}
	return toolbox, invocation, authorize, nil
}

func (s *SpacesService) executePersonalAgentRuntimeTool(ctx context.Context, run *db.SpaceRun, call agentRuntimeToolCall) (agentRuntimeToolOutcome, error) {
	if run == nil || strings.TrimSpace(call.CallID) == "" || len(call.CallID) > 200 || len(call.Arguments) == 0 {
		return agentRuntimeToolOutcome{}, db.ErrSpaceInvalid
	}
	impact := companionToolImpact(call.Name)
	if companionToolNeedsApproval(run.EffectiveRunMode, impact) {
		digest := sha256.Sum256(call.Arguments)
		argumentsHash := hex.EncodeToString(digest[:])
		mac := hmac.New(sha256.New, s.agentRuntime.secret)
		_, _ = mac.Write([]byte(run.ID + "\n" + call.CallID + "\n" + call.Name + "\n" + argumentsHash))
		signedCall := hex.EncodeToString(mac.Sum(nil))
		approval, allowed, err := s.database.RequireCreatorToolApproval(ctx, run, call.CallID, call.Name, impact, argumentsHash, signedCall, call.ApprovalHookToken, companionToolApprovalSummary(call.Name, call.Arguments))
		if err != nil {
			return agentRuntimeToolOutcome{}, err
		}
		if !allowed {
			if approval.State == "denied" || approval.State == "expired" {
				return agentRuntimeToolOutcome{Result: TestingMustAPIRawJSON(map[string]any{"denied": true, "reason": "creator_denied", "approval_id": approval.ID})}, nil
			}
			s.projectLinkedAIInvocationApproval(ctx, run, call.Name)
			return agentRuntimeToolOutcome{Approval: approval}, nil
		}
	}

	toolbox, invocation, authorize, err := s.resolvePersonalAgentRuntimeToolbox(ctx, run)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	result, err := toolbox.ExecuteWithMiddleware(ctx, invocation, serveragent.ToolRequest{
		ID: call.CallID, Name: call.Name, Arguments: call.Arguments,
	}, authorize, agentToolboxExecutionJournal(s.database))
	if errors.Is(err, workflowv2.ErrDeviceUnavailable) {
		if waitErr := s.database.AwaitAgentRunDevice(ctx, run.ID, call.RuntimeRunID, call.DeviceHookToken); waitErr != nil {
			return agentRuntimeToolOutcome{}, waitErr
		}
		return agentRuntimeToolOutcome{DeviceWait: true}, nil
	}
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	_ = s.database.TouchPersonalAgentTaskRuntime(ctx, run.ID, call.RuntimeRunID, "used_"+strings.ReplaceAll(call.Name, ".", "_"), 15)
	return agentRuntimeToolOutcome{Result: result}, nil
}

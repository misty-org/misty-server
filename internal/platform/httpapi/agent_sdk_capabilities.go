package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func sdkAgentBinding(b *agenttools.ProviderBinding) db.AgentSDKCapabilityBinding {
	return db.AgentSDKCapabilityBinding{TargetID: b.TargetID, TargetRevision: b.TargetRevision, Capability: b.Capability, CapabilityVersion: b.CapabilityVersion, ProviderID: b.ProviderID, ProviderVersion: b.ProviderVersion, AdapterVersion: b.AdapterVersion}
}
func sdkToolBinding(b db.AgentSDKCapabilityBinding) agenttools.ProviderBinding {
	return agenttools.ProviderBinding{AdapterVersion: b.AdapterVersion, TargetID: b.TargetID, TargetRevision: b.TargetRevision, Capability: b.Capability, CapabilityVersion: b.CapabilityVersion, ProviderID: b.ProviderID, ProviderVersion: b.ProviderVersion}
}

func (s *SpacesService) agentSDKRegistrations(ctx context.Context, run *db.SpaceRun) ([]agenttools.Registration, error) {
	registrations := []agenttools.Registration{}
	if !sdkExecutionEnabled() || !managedMistyRun(run) {
		return registrations, nil
	}
	return s.sdkRunRegistrations(ctx, run.OwnerUserID, run.ID)
}

func (s *SpacesService) aiSDKRegistrations(ctx context.Context, run *db.AIInvocationRecord) ([]agenttools.Registration, error) {
	if !sdkExecutionEnabled() || run.SurfaceID == "sdk" || run.AgentRunID != "" {
		return []agenttools.Registration{}, nil
	}
	return s.sdkRunRegistrations(ctx, run.UserID, run.ID)
}

func (s *SpacesService) sdkRunRegistrations(ctx context.Context, userID, runID string) ([]agenttools.Registration, error) {
	registrations := []agenttools.Registration{}
	bindings, err := s.database.AgentSDKCapabilityBindings(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		bound, err := s.database.ResolveAgentSDKCapability(ctx, userID, runID, binding)
		if errors.Is(err, db.ErrSDKProviderUnavailable) || errors.Is(err, db.ErrAppRuntimeForbidden) || errors.Is(err, db.ErrSpaceForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		registration, err := agenttools.ProviderRegistration(bound.Provider, bound.Target, bound.Definition, func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
			// SDK tools enter the same approval/effect boundary through the runtime
			// dispatch below. The catalog handler cannot bypass that boundary.
			return nil, db.ErrAgentToolboxNotAttempted
		})
		if err != nil {
			return nil, err
		}
		registration.Descriptor.AllowCustomAgent = true
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

func authorizeAgentSDKTool(ctx context.Context, database *db.Database, invocation agenttools.Invocation, descriptor agenttools.Descriptor) (bool, error) {
	if descriptor.ProviderBinding == nil || invocation.RunID == "" {
		return false, nil
	}
	if strings.HasPrefix(invocation.RunID, "invocation_") {
		record, err := database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
		if err != nil {
			return false, err
		}
		if record.SurfaceID == "sdk" || record.AgentRunID != "" {
			return false, nil
		}
		if invocation.AgentID == "" {
			_, err := database.ResolveAgentSDKCapability(ctx, invocation.UserID, invocation.RunID, sdkAgentBinding(descriptor.ProviderBinding))
			return err == nil, err
		}
	}
	if invocation.AgentID == "" {
		return false, nil
	}
	agent, err := database.AskIdentityByID(ctx, invocation.UserID, invocation.AgentID)
	if err != nil {
		return false, err
	}
	// Custom agents need explicit dynamic capability policy controls before they
	// can inherit SDK scopes. This does not confer any official-app privilege.
	if !agent.SystemManaged {
		return false, nil
	}
	_, err = database.ResolveAgentSDKCapability(ctx, invocation.UserID, invocation.RunID, sdkAgentBinding(descriptor.ProviderBinding))
	return err == nil, err
}

type sdkExecutionRun struct {
	ID, OwnerUserID, SpaceID, AgentID, Source string
	Deadline                                  time.Time
	Approval                                  func(context.Context, string, string, string, string, string, string, string, db.ProtectedSDKApproval) (*db.AgentToolApproval, bool, error)
}

func (s *SpacesService) executeConversationalSDKTool(ctx context.Context, run *db.SpaceRun, call agentRuntimeToolCall) (agentRuntimeToolOutcome, error) {
	if !sdkExecutionEnabled() || !managedMistyRun(run) {
		return agentRuntimeToolOutcome{}, db.ErrAppRuntimeForbidden
	}
	reference := sdkExecutionRun{ID: run.ID, OwnerUserID: run.OwnerUserID, SpaceID: run.SpaceID, AgentID: run.AgentID, Source: "space_conversation", Deadline: run.CreatedAt.Add(24 * time.Hour).UTC()}
	reference.Approval = func(ctx context.Context, callID, effectID, name, hash, signature, hook, summary string, review db.ProtectedSDKApproval) (*db.AgentToolApproval, bool, error) {
		approval, allowed, err := s.database.RequireCreatorToolApproval(ctx, run, callID, name, "consequential", hash, signature, hook, summary, review)
		if err == nil && !allowed && approval.State == "pending" {
			s.projectLinkedAIInvocationApproval(ctx, run, name)
		}
		return approval, allowed, err
	}
	return s.executeScopedSDKTool(ctx, reference, call)
}
func (s *SpacesService) executeAIProviderTool(ctx context.Context, record *db.AIInvocationRecord, call agentRuntimeToolCall) (agentRuntimeToolOutcome, error) {
	if record.SurfaceID == "routine" {
		if err := s.authorizeRoutineCall(ctx, record, call); err != nil {
			return agentRuntimeToolOutcome{}, err
		}
	}
	if !sdkExecutionEnabled() || record.SurfaceID == "sdk" || record.AgentRunID != "" {
		return agentRuntimeToolOutcome{}, db.ErrAppRuntimeForbidden
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return agentRuntimeToolOutcome{}, db.ErrSpaceInvalid
	}
	reference := sdkExecutionRun{ID: record.ID, OwnerUserID: record.UserID, SpaceID: record.SpaceID, AgentID: "", Source: "ai_invocation", Deadline: minTime(record.ExpiresAt, record.CreatedAt.Add(24*time.Hour)).UTC()}
	reference.Approval = func(ctx context.Context, callID, effectID, name, hash, signature, hook, summary string, review db.ProtectedSDKApproval) (*db.AgentToolApproval, bool, error) {
		return s.database.RequireSDKToolApproval(ctx, record.UserID, record.ID, effectID, name, hash, hook, summary, review)
	}
	return s.executeScopedSDKTool(ctx, reference, call)
}
func (s *SpacesService) executeScopedSDKTool(ctx context.Context, run sdkExecutionRun, call agentRuntimeToolCall) (agentRuntimeToolOutcome, error) {
	if !sdkExecutionEnabled() || call.CallID == "" || len(call.CallID) > 200 || len(call.Arguments) == 0 {
		return agentRuntimeToolOutcome{}, db.ErrSpaceInvalid
	}

	if pending, pendingErr := s.database.SDKPendingBrowserIntervention(ctx, run.OwnerUserID, run.ID, call.RuntimeRunID, call.CallID); pendingErr != nil {
		return agentRuntimeToolOutcome{}, pendingErr
	} else if pending != nil {
		return agentRuntimeToolOutcome{}, &aiInterventionRequired{pending}
	}
	bindings, err := s.database.AgentSDKCapabilityBindings(ctx, run.OwnerUserID, run.ID)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	var binding *db.AgentSDKCapabilityBinding
	for _, candidate := range bindings {
		if agenttools.ProviderToolName(sdkToolBinding(candidate)) == call.Name {
			b := candidate
			binding = &b
			break
		}
	}
	if binding == nil {
		return agentRuntimeToolOutcome{}, db.ErrAppRuntimeForbidden
	}
	bound, err := s.database.ResolveAgentSDKCapability(ctx, run.OwnerUserID, run.ID, *binding)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	schema, err := cap.CompileSchema(bound.Definition.InputSchema)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	var input any
	if json.Unmarshal(call.Arguments, &input) != nil || schema.Validate(input) != nil {
		return agentRuntimeToolOutcome{}, cap.ErrInvalid
	}
	publicRunID := strings.TrimPrefix(strings.TrimPrefix(run.ID, "invocation_"), "run_")
	if !cap.ValidID(publicRunID) {
		return agentRuntimeToolOutcome{}, db.ErrSpaceInvalid
	}
	requestID, effectID := cap.AgentSDKIdentities(run.OwnerUserID, run.ID, call.CallID)
	execution := cap.Execution{Invocation: cap.Invocation{RequestID: requestID, Capability: binding.Capability, CapabilityVersion: binding.CapabilityVersion, ProviderID: binding.ProviderID, ProviderVersion: binding.ProviderVersion, TargetID: binding.TargetID, TargetRevision: binding.TargetRevision, Input: call.Arguments, Deadline: run.Deadline}, RunID: publicRunID, EffectID: effectID, GrantIDs: []string{}}
	if err := execution.Invocation.Validate(time.Now()); err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	var observed json.RawMessage
	var browser *sdkBrowserExecution
	registration, err := agenttools.ProviderRegistration(bound.Provider, bound.Target, bound.Definition, func(executeCtx context.Context, _ agenttools.Invocation, _ serveragent.ToolRequest) (json.RawMessage, error) {
		fresh, err := s.database.ResolveAgentSDKCapability(executeCtx, run.OwnerUserID, run.ID, *binding)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
		executeCtx, cancel, err := s.agentExecutionContext(executeCtx, run.OwnerUserID, run.ID, call.RuntimeRunID)
		if err != nil {
			return nil, err
		}
		defer cancel()
		raw, err := s.dispatchSDKProvider(executeCtx, execution, fresh, browser)
		if err != nil {
			return nil, err
		}
		observed = raw
		var outcome cap.BackendOutcome
		if json.Unmarshal(raw, &outcome) != nil {
			return nil, cap.ErrInvalid
		}
		return outcome.Result, nil
	})
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	invocation := agenttools.Invocation{UserID: run.OwnerUserID, SpaceID: run.SpaceID, AgentID: run.AgentID, RunID: run.ID, Source: run.Source, ApprovedTools: map[string]bool{}}
	if allowed, err := authorizeAgentSDKTool(ctx, s.database, invocation, registration.Descriptor); err != nil || !allowed {
		return agentRuntimeToolOutcome{}, errors.Join(db.ErrAppRuntimeForbidden, err)
	}
	if allowed, err := authorizeAppRuntimeTool(ctx, s.database, invocation, registration.Descriptor); err != nil || !allowed {
		return agentRuntimeToolOutcome{}, errors.Join(db.ErrAppRuntimeForbidden, err)
	}
	if pending, err := s.database.AgentRunPendingEffect(ctx, run.OwnerUserID, run.ID, "sdk-agent-effect:"+effectID); err != nil {
		return agentRuntimeToolOutcome{}, err
	} else if pending != nil {
		return sdkPendingRunOutcome(pending)
	}
	browser, err = s.prepareSDKBrowser(ctx, run, execution, bound, call.RuntimeRunID)
	if err != nil {
		return agentRuntimeToolOutcome{}, s.sdkBrowserPreparationError(ctx, run, call, bound, err)
	}
	rawRequest, _ := json.Marshal(execution)
	if registration.Descriptor.Approval != agenttools.ApprovalNone {
		review, err := s.protectSDKApprovalReview(execution, bound, browser)
		if err != nil {
			return agentRuntimeToolOutcome{}, err
		}
		approvalRequest := rawRequest
		if browser != nil {
			approvalRequest, _ = json.Marshal([]any{execution, browser.Prepared})
			approvalRequest, _ = cap.CanonicalJSON(approvalRequest)
		}
		hash := sha256.Sum256(approvalRequest)
		argumentsHash := hex.EncodeToString(hash[:])
		mac := hmac.New(sha256.New, s.agentRuntime.secret)
		_, _ = mac.Write([]byte(run.ID + "\n" + call.CallID + "\n" + call.Name + "\n" + argumentsHash))
		approval, allowed, err := run.Approval(ctx, call.CallID, effectID, call.Name, argumentsHash, hex.EncodeToString(mac.Sum(nil)), call.ApprovalHookToken, "Allow "+bound.Definition.Name+" on "+bound.Target.Label+"?", review)
		if err != nil {
			return agentRuntimeToolOutcome{}, err
		}
		if !allowed {
			if approval.State != "pending" {
				return agentRuntimeToolOutcome{}, errors.New("sdk_approval_denied")
			}
			return agentRuntimeToolOutcome{Approval: approval}, nil
		}
		invocation.ApprovedTools[call.Name] = true
	}
	registry, err := agenttools.New(registration)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	middleware := func(execCtx context.Context, i agenttools.Invocation, d agenttools.Descriptor, tool serveragent.ToolRequest, next agenttools.Handler) (json.RawMessage, error) {
		raw, err := s.database.JournalAgentToolboxAction(execCtx, db.AgentToolboxAction{IdempotencyKey: "sdk-agent-effect:" + effectID, RequireSettledRun: true, UserID: run.OwnerUserID, SpaceID: run.SpaceID, AgentID: run.AgentID, RunID: run.ID, ToolName: call.Name, AuditEvent: "sdk.capability", Risk: d.Risk, Source: run.Source, Request: rawRequest, RedactPayload: true,
			ProtectResult: func(raw json.RawMessage) ([]byte, error) { return s.protectAgentEffectResult(effectID+":outcome", raw) }, RestoreResult: func(raw []byte) (json.RawMessage, error) { return s.restoreAgentEffectResult(effectID+":outcome", raw) },
		}, func() (json.RawMessage, error) {
			if _, err := next(execCtx, i, tool); err != nil {
				return nil, err
			}
			return observed, nil
		})
		if err != nil {
			return nil, err
		}
		// Replayed evidence is restored from the protected journal, not regenerated.
		observed = raw
		var outcome cap.BackendOutcome
		if json.Unmarshal(raw, &outcome) != nil || outcome.Status != "success" {
			return nil, db.ErrAgentToolboxActionUnknown
		}
		return outcome.Result, nil
	}
	result, err := registry.ExecuteWithMiddleware(ctx, invocation, serveragent.ToolRequest{ID: call.CallID, Name: call.Name, Arguments: call.Arguments}, func(ctx context.Context, i agenttools.Invocation, d agenttools.Descriptor) (bool, error) {
		return authorizeAppRuntimeTool(ctx, s.database, i, d)
	}, middleware)
	var pending *db.PendingRunEffect
	if errors.As(err, &pending) {
		return sdkPendingRunOutcome(pending)
	}
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) || errors.Is(err, db.ErrAgentToolboxActionInProgress) {
		return agentRuntimeToolOutcome{Result: TestingMustAPIRawJSON(map[string]any{"status": "uncertain", "effectId": effectID, "reason": "The provider action may have completed. Reconcile it before retrying.", "evidence": []any{}})}, nil
	}
	return agentRuntimeToolOutcome{Result: result, ProviderOutcome: observed}, err
}

func sdkPendingRunOutcome(pending *db.PendingRunEffect) (agentRuntimeToolOutcome, error) {
	originalID := strings.TrimPrefix(pending.IdempotencyKey, "sdk-agent-effect:")
	if cap.ValidID(originalID) {
		return agentRuntimeToolOutcome{Result: TestingMustAPIRawJSON(map[string]any{"status": "uncertain", "effectId": originalID, "reason": "An earlier provider action remains unconfirmed. Reconcile that original action before continuing.", "evidence": []any{}})}, nil
	}
	return agentRuntimeToolOutcome{}, pending
}

// A semantic implementation replaces the old destination-specific tool in Ask's
// catalog. Required-action checks retain its semantic name across providers.
func withoutReplacedSDKTools(names []string, registrations []agenttools.Registration) []string {
	replaced := map[string]bool{}
	for _, r := range registrations {
		if r.Descriptor.ProviderBinding != nil {
			replaced[r.Descriptor.ProviderBinding.Capability] = true
		}
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if !replaced[name] {
			result = append(result, name)
		}
	}
	return result
}

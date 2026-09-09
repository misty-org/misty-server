package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func sdkExecutionEnabled() bool { return envconfig.Getenv("MISTY_SDK_EXECUTION_ENABLED") == "true" }
func (s *SpacesService) prepareSDKInvocationRuntime(ctx context.Context, record *db.AIInvocationRecord) (*preparedAIInvocationRuntime, error) {
	request, bound, ctx, err := s.sdkInvocationAuthority(ctx, record)
	_ = ctx
	if err != nil {
		return nil, err
	}
	registration, err := agenttools.ProviderRegistration(bound.Provider, bound.Target, bound.Definition, func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return nil, db.ErrSpaceForbidden
	})
	if err != nil {
		return nil, err
	}
	return &preparedAIInvocationRuntime{sdkRequest: request, body: aiInvocationInput{SurfaceID: "sdk", Timezone: "UTC", Prompt: bound.Definition.Description}, spaceID: record.SpaceID, spaceName: bound.Target.Label, spaceKind: "sdk", timezone: "UTC", currentTime: time.Now().UTC(), prompt: bound.Definition.Description, allowedTools: []string{registration.Descriptor.Name}, requiredTools: []string{registration.Descriptor.Name}}, nil
}
func (s *SpacesService) sdkInvocationAuthority(ctx context.Context, record *db.AIInvocationRecord) (*db.SDKInvocationRecord, *db.SDKBoundCapability, context.Context, error) {
	if !sdkExecutionEnabled() {
		return nil, nil, ctx, db.ErrSDKProviderUnavailable
	}
	request, err := s.database.SDKInvocationForRun(ctx, record.UserID, record.ID)
	if err != nil {
		return nil, nil, ctx, err
	}
	if request.CancelRequested || !request.Request.Deadline.After(time.Now()) {
		return nil, nil, ctx, db.ErrSDKProviderUnavailable
	}
	ctx, err = db.ContextWithPersistedAppAuthority(ctx, record.RequestPayload)
	if err != nil {
		return nil, nil, ctx, err
	}
	if err := s.database.ValidateAppExecutionAuthority(ctx, db.AppAuthorityFromContext(ctx), record.UserID, record.SpaceID, "capabilities.invoke"); err != nil {
		return nil, nil, ctx, err
	}
	bound, err := s.database.ResolveSDKBoundCapability(ctx, record.UserID, request.Request.TargetID, request.Request.TargetRevision, request.Request.Capability, request.Request.CapabilityVersion)
	if err != nil {
		return nil, nil, ctx, err
	}
	if request.AdapterVersion != cap.ExecutionAdapterVersion(bound.Provider) || bound.Provider.ID != request.Request.ProviderID || bound.Provider.Version != request.Request.ProviderVersion {
		return nil, nil, ctx, db.ErrAppRuntimeForbidden
	}
	return request, bound, ctx, nil
}
func (s *SpacesService) sdkRegistry(ctx context.Context, record *db.AIInvocationRecord) (*agenttools.Registry, error) {
	_, bound, _, err := s.sdkInvocationAuthority(ctx, record)
	if err != nil {
		return nil, err
	}
	registration, err := agenttools.ProviderRegistration(bound.Provider, bound.Target, bound.Definition, func(context.Context, agenttools.Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return nil, db.ErrSpaceForbidden
	})
	if err != nil {
		return nil, err
	}
	return agenttools.New(registration)
}
func (s *SpacesService) executeSDKRuntimeTool(ctx context.Context, record *db.AIInvocationRecord, call agentRuntimeToolCall) (agentRuntimeToolOutcome, error) {
	request, bound, authorized, err := s.sdkInvocationAuthority(ctx, record)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	if call.CallID != request.EffectID || !cap.EqualJSON(call.Arguments, request.Request.Input) {
		return agentRuntimeToolOutcome{}, db.ErrSpaceConflict
	}
	if pending, pendingErr := s.database.SDKPendingBrowserIntervention(ctx, record.UserID, record.ID, call.RuntimeRunID, call.CallID); pendingErr != nil {
		return agentRuntimeToolOutcome{}, pendingErr
	} else if pending != nil {
		return agentRuntimeToolOutcome{}, &aiInterventionRequired{pending}
	}
	if request.State != "running" && request.State != "awaiting_approval" {
		return agentRuntimeToolOutcome{}, db.ErrSpaceConflict
	}
	execution := cap.Execution{Invocation: request.Request, RunID: db.SDKPublicRunID(record.ID), EffectID: request.EffectID, GrantIDs: []string{}}
	browser, err := s.prepareSDKBrowser(ctx, sdkExecutionRun{ID: record.ID, OwnerUserID: record.UserID, SpaceID: record.SpaceID, Source: "sdk", Deadline: request.Request.Deadline}, execution, bound, call.RuntimeRunID)
	if err != nil {
		return agentRuntimeToolOutcome{}, s.sdkBrowserPreparationError(ctx, sdkExecutionRun{ID: record.ID, OwnerUserID: record.UserID, SpaceID: record.SpaceID}, call, bound, err)
	}
	registration, err := agenttools.ProviderRegistration(bound.Provider, bound.Target, bound.Definition, func(executeCtx context.Context, _ agenttools.Invocation, _ serveragent.ToolRequest) (json.RawMessage, error) {
		fresh, latest, executionCtx, err := s.sdkInvocationAuthority(executeCtx, record)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
		if fresh.State != "running" {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, db.ErrSpaceConflict)
		}
		executionCtx, cancel, err := s.agentExecutionContext(executionCtx, record.UserID, record.ID, call.RuntimeRunID)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
		defer cancel()
		return s.executeSDKBackend(executionCtx, record, fresh, latest, browser)
	})
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	if call.Name != registration.Descriptor.Name {
		return agentRuntimeToolOutcome{}, db.ErrAppRuntimeForbidden
	}
	approved := registration.Descriptor.Approval == agenttools.ApprovalNone
	if !approved {
		execution := cap.Execution{Invocation: request.Request, RunID: db.SDKPublicRunID(record.ID), EffectID: request.EffectID, GrantIDs: []string{}}
		review, err := s.protectSDKApprovalReview(execution, bound, browser)
		if err != nil {
			return agentRuntimeToolOutcome{}, err
		}
		raw, _ := json.Marshal(request.Request)
		if browser != nil {
			raw, _ = json.Marshal([]any{execution, browser.Prepared})
			raw, _ = cap.CanonicalJSON(raw)
		}
		hash := sha256.Sum256(raw)
		approval, allowed, err := s.database.RequireSDKToolApproval(authorized, record.UserID, record.ID, request.EffectID, call.Name, hex.EncodeToString(hash[:]), call.ApprovalHookToken, "Allow "+bound.Definition.Name+" on "+bound.Target.Label+"?", review)
		if err != nil {
			return agentRuntimeToolOutcome{}, err
		}
		if !allowed {
			if approval.State != "pending" {
				return agentRuntimeToolOutcome{}, errors.New("sdk_approval_denied")
			}
			outcome, _ := json.Marshal(map[string]any{"status": "approval_required", "waitId": approval.ID, "approvalId": approval.ID, "expiresAt": approval.ExpiresAt, "reason": approval.Summary})
			encrypted, err := s.protectAgentEffectResult(request.EffectID+":outcome", outcome)
			if err != nil {
				return agentRuntimeToolOutcome{}, err
			}
			if err := s.database.StoreSDKWaitOutcome(ctx, record.UserID, record.ID, "approval_required", encrypted); err != nil {
				return agentRuntimeToolOutcome{}, err
			}
			return agentRuntimeToolOutcome{Approval: approval}, nil
		}
		approved = true
	}
	registry, err := agenttools.New(registration)
	if err != nil {
		return agentRuntimeToolOutcome{}, err
	}
	invocation := agenttools.Invocation{UserID: record.UserID, SpaceID: bound.Target.SpaceID, RunID: record.ID, Source: "sdk", ApprovedTools: map[string]bool{call.Name: approved}}
	rawRequest, _ := json.Marshal(request.Request)
	middleware := func(execCtx context.Context, invocation agenttools.Invocation, descriptor agenttools.Descriptor, tool serveragent.ToolRequest, next agenttools.Handler) (json.RawMessage, error) {
		return s.database.JournalAgentToolboxAction(execCtx, db.AgentToolboxAction{IdempotencyKey: "sdk-effect:" + request.EffectID, UserID: record.UserID, SpaceID: record.SpaceID, RunID: record.ID, ToolName: call.Name, AuditEvent: "sdk.capability", Risk: descriptor.Risk, Source: "sdk", Request: rawRequest, RedactPayload: true,
			ProtectResult: func(raw json.RawMessage) ([]byte, error) {
				return s.protectAgentEffectResult(request.EffectID+":result", raw)
			},
			RestoreResult: func(raw []byte) (json.RawMessage, error) {
				return s.restoreAgentEffectResult(request.EffectID+":result", raw)
			},
		}, func() (json.RawMessage, error) { return next(execCtx, invocation, tool) })
	}
	result, err := registry.ExecuteWithMiddleware(authorized, invocation, serveragent.ToolRequest{ID: call.CallID, Name: call.Name, Arguments: call.Arguments}, func(ctx context.Context, i agenttools.Invocation, d agenttools.Descriptor) (bool, error) {
		return authorizeAppRuntimeTool(ctx, s.database, i, d)
	}, middleware)
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) || errors.Is(err, db.ErrAgentToolboxActionInProgress) {
		result, _ = json.Marshal(map[string]any{"status": "uncertain", "effectId": request.EffectID, "reason": "The action has not been confirmed. Reconcile its outcome before retrying.", "evidence": []any{}})
		return agentRuntimeToolOutcome{Result: result}, nil
	}
	return agentRuntimeToolOutcome{Result: result}, err
}
func (s *SpacesService) dispatchSDKBackend(ctx context.Context, execution cap.Execution, bound *db.SDKBoundCapability) (json.RawMessage, error) {
	// Narrow the transport deadline without changing the protected invocation or
	// effect identity. An adapter must not keep working for the original 24-hour
	// approval window after the run's active execution allowance is exhausted.
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(execution.Deadline) {
		execution.Deadline = deadline
	}
	client, err := s.sdkBackendHTTPClient(bound.Connection)
	if err != nil {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
	}
	deadline := minTime(execution.Deadline, time.Now().Add(30*time.Second))
	execution.Deadline = deadline
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	raw, _ := json.Marshal(map[string]any{"protocol": 1, "execution": execution, "target": bound.Target})
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, bound.Connection.EndpointURL, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", execution.EffectID)
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("sdk_backend_request_failed")
	}
	outcome, err := cap.ParseBackendOutcome(body, execution.Invocation, execution.EffectID, bound.Definition, time.Now())
	if err != nil {
		return nil, err
	}
	if outcome.Status != "success" {
		return nil, errors.New("sdk_backend_outcome_unconfirmed")
	}
	return body, nil
}
func (s *SpacesService) executeSDKBackend(ctx context.Context, record *db.AIInvocationRecord, request *db.SDKInvocationRecord, bound *db.SDKBoundCapability, browser *sdkBrowserExecution) (json.RawMessage, error) {
	body, err := s.dispatchSDKProvider(ctx, cap.Execution{Invocation: request.Request, RunID: db.SDKPublicRunID(record.ID), EffectID: request.EffectID, GrantIDs: []string{}}, bound, browser)
	if err != nil {
		return nil, err
	}
	var outcome cap.BackendOutcome
	if json.Unmarshal(body, &outcome) != nil {
		return nil, cap.ErrInvalid
	}

	encrypted, err := s.protectAgentEffectResult(request.EffectID+":outcome", body)
	if err != nil {
		return nil, err
	}
	persist, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelPersist()
	if err := s.database.StoreSDKObservedOutcome(persist, record.UserID, record.ID, request.EffectID, encrypted); err != nil {
		return nil, err
	}
	return outcome.Result, nil
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (s *SpacesService) completeSDKInvocation(ctx context.Context, record *db.AIInvocationRecord, cancelled bool) error {
	request, err := s.database.SDKInvocationForRun(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	evidence, err := s.database.SDKInvocationCompletionEvidence(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	status, state := "failure", "failed"
	result := map[string]any{"status": "failure", "code": "execution_not_confirmed", "message": "The requested action did not complete.", "retryable": false}
	var encrypted []byte
	switch evidence.JournalState {
	case "completed":
		if len(evidence.Proof) == 0 {
			return db.ErrAgentToolboxActionUnknown
		}
		raw, err := s.restoreAgentEffectResult(request.EffectID+":outcome", evidence.Proof)
		if err != nil {
			return err
		}
		var observed struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &observed) != nil || observed.Status != "success" {
			return db.ErrAgentToolboxActionUnknown
		}
		status, state, encrypted = "success", "completed", evidence.Proof
	case "started", "unknown":
		status = "uncertain"
		result = map[string]any{"status": "uncertain", "effectId": request.EffectID, "reason": "The action may have completed. Reconcile its result before trying again.", "evidence": []any{}}
	default:
		if cancelled || evidence.CancelRequested {
			state = "canceled"
			result["code"] = "cancelled"
			result["message"] = "The request was cancelled before a confirmed action."
		}
	}
	if len(encrypted) == 0 {
		raw, _ := json.Marshal(result)
		encrypted, err = s.protectAgentEffectResult(request.EffectID+":outcome", raw)
		if err != nil {
			return err
		}
	}
	return s.database.PublishSDKCompletion(ctx, record.UserID, record.ID, status, state, encrypted)
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/browseractions"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type sdkBrowserExecution struct {
	Session  *browseractions.Session
	Prepared browseractions.Prepared
}

func (s *SpacesService) prepareSDKBrowser(ctx context.Context, run sdkExecutionRun, e cap.Execution, bound *db.SDKBoundCapability, runtimeRunID string) (*sdkBrowserExecution, error) {
	if bound.Provider.Route.Kind != "browser" {
		return nil, nil
	}
	adapter, err := browseractions.Pilots.Resolve(bound.Provider, bound.Definition.Name)
	if err != nil {
		return nil, err
	}
	binding, err := bound.Target.ValidateBrowser(bound.Provider)
	if err != nil {
		return nil, err
	}
	if err := s.database.ValidateSDKBrowserRunContext(ctx, run.OwnerUserID, run.ID, run.SpaceID, binding); err != nil {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
	}
	session, err := browseractions.NewSession(adapter, bound.Target, bound.Provider, func(ctx context.Context, id, operation string, input json.RawMessage) (json.RawMessage, error) {
		if err := s.database.ValidateSDKBrowserRunContext(ctx, run.OwnerUserID, run.ID, run.SpaceID, binding); err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
		raw, err := s.executeBrowserAgentToolInvocation(ctx, agenttools.Invocation{UserID: run.OwnerUserID, SpaceID: run.SpaceID, RunID: run.ID, AgentID: run.AgentID}, serveragent.ToolRequest{ID: id, Name: operation, Arguments: input})
		return raw, err
	}, "sdk-"+uuid.NewString())
	if err != nil {
		return nil, err
	}
	request, _ := json.Marshal(e)
	// Reuse the effect journal for draft preparation too: transport replay must not
	// open or overwrite another draft after the logical preparation has completed.
	raw, err := s.database.JournalAgentToolboxAction(ctx, db.AgentToolboxAction{
		IdempotencyKey: "sdk-browser-prepare:" + e.EffectID, RequireSettledRun: true, UserID: run.OwnerUserID, SpaceID: run.SpaceID, AgentID: run.AgentID, RunID: run.ID, ToolName: bound.Definition.Name, AuditEvent: "sdk.browser.prepare", Risk: serveragent.RiskWrite, Source: run.Source, Request: request, RedactPayload: true,
		ProtectResult: func(raw json.RawMessage) ([]byte, error) {
			return s.protectAgentEffectResult(e.EffectID+":prepared", raw)
		},
		RestoreResult: func(raw []byte) (json.RawMessage, error) {
			return s.restoreAgentEffectResult(e.EffectID+":prepared", raw)
		},
	}, func() (json.RawMessage, error) {
		// Budget gates fresh preparation; saved preparation remains replayable.
		prepareCtx, cancel, err := s.agentExecutionContext(ctx, run.OwnerUserID, run.ID, runtimeRunID)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
		defer cancel()
		prepared, err := session.Prepare(prepareCtx, e)
		if err != nil {
			if !session.Attempted {
				return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
			}
			// A later unstarted primitive does not erase earlier autosaves or
			// navigation effects. Keep the whole preparation unconfirmed.
			return nil, errors.Join(db.ErrAgentToolboxActionUnknown, fmt.Errorf("browser preparation interrupted after an action: %v", err))
		}
		return json.Marshal(prepared)
	})
	if err != nil {
		return nil, err
	}
	var prepared browseractions.Prepared
	if json.Unmarshal(raw, &prepared) != nil {
		return nil, cap.ErrInvalid
	}
	return &sdkBrowserExecution{session, prepared}, nil
}
func (s *SpacesService) dispatchSDKProvider(ctx context.Context, e cap.Execution, bound *db.SDKBoundCapability, browser *sdkBrowserExecution) (json.RawMessage, error) {
	if bound.Provider.ID == cap.PlannerProviderID {
		return s.dispatchSDKPlanner(ctx, e, bound)
	}
	if bound.Provider.Route.Kind == "backend" {
		return s.dispatchSDKBackend(ctx, e, bound)
	}
	if bound.Provider.Route.Kind != "browser" || browser == nil {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, db.ErrSDKProviderUnavailable)
	}
	raw, err := browser.Session.Commit(ctx, e, browser.Prepared)
	var intervention *browseractions.Intervention
	if errors.Is(err, browseractions.ErrReviewChanged) || errors.As(err, &intervention) {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		invalidationErr := s.database.InvalidateSDKBrowserApproval(persist, bound.OwnerUserID, e.EffectID)
		cancel()
		if invalidationErr != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err, invalidationErr)
		}
	}
	if errors.Is(err, browseractions.ErrUncertain) {
		return nil, db.ErrAgentToolboxActionUnknown
	}
	if err != nil {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
	}
	outcome, err := cap.ParseBackendOutcome(raw, e.Invocation, e.EffectID, bound.Definition, time.Now())
	if err != nil || outcome.Status != "success" {
		return nil, db.ErrAgentToolboxActionUnknown
	}
	return raw, nil
}

// Browser adapters use the existing durable intervention wait and runtime hook.
func (s *SpacesService) sdkBrowserPreparationError(ctx context.Context, run sdkExecutionRun, call agentRuntimeToolCall, bound *db.SDKBoundCapability, cause error) error {
	if !call.SupportsIntervention {
		return cause
	}
	var intervention *browseractions.Intervention
	if !errors.As(cause, &intervention) {
		return cause
	}
	binding, err := bound.Target.ValidateBrowser(bound.Provider)
	if err != nil {
		return cause
	}
	call.Arguments, _ = json.Marshal(map[string]any{"scopeId": binding.ScopeID, "action": intervention.Action, "reason": intervention.Reason})
	_, err = s.requestAIUserAction(ctx, run.OwnerUserID, run.ID, call)
	if err != nil {
		return err
	}
	// Readiness is not an account observation. A still-invalid page must not be
	// treated as success just because this wait was already released.
	return cause
}

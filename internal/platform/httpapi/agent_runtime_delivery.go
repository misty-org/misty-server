package api

import (
	"context"
	"encoding/json"
	"errors"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"time"
)

func (s *SpacesService) ProcessAgentRuntimeDeliveries(ctx context.Context, limit int) (int, error) {
	if !s.agentRuntime.Enabled() {
		return 0, nil
	}
	if err := s.database.ExpireRoutineWaits(ctx); err != nil {
		return 0, err
	}
	if err := s.database.ExpireAIUserInterventions(ctx); err != nil {
		return 0, err
	}
	if err := s.database.ExpireSDKToolApprovals(ctx); err != nil {
		return 0, err
	}
	waits, err := s.database.AIInvocationDeviceWaitsReady(ctx, 20)
	if err != nil {
		return 0, err
	}
	for _, wait := range waits {
		if err := s.database.QueueAgentDeviceResume(ctx, wait); err != nil && !errors.Is(err, db.ErrSpaceConflict) {
			return 0, err
		}
	}
	if _, err := s.database.ReconcileStaleAIInvocations(ctx, time.Now().Add(-5*time.Minute), 20); err != nil {
		return 0, err
	}
	deliveries, err := s.database.ClaimAgentRuntimeDeliveries(ctx, min(limit, 2))
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, delivery := range deliveries {
		deliveryCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		terminal := false
		var deliveryErr error
		if delivery.Operation != "invocation.start" {
			deliveryErr = s.deliverAgentContinuation(deliveryCtx, delivery)
			terminal = errors.Is(deliveryErr, db.ErrSpaceInvalid)
		} else {
			var record *db.AIInvocationRecord
			record, deliveryErr = s.database.AIInvocationByID(deliveryCtx, delivery.UserID, delivery.RunID)
			if deliveryErr == nil && !aiInvocationTerminal(record.State) && record.RuntimeRunID == "" {
				if time.Now().After(record.ExpiresAt) {
					deliveryErr = errors.New("invocation expired before dispatch")
					terminal = true
				} else {
					deliveryErr = s.prepareInvocationDelivery(deliveryCtx, record)
					if deliveryErr == nil {
						var runtimeID string
						runtimeID, deliveryErr = s.agentRuntime.Start(deliveryCtx, record.ID)
						if deliveryErr == nil {
							deliveryErr = s.database.MarkAIInvocationDispatched(deliveryCtx, record.ID, s.agentRuntime.Kind, runtimeID)
						}
					}
				}
			}
			if deliveryErr != nil {
				// Activation may have succeeded even when its transport response was lost.
				current, lookupErr := s.database.AIInvocationByID(deliveryCtx, delivery.UserID, delivery.RunID)
				if lookupErr == nil && current.RuntimeRunID != "" {
					deliveryErr = nil
				}
			}
			if deliveryErr != nil && (delivery.Attempts >= 3 || errors.Is(deliveryErr, db.ErrSpaceInvalid) || errors.Is(deliveryErr, db.ErrAppRuntimeForbidden) || errors.Is(deliveryErr, db.ErrSpaceForbidden) || errors.Is(deliveryErr, db.ErrDeviceNotFound)) {
				terminal = true
			}
		}
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if terminal && deliveryErr != nil && delivery.Operation == "invocation.start" {
			failure := json.RawMessage(`{"type":"invocation.failed","state":"failed","error":"Misty could not prepare or start this work. Review its targets and try again."}`)
			if errors.Is(deliveryErr, errAgentRuntimeStartUnconfirmed) {
				failure = json.RawMessage(`{"type":"invocation.failed","state":"failed","code":"workflow_start_unconfirmed","error":"The original workflow start could not be confirmed. Misty stopped automatic retries to avoid submitting duplicate work."}`)
			}
			_, eventErr := s.database.CommitAIInvocationEvent(finishCtx, delivery.UserID, delivery.RunID, "delivery-failed:"+delivery.ID, "invocation.failed", failure, "failed")
			if eventErr == nil {
				if record, lookupErr := s.database.AIInvocationByID(finishCtx, delivery.UserID, delivery.RunID); lookupErr != nil {
					eventErr = lookupErr
				} else if record.SurfaceID == "routine" {
					eventErr = s.completeRoutine(finishCtx, record, false)
				} else if record.SurfaceID == "sdk" {
					eventErr = s.completeSDKInvocation(finishCtx, record, false)
				}
			}
			if eventErr != nil && !errors.Is(eventErr, db.ErrSpaceConflict) {
				finishCancel()
				return completed, eventErr
			}
		}
		finishErr := s.database.FinishAgentRuntimeDelivery(finishCtx, delivery, deliveryErr, terminal)
		finishCancel()
		if finishErr != nil {
			return completed, finishErr
		}
		if deliveryErr == nil {
			completed++
		}
	}
	return completed, nil
}

func (s *SpacesService) prepareInvocationDelivery(ctx context.Context, record *db.AIInvocationRecord) error {
	if record.SurfaceID == "routine" {
		_, err := s.prepareRoutineRuntime(ctx, record)
		return err
	}
	if record.SurfaceID == "sdk" {
		_, err := s.prepareSDKInvocationRuntime(ctx, record)
		return err
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return db.ErrSpaceInvalid
	}
	bound, err := db.ContextWithPersistedAppAuthority(ctx, record.RequestPayload)
	if err != nil {
		return err
	}
	if err := s.database.ValidateAppExecutionAuthority(bound, db.AppAuthorityFromContext(bound), record.UserID, record.SpaceID, "ai.write"); err != nil {
		return err
	}
	if err := s.database.BindAIConversationAttachments(ctx, record.UserID, record.ConversationID, record.ID, body.AttachmentIDs); err != nil {
		return err
	}
	existing, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	for _, target := range body.DeviceContexts {
		found := false
		for _, item := range existing {
			if item.DeviceID == target.DeviceID && item.Kind == target.Kind && item.OpaqueRef == target.OpaqueRef {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if _, err := s.database.AttachAIInvocationContext(ctx, record.UserID, record.ID, record.SpaceID, target.DeviceID, target.Kind, target.OpaqueRef, target.DisplayName, target.Capabilities, target.Metadata); err != nil {
			return err
		}
	}
	return nil
}

func (s *SpacesService) deliverAgentContinuation(ctx context.Context, delivery db.AgentRuntimeDelivery) error {
	var payload db.AgentContinuation
	if json.Unmarshal(delivery.Payload, &payload) != nil || payload.RuntimeID == "" {
		return db.ErrSpaceInvalid
	}
	if delivery.Operation == "runtime.cancel" {
		if err := s.agentRuntime.Cancel(ctx, payload.RuntimeID, delivery.RunID); err != nil {
			return err
		}
		if record, err := s.database.AIInvocationByID(ctx, delivery.UserID, delivery.RunID); err == nil && record.SurfaceID == "routine" {
			return s.completeRoutine(ctx, record, true)
		}
		if record, err := s.database.AIInvocationByID(ctx, delivery.UserID, delivery.RunID); err == nil && record.SurfaceID == "sdk" {
			return s.completeSDKInvocation(ctx, record, true)
		}
		return nil
	}
	if delivery.Operation == "runtime.reconcile" {
		status, err := s.agentRuntime.Status(ctx, payload.RuntimeID, delivery.RunID)
		if err != nil {
			return err
		}
		if isAIInvocationRuntimeID(delivery.RunID) {
			if payload.ObservedAfter == nil {
				return db.ErrSpaceInvalid
			}
			sdk, err := s.database.RecordAIInvocationRuntimeStatus(ctx, delivery.UserID, delivery.RunID, payload.RuntimeID, status, *payload.ObservedAfter)
			if err != nil || !sdk {
				return err
			}
			record, err := s.database.AIInvocationByID(ctx, delivery.UserID, delivery.RunID)
			if err != nil {
				return err
			}
			if record.SurfaceID == "routine" {
				return s.completeRoutine(ctx, record, status == "cancelled")
			}
			return s.completeSDKInvocation(ctx, record, false)
		}
		return s.database.RecordAgentRuntimeStatus(ctx, delivery.RunID, payload.RuntimeID, status)
	}
	if delivery.Operation == "intervention.resume" {
		current, allowed, err := s.database.AIUserInterventionContinuation(ctx, delivery, payload)
		if err != nil || !current {
			return err
		}
		if allowed && !isAIInvocationRuntimeID(delivery.RunID) {
			run, _, validateErr := s.database.ValidatePersonalAgentTaskRuntime(ctx, delivery.RunID, payload.RuntimeID)
			if validateErr != nil {
				return validateErr
			}
			toolbox, invocation, authorize, resolveErr := s.resolvePersonalAgentRuntimeToolbox(ctx, run)
			if resolveErr != nil {
				return resolveErr
			}
			manifest, resolveErr := toolbox.Resolve(ctx, invocation, []string{"browser.request_user_action"}, authorize)
			if resolveErr != nil {
				return resolveErr
			}
			allowed = len(manifest.Tools) == 1
		}
		// The adapter's opaque boolean wake hook is shared; the control plane
		// retains the distinct intervention identity and trusted user decision.
		err = s.agentRuntime.ResumeDevice(ctx, payload.HookToken, delivery.RunID, allowed)
		if err != nil {
			current, _, lookupErr := s.database.AIUserInterventionContinuation(ctx, delivery, payload)
			if lookupErr == nil && !current {
				return nil
			}
		}
		return err
	}
	if delivery.Operation != "approval.resume" && delivery.Operation != "device.resume" {
		return db.ErrSpaceInvalid
	}
	current, err := s.database.AgentContinuationCurrent(ctx, delivery, payload)
	if err != nil || !current {
		return err
	}
	if delivery.Operation == "approval.resume" {
		err = s.agentRuntime.ResumeApproval(ctx, payload.HookToken, delivery.RunID, payload.ApprovalID, payload.Approved)
	} else {
		if isAIInvocationRuntimeID(delivery.RunID) && payload.Available {
			authorized, checkErr := s.database.AIInvocationDeviceResumeAuthorized(ctx, delivery, payload)
			if checkErr != nil {
				return checkErr
			}
			payload.Available = authorized
		}
		err = s.agentRuntime.ResumeDevice(ctx, payload.HookToken, delivery.RunID, payload.Available)
	}
	if err != nil {
		// A lost response may follow an accepted hook. A later wait or terminal
		// transition is durable evidence that this particular continuation advanced.
		current, lookupErr := s.database.AgentContinuationCurrent(ctx, delivery, payload)
		if lookupErr != nil || current {
			return err
		}
		return nil
	}
	return s.database.FinishAgentContinuation(ctx, delivery, payload)
}

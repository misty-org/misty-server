package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/misty-org/misty-server/internal/platform/db"
	"time"
)

// AgentRuntimeDeliveriesEnabled reports whether this process should run the
// invocation dispatcher. Without a runtime its due work can never clear.
func (s *SpacesService) AgentRuntimeDeliveriesEnabled() bool { return s.agentRuntime.Enabled() }

func (s *SpacesService) ProcessAgentRuntimeDeliveries(ctx context.Context, limit int) (int, error) {
	if !s.agentRuntime.Enabled() {
		return 0, nil
	}
	if err := s.database.ExpireAIUserInterventions(ctx); err != nil {
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
	// Desktops claim the deliveries themselves (agent_runtime_device.go).
	return 0, nil
}

// agentDeliveryMessage is what a desktop's runtime is told for one delivery, after the
// server's own checks. Skip means there is nothing left to tell it.
type agentDeliveryMessage struct {
	Operation    string `json:"operation"`
	RunID        string `json:"run_id"`
	RuntimeRunID string `json:"runtime_run_id,omitempty"`
	HookToken    string `json:"hook_token,omitempty"`
	Available    bool   `json:"available,omitempty"`
	Skip         bool   `json:"-"`
}

// agentDeliveryOutcome is what the runtime answered: the started run's ID, a
// reconciled status, or the error that kept it from acting. Skipped means the
// server's checks found nothing to tell it.
type agentDeliveryOutcome struct {
	RuntimeRunID string
	Status       string
	Err          error
	Skipped      bool
}

var errAgentDeliveryExpired = errors.New("invocation expired before dispatch")

// planAgentDelivery runs the server's checks for one delivery and says what the
// runtime is told. Starts prepare the invocation first; continuations check
// that the wait they resume is still the current one.
func (s *SpacesService) planAgentDelivery(ctx context.Context, delivery db.AgentRuntimeDelivery) (agentDeliveryMessage, error) {
	message := agentDeliveryMessage{Operation: delivery.Operation, RunID: delivery.RunID}
	if delivery.Operation == "invocation.start" {
		if !isAIInvocationRuntimeID(delivery.RunID) {
			return message, nil
		}
		record, err := s.database.AIInvocationByID(ctx, delivery.UserID, delivery.RunID)
		if err != nil {
			return message, err
		}
		if aiInvocationTerminal(record.State) || record.RuntimeRunID != "" {
			message.Skip = true
			return message, nil
		}
		if time.Now().After(record.ExpiresAt) {
			return message, errAgentDeliveryExpired
		}
		return message, s.prepareInvocationDelivery(ctx, record)
	}
	var payload db.AgentContinuation
	if json.Unmarshal(delivery.Payload, &payload) != nil || payload.RuntimeID == "" {
		return message, db.ErrSpaceInvalid
	}
	message.RuntimeRunID = payload.RuntimeID
	switch delivery.Operation {
	case "runtime.cancel", "runtime.reconcile":
		if delivery.Operation == "runtime.reconcile" && isAIInvocationRuntimeID(delivery.RunID) && payload.ObservedAfter == nil {
			return message, db.ErrSpaceInvalid
		}
		return message, nil
	case "intervention.resume":
		current, allowed, err := s.database.AIUserInterventionContinuation(ctx, delivery, payload)
		if err != nil || !current {
			message.Skip = err == nil
			return message, err
		}
		if allowed && !isAIInvocationRuntimeID(delivery.RunID) {
			run, err := s.database.ValidatePersonalAgentTaskRuntime(ctx, delivery.RunID, payload.RuntimeID)
			if err != nil {
				return message, err
			}
			toolbox, invocation, authorize, err := s.resolvePersonalAgentRuntimeToolbox(ctx, run)
			if err != nil {
				return message, err
			}
			manifest, err := toolbox.Resolve(ctx, invocation, []string{"browser.request_user_action"}, authorize)
			if err != nil {
				return message, err
			}
			allowed = len(manifest.Tools) == 1
		}
		// The adapter's opaque boolean wake hook is shared; the control plane
		// retains the distinct intervention identity and trusted user decision.
		message.HookToken, message.Available = payload.HookToken, allowed
		return message, nil
	case "device.resume":
		current, err := s.database.AgentContinuationCurrent(ctx, delivery, payload)
		if err != nil || !current {
			message.Skip = err == nil
			return message, err
		}
		if isAIInvocationRuntimeID(delivery.RunID) && payload.Available {
			authorized, err := s.database.AIInvocationDeviceResumeAuthorized(ctx, delivery, payload)
			if err != nil {
				return message, err
			}
			payload.Available = authorized
		}
		message.HookToken, message.Available = payload.HookToken, payload.Available
		return message, nil
	}
	return message, db.ErrSpaceInvalid
}

// settleAgentDelivery records what the runtime did with a delivery and finishes
// it: done, back to the queue, or failed for good. It reports whether the
// delivery is done.
func (s *SpacesService) settleAgentDelivery(ctx context.Context, delivery db.AgentRuntimeDelivery, outcome agentDeliveryOutcome) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	deliveryErr, terminal := outcome.Err, false
	if outcome.Skipped {
		return true, s.database.FinishAgentRuntimeDelivery(ctx, delivery, nil, false)
	}
	if delivery.Operation == "invocation.start" {
		invocation := isAIInvocationRuntimeID(delivery.RunID)
		if deliveryErr == nil && invocation && outcome.RuntimeRunID != "" {
			if err := s.database.MarkAIInvocationDispatched(ctx, delivery.RunID, s.agentRuntime.Kind, outcome.RuntimeRunID); err != nil && !errors.Is(err, db.ErrSpaceConflict) {
				deliveryErr = err
			}
		}
		if deliveryErr != nil && invocation {
			// Activation may have succeeded even when its transport response was lost.
			current, lookupErr := s.database.AIInvocationByID(ctx, delivery.UserID, delivery.RunID)
			if lookupErr == nil && current.RuntimeRunID != "" {
				deliveryErr = nil
			}
		}
		terminal = deliveryErr != nil && (delivery.Attempts >= 3 || errors.Is(deliveryErr, errAgentDeliveryExpired) || errors.Is(deliveryErr, db.ErrSpaceInvalid) || errors.Is(deliveryErr, db.ErrSpaceForbidden) || errors.Is(deliveryErr, db.ErrDeviceNotFound))
		if terminal && invocation {
			failure := json.RawMessage(`{"type":"invocation.failed","state":"failed","error":"Misty could not prepare or start this work. Review its targets and try again."}`)
			if errors.Is(deliveryErr, errAgentRuntimeStartUnconfirmed) {
				failure = json.RawMessage(`{"type":"invocation.failed","state":"failed","code":"workflow_start_unconfirmed","error":"The original workflow start could not be confirmed. Misty stopped automatic retries to avoid submitting duplicate work."}`)
			}
			_, eventErr := s.database.CommitAIInvocationEvent(ctx, delivery.UserID, delivery.RunID, "delivery-failed:"+delivery.ID, "invocation.failed", failure, "failed")
			if eventErr != nil && !errors.Is(eventErr, db.ErrSpaceConflict) {
				return false, eventErr
			}
		}
	} else {
		deliveryErr = s.settleAgentContinuation(ctx, delivery, outcome)
		terminal = errors.Is(deliveryErr, db.ErrSpaceInvalid)
	}
	if err := s.database.FinishAgentRuntimeDelivery(ctx, delivery, deliveryErr, terminal); err != nil {
		return false, err
	}
	return deliveryErr == nil, nil
}

func (s *SpacesService) settleAgentContinuation(ctx context.Context, delivery db.AgentRuntimeDelivery, outcome agentDeliveryOutcome) error {
	var payload db.AgentContinuation
	if json.Unmarshal(delivery.Payload, &payload) != nil || payload.RuntimeID == "" {
		return db.ErrSpaceInvalid
	}
	switch delivery.Operation {
	case "runtime.cancel":
		return outcome.Err
	case "runtime.reconcile":
		if outcome.Err != nil {
			return outcome.Err
		}
		switch outcome.Status {
		case "pending", "running", "completed", "failed", "cancelled", "missing":
		default:
			return errors.New("runtime returned an invalid status")
		}
		if isAIInvocationRuntimeID(delivery.RunID) {
			if payload.ObservedAfter == nil {
				return db.ErrSpaceInvalid
			}
			return s.database.RecordAIInvocationRuntimeStatus(ctx, delivery.UserID, delivery.RunID, payload.RuntimeID, outcome.Status, *payload.ObservedAfter)
		}
		return s.database.RecordAgentRuntimeStatus(ctx, delivery.RunID, payload.RuntimeID, outcome.Status)
	case "intervention.resume":
		if outcome.Err != nil {
			current, _, lookupErr := s.database.AIUserInterventionContinuation(ctx, delivery, payload)
			if lookupErr == nil && !current {
				return nil
			}
		}
		return outcome.Err
	}
	if outcome.Err != nil {
		// A lost response may follow an accepted hook. A later wait or terminal
		// transition is durable evidence that this particular continuation advanced.
		current, lookupErr := s.database.AgentContinuationCurrent(ctx, delivery, payload)
		if lookupErr != nil || current {
			return outcome.Err
		}
		return nil
	}
	return s.database.FinishAgentContinuation(ctx, delivery, payload)
}

func (s *SpacesService) prepareInvocationDelivery(ctx context.Context, record *db.AIInvocationRecord) error {
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return db.ErrSpaceInvalid
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
		if _, err := s.database.AttachAIInvocationContext(ctx, record.UserID, record.ID, record.SpaceID, target.DeviceID, target.Kind, target.OpaqueRef, target.DisplayName, target.Capabilities, target.Metadata, runGrantRecord(target.RunGrant)); err != nil {
			return err
		}
	}
	return nil
}

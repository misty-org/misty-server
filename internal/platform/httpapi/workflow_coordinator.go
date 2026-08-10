package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// ProcessDueAgentWorkflows is the durable schedule coordinator entry point.
// Claims are committed before runs start, so multiple server replicas can call
// it concurrently without duplicating an occurrence.
func (s *SpacesService) ProcessDueAgentWorkflows(ctx context.Context, now time.Time, limit int) (int, error) {
	due, err := s.database.ClaimDueAgentWorkflowSchedules(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, schedule := range due {
		input := TestingMustAPIRawJSON(map[string]any{"trigger": map[string]any{"kind": "cron", "eventId": schedule.EventID, "scheduledFor": schedule.ScheduledFor.Format(time.RFC3339Nano)}})
		run, runErr := s.database.CreateAgentRun(ctx, db.AgentRunRequest{RequestingMemberID: schedule.UserID, SpaceID: schedule.SpaceID, AgentID: schedule.AgentID, SourceType: "schedule", CapabilityID: schedule.CapabilityID, Input: input, TriggerKind: "schedule"})
		if runErr != nil {
			_ = s.database.FinishWorkflowEventClaim(ctx, schedule.InstanceID, schedule.WorkflowVersionID, "cron", schedule.EventID, "", "failed")
			continue
		}
		finished, runErr := s.executeCanonicalAgentRun((&http.Request{}).WithContext(ctx), run, fmt.Sprintf("Run the scheduled workflow occurrence for %s.", schedule.ScheduledFor.Format(time.RFC3339)))
		if runErr == nil && finished != nil && finished.State == "awaiting_approval" {
			if bindErr := s.database.BindWorkflowEventClaim(ctx, schedule.InstanceID, schedule.WorkflowVersionID, "cron", schedule.EventID, run.ID); bindErr != nil {
				return processed, bindErr
			}
			processed++
			continue
		}
		state := "completed"
		if runErr != nil || finished == nil || finished.State != "completed" && finished.State != "completed_with_errors" {
			state = "failed"
		}
		if claimErr := s.database.FinishWorkflowEventClaim(ctx, schedule.InstanceID, schedule.WorkflowVersionID, "cron", schedule.EventID, run.ID, state); claimErr != nil {
			return processed, claimErr
		}
		processed++
	}
	return processed, nil
}

// ProcessProviderEvent creates one isolated run per member-owned Agent
// instance that explicitly enabled a matching connector trigger.
func (s *SpacesService) ProcessProviderEvent(ctx context.Context, resource db.ProviderSharedResource, eventID, fingerprint string, payload any) (int, error) {
	return s.ProcessNormalizedProviderEvent(ctx, resource.SpaceID, resource.Provider, resource.ExternalResourceID, resource.DisplayName, eventID, fingerprint, payload)
}

func (s *SpacesService) ProcessNormalizedProviderEvent(ctx context.Context, spaceID, provider, resourceID, displayName, eventID, fingerprint string, payload any) (int, error) {
	claimed, err := s.database.ClaimProviderWorkflows(ctx, spaceID, provider, resourceID, eventID, fingerprint, 200)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, target := range claimed {
		input := TestingMustAPIRawJSON(map[string]any{"trigger": map[string]any{"kind": "connector_event", "provider": provider, "eventId": eventID, "resourceId": resourceID, "fingerprint": fingerprint}, "event": payload})
		run, runErr := s.database.CreateAgentRun(ctx, db.AgentRunRequest{RequestingMemberID: target.UserID, SpaceID: target.SpaceID, AgentID: target.AgentID, SourceType: "connector", CapabilityID: target.CapabilityID, Input: input, TriggerKind: "connector_event"})
		if runErr != nil {
			_ = s.database.FinishWorkflowEventClaim(ctx, target.InstanceID, target.WorkflowVersionID, provider, eventID, "", "failed")
			continue
		}
		finished, runErr := s.executeCanonicalAgentRun((&http.Request{}).WithContext(ctx), run, fmt.Sprintf("Handle the new %s event from %s.", provider, displayName))
		if runErr == nil && finished != nil && finished.State == "awaiting_approval" {
			if bindErr := s.database.BindWorkflowEventClaim(ctx, target.InstanceID, target.WorkflowVersionID, provider, eventID, run.ID); bindErr != nil {
				return processed, bindErr
			}
			processed++
			continue
		}
		state := "completed"
		if runErr != nil || finished == nil || finished.State != "completed" && finished.State != "completed_with_errors" {
			state = "failed"
		}
		if claimErr := s.database.FinishWorkflowEventClaim(ctx, target.InstanceID, target.WorkflowVersionID, provider, eventID, run.ID, state); claimErr != nil {
			return processed, claimErr
		}
		processed++
	}
	return processed, nil
}

func (s *SpacesService) ProcessSpaceTaskEvent(ctx context.Context, task db.SpaceTask, eventKind string) (int, error) {
	payload := TestingMustAPIRawJSON(map[string]any{"task": task, "eventKind": eventKind})
	fingerprint := providerPayloadFingerprint(payload)
	eventID := eventKind + ":" + task.ID + ":v" + fmt.Sprint(task.Version)
	claimed, err := s.database.ClaimTaskWorkflows(ctx, task.SpaceID, task.ID, eventID, fingerprint, task.CreatedByAgentID != "", 200)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, target := range claimed {
		input := TestingMustAPIRawJSON(map[string]any{"trigger": map[string]any{"kind": "task_change", "provider": "space_tasks", "eventId": eventID, "resourceId": task.ID, "fingerprint": fingerprint}, "event": json.RawMessage(payload)})
		run, runErr := s.database.CreateAgentRun(ctx, db.AgentRunRequest{RequestingMemberID: target.UserID, SpaceID: target.SpaceID, AgentID: target.AgentID, SourceType: "task", CapabilityID: target.CapabilityID, Input: input, TriggerKind: "task_change"})
		if runErr != nil {
			_ = s.database.FinishWorkflowEventClaim(ctx, target.InstanceID, target.WorkflowVersionID, "space_tasks", eventID, "", "failed")
			continue
		}
		finished, runErr := s.executeCanonicalAgentRun((&http.Request{}).WithContext(ctx), run, "Handle the Space task change.")
		if runErr == nil && finished != nil && finished.State == "awaiting_approval" {
			if bindErr := s.database.BindWorkflowEventClaim(ctx, target.InstanceID, target.WorkflowVersionID, "space_tasks", eventID, run.ID); bindErr != nil {
				return processed, bindErr
			}
			processed++
			continue
		}
		state := "completed"
		if runErr != nil || finished == nil || finished.State != "completed" && finished.State != "completed_with_errors" {
			state = "failed"
		}
		if claimErr := s.database.FinishWorkflowEventClaim(ctx, target.InstanceID, target.WorkflowVersionID, "space_tasks", eventID, run.ID, state); claimErr != nil {
			return processed, claimErr
		}
		processed++
	}
	return processed, nil
}

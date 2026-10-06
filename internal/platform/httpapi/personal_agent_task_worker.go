package api

import (
	"context"
	"errors"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// ProcessPersonalAgentTasks advances Space run waits and dispatches claimable
// jobs. The count includes every wait transition as well as dispatched jobs, so
// a pass that made progress is not mistaken for lock contention.
func (s *SpacesService) ProcessPersonalAgentTasks(ctx context.Context, workerID string, limit int) (int, error) {
	processed := 0
	deviceWaits, err := s.database.AgentDeviceWaitsReady(ctx, 20)
	if err != nil {
		return processed, err
	}
	for _, wait := range deviceWaits {
		if err := s.database.QueueAgentDeviceResume(ctx, wait); err != nil && !errors.Is(err, db.ErrSpaceConflict) {
			return processed, err
		} else if err == nil {
			processed++
		}
	}
	reconciled, err := s.database.ReconcileStalePersonalAgentTaskRuns(ctx, time.Now().UTC().Add(-12*time.Minute), 20)
	if err != nil {
		return processed, err
	}
	processed += reconciled
	jobs, err := s.database.ClaimPersonalAgentTaskRunJobs(ctx, workerID, limit, 90*time.Second)
	if err != nil {
		return processed, err
	}
	var firstErr error
	for index := range jobs {
		job := &jobs[index]
		runtimeRunID, dispatchErr := s.agentRuntime.Start(ctx, job.Run.ID)
		if dispatchErr == nil {
			_, dispatchErr = s.database.MarkPersonalAgentTaskRunDispatched(ctx, job.Run.ID, workerID, s.agentRuntime.Kind, runtimeRunID)
		}
		if dispatchErr == nil {
			processed++
			continue
		}
		message := strings.TrimSpace(dispatchErr.Error())
		code := "agent_runtime_dispatch_failed"
		if errors.Is(dispatchErr, errAgentRuntimeStartUnconfirmed) {
			code = "workflow_start_unconfirmed"
			message = "The original workflow start is unconfirmed. Misty will recover its existing identity if available and will not submit duplicate work."
		}
		requeued, jobErr := s.database.FailPersonalAgentTaskRunJob(ctx, job.Run.ID, workerID, code, message, true)
		if errors.Is(jobErr, db.ErrSpaceConflict) {
			state, _, stateErr := s.database.PersonalAgentTaskRunJobState(ctx, job.Run.ID)
			if stateErr == nil && state == "dispatched" {
				processed++
				continue
			}
			if stateErr != nil && firstErr == nil {
				firstErr = stateErr
			}
			continue
		}
		if jobErr != nil && !errors.Is(jobErr, db.ErrSpaceConflict) {
			if firstErr == nil {
				firstErr = jobErr
			}
			continue
		}
		if requeued {
			if job.HasTask {
				_, _ = s.database.AddSpaceTaskAgentActivity(ctx, job.Task.ID, job.Run.AgentID, job.Run.ID, "status", "Agent runtime was unavailable and will retry", TestingMustAPIRawJSON(map[string]any{"attempt": job.Attempt}))
			}
			continue
		}
		if job.HasTask {
			s.finishPersonalAgentTaskRun(ctx, &job.Run, &job.Task, "", dispatchErr)
		}
		processed++
	}
	return processed, firstErr
}

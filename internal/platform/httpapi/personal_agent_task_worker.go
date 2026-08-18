package api

import (
	"context"
	"errors"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) ProcessAssignedPersonalAgentRuns(ctx context.Context, workerID string, limit int) (int, error) {
	if s.agentRuntime.Enabled() {
		if _, err := s.database.ReconcileStalePersonalAgentTaskRuns(ctx, time.Now().UTC().Add(-12*time.Minute), 20); err != nil {
			return 0, err
		}
	}
	jobs, err := s.database.ClaimPersonalAgentTaskRunJobs(ctx, workerID, limit, 90*time.Second)
	if err != nil {
		return 0, err
	}
	processed := 0
	var firstErr error
	for index := range jobs {
		job := &jobs[index]
		if s.agentRuntime.EnabledFor(job.Run.BillingUserID, job.Run.AgentID) {
			runtimeRunID, dispatchErr := s.agentRuntime.Start(ctx, job.Run.ID)
			if dispatchErr == nil {
				_, dispatchErr = s.database.MarkPersonalAgentTaskRunDispatched(ctx, job.Run.ID, workerID, s.agentRuntime.Kind, runtimeRunID)
			}
			if dispatchErr == nil {
				processed++
				continue
			}
			message := strings.TrimSpace(dispatchErr.Error())
			requeued, jobErr := s.database.FailPersonalAgentTaskRunJob(ctx, job.Run.ID, workerID, "agent_runtime_dispatch_failed", message, true)
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
				_, _ = s.database.AddSpaceTaskAgentActivity(ctx, job.Task.ID, job.Run.AgentID, job.Run.ID, "status", "Agent runtime was unavailable and will retry", TestingMustAPIRawJSON(map[string]any{"attempt": job.Attempt}))
				continue
			}
			s.finishPersonalAgentTaskRun(ctx, &job.Run, &job.Task, "", dispatchErr)
			processed++
			continue
		}
		runCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		leaseDone := make(chan struct{})
		go s.renewAssignedAgentRunLease(runCtx, cancel, leaseDone, job.Run.ID, workerID)
		result, runErr := s.executeAssignedPersonalAgentRun(runCtx, &job.Run, &job.Task)
		close(leaseDone)
		cancel()
		if runErr == nil {
			if _, err := s.database.FinishSpaceRun(ctx, job.Run.ID, "completed", result, ""); err != nil {
				runErr = err
			} else if err := s.database.CompletePersonalAgentTaskRunJob(ctx, job.Run.ID, workerID); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			} else {
				processed++
				continue
			}
		}
		message := strings.TrimSpace(runErr.Error())
		requeued, jobErr := s.database.FailPersonalAgentTaskRunJob(ctx, job.Run.ID, workerID, "agent_task_failed", message, retryableAssignedAgentRunError(runErr))
		if jobErr != nil {
			if firstErr == nil {
				firstErr = jobErr
			}
			continue
		}
		if requeued {
			_, _ = s.database.AddSpaceTaskAgentActivity(ctx, job.Task.ID, job.Run.AgentID, job.Run.ID, "status", "Agent run was interrupted and will retry", TestingMustAPIRawJSON(map[string]any{"attempt": job.Attempt}))
			continue
		}
		s.finishPersonalAgentTaskRun(ctx, &job.Run, &job.Task, "", runErr)
		processed++
	}
	return processed, firstErr
}

func (s *SpacesService) renewAssignedAgentRunLease(ctx context.Context, cancel context.CancelFunc, done <-chan struct{}, runID, workerID string) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			active, err := s.database.RenewPersonalAgentTaskRunLease(ctx, runID, workerID, 90*time.Second)
			if err != nil || !active {
				cancel()
				return
			}
		}
	}
}

func retryableAssignedAgentRunError(err error) bool {
	return !errors.Is(err, errAssignedAgentProviderUnavailable) &&
		!errors.Is(err, db.ErrSpaceForbidden) && !errors.Is(err, db.ErrPersonalAgentNotFound) &&
		!errors.Is(err, db.ErrSpaceNotFound) && !errors.Is(err, db.ErrSpaceInvalid)
}

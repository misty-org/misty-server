package api

import (
	"context"
	"errors"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) ProcessAssignedPersonalAgentRuns(ctx context.Context, workerID string, limit int) (int, error) {
	jobs, err := s.database.ClaimPersonalAgentTaskRunJobs(ctx, workerID, limit, 90*time.Second)
	if err != nil {
		return 0, err
	}
	processed := 0
	var firstErr error
	for index := range jobs {
		job := &jobs[index]
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

package api

import (
	"context"
	"errors"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) ProcessAssignedPersonalAgentRuns(ctx context.Context, workerID string, limit int) (int, error) {
	decided, err := s.database.CreatorToolApprovalResumesPending(ctx, 20)
	if err != nil {
		return 0, err
	}
	for index := range decided {
		approval := &decided[index]
		if s.agentRuntime.ResumeApproval(ctx, approval.HookToken, approval.RunID, approval.ID, approval.State == "approved") == nil {
			_ = s.database.MarkCreatorToolApprovalResumed(ctx, approval.RunID, approval.ID)
		}
	}
	expired, err := s.database.ExpireCreatorToolApprovals(ctx, 20)
	if err != nil {
		return 0, err
	}
	for index := range expired {
		approval := &expired[index]
		if s.agentRuntime.ResumeApproval(ctx, approval.HookToken, approval.RunID, approval.ID, false) == nil {
			_ = s.database.MarkExpiredCreatorToolApprovalResumed(ctx, approval.RunID, approval.ID)
		}
	}
	deviceWaits, err := s.database.AgentDeviceWaitsReady(ctx, 20)
	if err != nil {
		return 0, err
	}
	for index := range deviceWaits {
		wait := &deviceWaits[index]
		if beginErr := s.database.BeginAgentDeviceResume(ctx, wait.RunID, wait.Available); beginErr != nil {
			continue
		}
		resumeErr := s.agentRuntime.ResumeDevice(ctx, wait.HookToken, wait.RunID, wait.Available)
		_ = s.database.FinishAgentDeviceResume(ctx, wait.RunID, resumeErr == nil)
	}
	if _, err := s.database.ReconcileStalePersonalAgentTaskRuns(ctx, time.Now().UTC().Add(-12*time.Minute), 20); err != nil {
		return 0, err
	}
	jobs, err := s.database.ClaimPersonalAgentTaskRunJobs(ctx, workerID, limit, 90*time.Second)
	if err != nil {
		return 0, err
	}
	processed := 0
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

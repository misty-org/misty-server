package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// afterAIInvocationGoalRun counts a finished run against its conversation's goal
// and, when the goal is still pursued and nothing waits on the user, starts the
// next continuation on the server. It never blocks the completion callback.
func (s *SpacesService) afterAIInvocationGoalRun(record *db.AIInvocationRecord, status string, usage json.RawMessage) {
	if record == nil || record.ConversationID == "" || s.database == nil {
		return
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		measured := agentRuntimeModelUsage(usage)
		goal, err := s.database.RecordAgentGoalRun(ctx, record.UserID, record.ConversationID, record.ID, measured.InputTokens+measured.OutputTokens)
		if err != nil || goal == nil || goal.Status != "pursuing" || status == "failed" || voiceInvocation(body) {
			return
		}
		// Goals stop at a phase boundary once any usage limit is used up: the
		// phase that just ran was allowed to finish, the next one waits for the
		// limit to refill and then resumes by itself.
		if reached, resumeAt := usageLimitReached(ctx, s.database, record.UserID); reached {
			_, _ = s.database.PauseAgentGoalUntil(ctx, record.UserID, goal.ID, "Paused at your AI usage limit. Misty picks this goal up again when it refills.", resumeAt)
			return
		}
		if body.Trigger == goalContinuationTrigger && !goalReportedBy(goal, record.ID) {
			// A continuation that stopped without reporting progress would loop
			// without evidence of it; stop and let the user decide.
			_, _ = s.database.PauseAgentGoal(ctx, record.UserID, goal.ID, "A continuation ended without reporting progress. Resume when you're ready.")
			return
		}
		if mode, err := s.database.ConversationMode(ctx, record.UserID, record.ConversationID); err != nil || mode != db.ConversationModeAct {
			return
		}
		if pending, err := s.database.PendingAgentQuestionSet(ctx, record.UserID, record.ConversationID); err != nil || pending != nil {
			return
		}
		if waiting, err := s.database.PendingAgentAppRequestForRun(ctx, record.UserID, record.ID); err != nil || waiting {
			return
		}
		if plan, err := s.database.CurrentAgentPlan(ctx, record.UserID, record.ConversationID); err != nil || plan != nil && plan.State == "proposed" {
			return
		}
		// One active invocation per conversation: wait for this one to settle.
		for attempt := 0; attempt < 40; attempt++ {
			state, err := s.database.AIInvocationState(ctx, record.UserID, record.ID)
			if err != nil {
				return
			}
			if aiInvocationTerminal(state) {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
		if running, err := s.database.RunningConversationInvocation(ctx, record.UserID, record.ConversationID); err != nil || running != "" {
			return
		}
		claimed, err := s.database.ClaimAgentGoalContinuation(ctx, record.UserID, goal.ID, record.ID)
		if err != nil || !claimed {
			return
		}
		if err := s.startGoalContinuation(ctx, record, body, goal); err != nil {
			_, _ = s.database.PauseAgentGoal(ctx, record.UserID, goal.ID, "Misty couldn't start the next step toward the goal. Resume to try again.")
		}
	}()
}

func goalReportedBy(goal *db.AgentGoal, invocationID string) bool {
	var report struct {
		InvocationID string `json:"invocation_id"`
	}
	return len(goal.LastReport) > 0 && json.Unmarshal(goal.LastReport, &report) == nil && report.InvocationID == invocationID
}

func (s *SpacesService) startGoalContinuation(ctx context.Context, finished *db.AIInvocationRecord, previous aiInvocationInput, goal *db.AgentGoal) error {
	number := goal.ContinuationCount + 1
	recent, err := s.database.RecentGoalContinuations(ctx, finished.UserID)
	if err != nil {
		return err
	}
	if recent >= db.MaxDailyGoalContinuations {
		return errors.New("the daily limit for automatic goal work is reached")
	}
	body := aiInvocationInput{
		Mode: "drawer", SurfaceID: firstAIText(previous.SurfaceID, "global"), Trigger: goalContinuationTrigger,
		ConversationID: finished.ConversationID, AgentID: previous.AgentID, Timezone: previous.Timezone,
		ModelID: previous.ModelID, ReasoningEffort: previous.ReasoningEffort, ThinkingMode: previous.ThinkingMode,
		CollaborationMode: db.ConversationModeAct,
		Prompt:            fmt.Sprintf("Continue working toward the goal (continuation %d of %d).", number, goal.MaxContinuations),
		IdempotencyKey:    "goal:" + goal.ID + ":" + strconv.Itoa(number),
	}
	if err := validateAIInvocationInput(&body); err != nil {
		return err
	}
	available, err := s.database.AIActionAvailable(ctx, finished.UserID, body.SurfaceID, "ask", firstAIText(body.ModelID, aiInvocationModelID(body)))
	if err != nil {
		return err
	}
	if !available {
		return errors.New("misty is unavailable for this account right now")
	}
	payload, _ := json.Marshal(body)
	invocation, _, err := s.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		DispatchRuntime: true,
		ID:              "invocation_" + uuid.NewString(), UserID: finished.UserID, ConversationID: finished.ConversationID,
		SurfaceID: body.SurfaceID, Mode: body.Mode, Trigger: body.Trigger, State: "queued",
		IdempotencyKey: body.IdempotencyKey, RequestPayload: payload, ExpiresAt: time.Now().Add(aiInvocationTTL),
	})
	if err != nil {
		return err
	}
	if s.aiInvocations != nil {
		_, err = s.aiInvocations.restoreDurable(ctx, invocation)
	}
	return err
}

// usageLimitReached reports whether any AI limit is used up, and when every
// used-up limit has refilled.
func usageLimitReached(ctx context.Context, database *db.Database, user string) (bool, time.Time) {
	if !database.BillingService().Adapter.Enabled() {
		return false, time.Time{}
	}
	summary, err := accountBillingSummary(ctx, database, user)
	if err != nil {
		return false, time.Time{}
	}
	ai, _ := summary["ai"].(map[string]any)
	limits, _ := ai["limits"].(map[string]any)
	reached, resumeAt := false, time.Time{}
	for _, raw := range limits {
		limit, _ := raw.(map[string]any)
		remaining, _ := limit["remaining"].(float64)
		resets, _ := limit["resets_at"].(string)
		at, err := time.Parse(time.RFC3339Nano, resets)
		if remaining > 0 || err != nil {
			continue
		}
		reached = true
		if at.After(resumeAt) {
			resumeAt = at
		}
	}
	return reached, resumeAt
}

// ResumeDueGoals starts the next phase of goals whose usage limit refilled.
func (s *SpacesService) ResumeDueGoals(ctx context.Context, limit int) (int, error) {
	due, err := s.database.ClaimDueGoalResumes(ctx, limit)
	if err != nil {
		return 0, err
	}
	for _, d := range due {
		goal, err := s.database.AgentGoalByID(ctx, d.User, d.GoalID)
		if err != nil {
			continue
		}
		if reached, resumeAt := usageLimitReached(ctx, s.database, d.User); reached {
			_, _ = s.database.PauseAgentGoalUntil(ctx, d.User, d.GoalID, "Paused at your AI usage limit. Misty picks this goal up again when it refills.", resumeAt)
			continue
		}
		finished, err := s.database.AIInvocationByID(ctx, d.User, d.LastInvocationID)
		if err != nil || finished == nil {
			_, _ = s.database.PauseAgentGoal(ctx, d.User, d.GoalID, "Misty couldn't pick this goal up again. Resume to try again.")
			continue
		}
		var body aiInvocationInput
		if json.Unmarshal(finished.RequestPayload, &body) != nil {
			continue
		}
		if claimed, err := s.database.ClaimAgentGoalContinuation(ctx, d.User, goal.ID, finished.ID); err != nil || !claimed {
			continue
		}
		if err := s.startGoalContinuation(ctx, finished, body, goal); err != nil {
			_, _ = s.database.PauseAgentGoal(ctx, d.User, goal.ID, "Misty couldn't start the next step toward the goal. Resume to try again.")
		}
	}
	return len(due), nil
}

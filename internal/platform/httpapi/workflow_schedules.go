package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// scheduledRunTrigger marks invocations a workflow schedule started. The stored
// trigger kind stays "schedule"; the payload trigger keeps its historical value
// so earlier scheduled turns still read as scheduled.
const scheduledRunTrigger = "scheduled_task"

func mistyTurnSource(trigger string) string {
	if trigger == scheduledRunTrigger || trigger == goalContinuationTrigger {
		return trigger
	}
	return ""
}

type workflowScheduleInput struct {
	db.ScheduleTiming
	Inputs  map[string]any `json:"inputs"`
	Enabled *bool          `json:"enabled"`
}

// WorkflowSchedule sets or removes a workflow's schedule.
func (s *AIService) WorkflowSchedule() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		methodID := strings.TrimSpace(chi.URLParam(r, "methodID"))
		switch r.Method {
		case http.MethodPut:
			var body workflowScheduleInput
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			saved, err := saveWorkflowSchedule(r.Context(), s.database, userID, methodID, body, time.Now().UTC())
			if err != nil {
				writeWorkflowScheduleError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"schedule": saved})
		case http.MethodDelete:
			if err := s.database.DeleteWorkflowSchedule(r.Context(), userID, methodID); err != nil {
				TestingWriteAIError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeWorkflowScheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSpaceInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_schedule", "message": "Check the schedule: each rule needs its days and times, and every required input needs a value."})
	case errors.Is(err, db.ErrSpaceConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "schedule_limit", "message": "You have reached the limit of 50 scheduled workflows."})
	default:
		writeAgentMethodError(w, err)
	}
}

// saveWorkflowSchedule checks the workflow and its inputs, then stores the
// schedule. A new schedule gets its own conversation with the workflow's agent.
func saveWorkflowSchedule(ctx context.Context, database *db.Database, userID, methodID string, body workflowScheduleInput, now time.Time) (*db.WorkflowSchedule, error) {
	method, err := database.AgentMethodByID(ctx, userID, methodID)
	if err != nil {
		return nil, err
	}
	if method.Kind != "workflow" {
		return nil, db.ErrSpaceInvalid
	}
	if _, err := db.RenderAgentMethod(method.Definition, body.Inputs); err != nil {
		return nil, db.ErrSpaceInvalid
	}
	conversationID := ""
	existing, err := database.WorkflowScheduleByMethod(ctx, userID, methodID)
	if errors.Is(err, db.ErrSpaceNotFound) {
		if conversationID, err = createScheduleConversation(ctx, database, userID, method.Definition.Title, method.AgentID); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	enabled := body.Enabled == nil || *body.Enabled
	if body.Enabled == nil && existing != nil {
		enabled = existing.Enabled
	}
	return database.SaveWorkflowSchedule(ctx, userID, conversationID, db.WorkflowSchedule{
		MethodID: methodID, ScheduleTiming: body.ScheduleTiming, Inputs: body.Inputs, Enabled: enabled,
	}, now)
}

// createScheduleConversation gives a schedule its own conversation with the
// workflow's agent, so every scheduled run reads as one thread.
func createScheduleConversation(ctx context.Context, database *db.Database, userID, title, agentID string) (string, error) {
	identity, err := database.AskIdentityByID(ctx, userID, agentID)
	if err != nil {
		return "", err
	}
	conversationID, err := database.CreatePersonalAgentConversation(ctx, userID, "", identity.ID)
	if err != nil {
		return "", err
	}
	if err := database.RenameAgentSession(ctx, userID, conversationID, cleanMistyTitle(title)); err != nil {
		return "", err
	}
	reasoning := agent.ManagedReasoning("", "")
	return conversationID, database.UpdateMistyConversationModel(ctx, userID, conversationID, agent.FrontierDefaultModelID(), reasoning, agent.FrontierModelCatalogVersion)
}

// ProcessDueWorkflowSchedules starts every workflow whose time has come. Each run
// is an ordinary metered invocation in the schedule's conversation.
func (s *AIService) ProcessDueWorkflowSchedules(ctx context.Context, now time.Time, limit int) (int, error) {
	items, err := s.database.ClaimDueWorkflowSchedules(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, item := range items {
		if err := s.startWorkflowScheduleRun(ctx, item, now); err == nil {
			started++
		}
	}
	return started, nil
}

func (s *AIService) startWorkflowScheduleRun(ctx context.Context, item db.WorkflowSchedule, now time.Time) error {
	fail := func(err error) error {
		_ = s.database.CompleteWorkflowScheduleRun(ctx, item, err, now)
		return err
	}
	available, err := s.database.AIActionAvailable(ctx, item.UserID, "global", "ask", agent.InitialSelectedModelID)
	if err != nil || !available {
		return fail(errors.Join(err, errors.New("misty is unavailable for this account right now")))
	}
	if !s.agentRuntime.Enabled() {
		return fail(errors.New("the agent runtime is not configured"))
	}
	// Every run uses the workflow's latest version.
	method, err := s.database.AgentMethodByID(ctx, item.UserID, item.MethodID)
	if err != nil {
		return fail(err)
	}
	conversationID := item.ConversationID
	if conversationID == "" {
		// The conversation expired or was deleted; start a fresh thread.
		if conversationID, err = createScheduleConversation(ctx, s.database, item.UserID, method.Definition.Title, method.AgentID); err != nil {
			return fail(err)
		}
	}
	bound, err := s.database.AgentConversationIdentity(ctx, item.UserID, conversationID)
	if err != nil {
		return fail(err)
	}
	if bound.AgentID != method.AgentID {
		return fail(errors.New("the workflow's agent no longer matches its schedule conversation"))
	}
	if _, err := s.database.AskExecutionContext(ctx, item.UserID, "", bound.AgentID); err != nil {
		return fail(err)
	}
	scheduledAt := now
	if item.NextRunAt != nil {
		scheduledAt = *item.NextRunAt
	}
	body := aiInvocationInput{
		Mode: "drawer", SurfaceID: "global", Trigger: scheduledRunTrigger,
		ConversationID: conversationID, AgentID: bound.AgentID, Timezone: item.Timezone,
		IdempotencyKey: "workflow-schedule:" + item.ID + ":" + scheduledAt.UTC().Format(time.RFC3339),
	}
	body.MethodVersionID = method.VersionID
	body.MethodInputs = item.Inputs
	// Device targets fail truthfully here until a native device hands off a
	// fresh lease; scheduled runs never fall back to ambient control.
	if _, err := resolveInvocationMethod(ctx, s.database, item.UserID, &body); err != nil {
		return fail(err)
	}
	if err := pinInvocationSkills(ctx, s.database, item.UserID, &body); err != nil {
		return fail(err)
	}
	payload, _ := json.Marshal(body)
	invocation, created, err := s.database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{
		ID: "invocation_" + uuid.NewString(), UserID: item.UserID, ConversationID: conversationID,
		SurfaceID: "global", Mode: "drawer", Trigger: "schedule", State: "queued",
		IdempotencyKey: body.IdempotencyKey, RequestPayload: payload, ExpiresAt: now.Add(aiInvocationTTL),
	})
	if err != nil {
		return fail(err)
	}
	if err := s.database.BindWorkflowScheduleRun(ctx, item, conversationID, invocation.ID); err != nil {
		return fail(err)
	}
	if _, err := s.invocations.restoreDurable(ctx, invocation); err != nil {
		return fail(err)
	}
	if aiInvocationTerminal(invocation.State) {
		// This occurrence already ran (a retried claim); settle it instead of waiting.
		return s.database.CompleteWorkflowScheduleRun(ctx, item, nil, now)
	}
	if !created || invocation.RuntimeRunID != "" {
		return nil
	}
	if _, err := s.agentRuntime.Start(ctx, invocation.ID); err != nil {
		s.invocations.fail(invocation.ID, "Misty could not start the agent runtime. Please try again.")
		return fail(err)
	}
	return nil
}

// completeWorkflowScheduleInvocation settles the schedule that started a finished invocation.
func (s *SpacesService) completeWorkflowScheduleInvocation(ctx context.Context, record *db.AIInvocationRecord, runErr error) error {
	schedule, err := s.database.WorkflowScheduleByInvocation(ctx, record.ID)
	if err != nil {
		return err
	}
	return s.database.CompleteWorkflowScheduleRun(ctx, *schedule, runErr, time.Now().UTC())
}

// UpcomingWorkflowRuns lists schedules across the account's agents.
func (s *AIService) UpcomingWorkflowRuns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		items, err := s.database.UpcomingWorkflowRuns(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": items})
	}
}

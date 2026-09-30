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

// scheduledTaskTrigger marks invocations a scheduled task started. The stored trigger kind
// stays "schedule"; the payload trigger tells scheduled tasks apart from recurring briefings.
const scheduledTaskTrigger = "scheduled_task"

const maxScheduledTasksPerUser = 50

func mistyTurnSource(trigger string) string {
	if trigger == scheduledTaskTrigger {
		return scheduledTaskTrigger
	}
	return ""
}

type scheduledTaskInput struct {
	AgentID string `json:"agent_id"`
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Enabled *bool  `json:"enabled"`
	db.ScheduledTaskSchedule
}

func (input scheduledTaskInput) task(id string) db.ScheduledTask {
	enabled := input.Enabled == nil || *input.Enabled
	return db.ScheduledTask{
		ID: id, Title: strings.TrimSpace(input.Title), Prompt: strings.TrimSpace(input.Prompt),
		Enabled: enabled, ScheduledTaskSchedule: input.ScheduledTaskSchedule,
	}
}

// ScheduledTasks lists and creates the signed-in person's scheduled tasks.
func (s *AIService) ScheduledTasks() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		items, err := s.database.ScheduledTasks(r.Context(), userID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"tasks": items})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body scheduledTaskInput
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		task := body.task("")
		if !db.ValidScheduledTask(task) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_scheduled_task", "message": "Give the task a name, instructions, and a valid time."})
			return
		}
		if len(items) >= maxScheduledTasksPerUser {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "scheduled_task_limit", "message": "You have reached the limit of 50 scheduled tasks."})
			return
		}
		conversationID, err := s.createScheduledTaskConversation(r.Context(), userID, task.Title, strings.TrimSpace(body.AgentID))
		if err != nil {
			writePersonalAgentError(w, err)
			return
		}
		created, err := s.database.CreateScheduledTask(r.Context(), userID, conversationID, task, time.Now().UTC())
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"task": created})
	}
}

// ScheduledTask edits or deletes one task.
func (s *AIService) ScheduledTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "taskID"))
		switch r.Method {
		case http.MethodPut:
			var body scheduledTaskInput
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			updated, err := s.database.UpdateScheduledTask(r.Context(), userID, body.task(id), time.Now().UTC())
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"task": updated})
		case http.MethodDelete:
			if err := s.database.DeleteScheduledTask(r.Context(), userID, id); err != nil {
				TestingWriteAIError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// RunScheduledTask makes a task due now; the scheduler starts it within a minute.
func (s *AIService) RunScheduledTask() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		id := strings.TrimSpace(chi.URLParam(r, "taskID"))
		if err := s.database.RunScheduledTaskNow(r.Context(), userID, id, time.Now().UTC()); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *AIService) createScheduledTaskConversation(ctx context.Context, userID, title, agentID string) (string, error) {
	var identity *db.AskIdentity
	var err error
	if agentID == "" {
		identity, err = s.database.EnsureAskIdentity(ctx, userID, agent.FrontierDefaultModelID())
	} else {
		identity, err = s.database.AskIdentityByID(ctx, userID, agentID)
	}
	if err != nil {
		return "", err
	}
	conversationID, err := s.database.CreatePersonalAgentConversation(ctx, userID, "", identity.ID)
	if err != nil {
		return "", err
	}
	if err := s.database.RenameAgentSession(ctx, userID, conversationID, cleanMistyTitle(title)); err != nil {
		return "", err
	}
	reasoning := agent.ManagedReasoning("", "")
	return conversationID, s.database.UpdateMistyConversationModel(ctx, userID, conversationID, agent.FrontierDefaultModelID(), reasoning, agent.FrontierModelCatalogVersion)
}

// ProcessDueScheduledTasks starts every task whose time has come. Each run is an ordinary
// metered invocation in the task's conversation, so it shows up as a normal chat turn.
func (s *AIService) ProcessDueScheduledTasks(ctx context.Context, now time.Time, limit int) (int, error) {
	items, err := s.database.ClaimDueScheduledTasks(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, item := range items {
		if err := s.startScheduledTaskRun(ctx, item, now); err == nil {
			started++
		}
	}
	return started, nil
}

func (s *AIService) startScheduledTaskRun(ctx context.Context, item db.ScheduledTask, now time.Time) error {
	fail := func(err error) error {
		_ = s.database.CompleteScheduledTaskRun(ctx, item, err, now)
		return err
	}
	available, err := s.database.AIActionAvailable(ctx, item.UserID, "global", "ask", agent.InitialSelectedModelID)
	if err != nil || !available {
		return fail(errors.Join(err, errors.New("misty is unavailable for this account right now")))
	}
	if !s.agentRuntime.Enabled() {
		return fail(errors.New("the agent runtime is not configured"))
	}
	conversationID := item.ConversationID
	if conversationID == "" {
		// The conversation expired or was deleted; start a fresh thread for this task.
		if conversationID, err = s.createScheduledTaskConversation(ctx, item.UserID, item.Title, item.AgentID); err != nil {
			return fail(err)
		}
	}
	// Resolve from the bound conversation, so every occurrence uses the same agent's tools
	// and instructions, including legacy tasks that predate explicit assignments.
	bound, err := s.database.AgentConversationIdentity(ctx, item.UserID, conversationID)
	if err != nil {
		return fail(err)
	}
	if item.AgentID != "" && bound.AgentID != item.AgentID {
		return fail(errors.New("the scheduled task's agent no longer matches its conversation"))
	}
	if _, err := s.database.AskExecutionContext(ctx, item.UserID, "", bound.AgentID); err != nil {
		return fail(err)
	}
	scheduledAt := now
	if item.NextRunAt != nil {
		scheduledAt = *item.NextRunAt
	}
	body := aiInvocationInput{
		Mode: "drawer", SurfaceID: "global", Trigger: scheduledTaskTrigger, Prompt: item.Prompt,
		ConversationID: conversationID, AgentID: bound.AgentID, Timezone: item.Timezone,
		IdempotencyKey: "scheduled-task:" + item.ID + ":" + scheduledAt.UTC().Format(time.RFC3339),
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
	if err := s.database.BindScheduledTaskRun(ctx, item, conversationID, invocation.ID); err != nil {
		return fail(err)
	}
	if _, err := s.invocations.restoreDurable(ctx, invocation); err != nil {
		return fail(err)
	}
	if aiInvocationTerminal(invocation.State) {
		// This occurrence already ran (a retried claim); settle the task instead of waiting.
		return s.database.CompleteScheduledTaskRun(ctx, item, nil, now)
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

// completeScheduledTaskInvocation settles the task that started a finished invocation.
func (s *SpacesService) completeScheduledTaskInvocation(ctx context.Context, record *db.AIInvocationRecord, runErr error) error {
	task, err := s.database.ScheduledTaskByInvocation(ctx, record.ID)
	if err != nil {
		return err
	}
	return s.database.CompleteScheduledTaskRun(ctx, *task, runErr, time.Now().UTC())
}

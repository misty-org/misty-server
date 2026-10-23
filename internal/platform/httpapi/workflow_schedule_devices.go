package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// Scheduled runs on a computer (roadmap C1). When a schedule names a computer,
// its due runs wait for Misty there: the app lists them, claims one, and starts
// it as an ordinary invocation that carries its own signed browser grant. The
// server never runs them in the cloud.

// DeviceScheduledRuns lists runs waiting for one of the caller's computers.
func (s *AIService) DeviceScheduledRuns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
		if deviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "device_required"})
			return
		}
		runs, err := s.database.DeviceScheduledRuns(r.Context(), userID, deviceID, time.Now().UTC())
		if err != nil {
			WriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
	}
}

// ClaimDeviceScheduledRun gives the computer a one-time claim and what it
// needs to start the run. The run itself must then carry that computer's
// signed grant, or the claim is refused.
func (s *AIService) ClaimDeviceScheduledRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			DeviceID string `json:"device_id"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		token, err := security.GenerateSecureToken()
		if err != nil {
			WriteAIError(w, err)
			return
		}
		now := time.Now().UTC()
		schedule, err := s.database.ClaimDeviceScheduledRun(r.Context(), userID, chi.URLParam(r, "scheduleID"), strings.TrimSpace(body.DeviceID), security.HashToken(token), now)
		if errors.Is(err, db.ErrSpaceConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "schedule_not_waiting", "message": "This run already started or isn't waiting for this computer."})
			return
		}
		if err != nil {
			WriteAIError(w, err)
			return
		}
		method, err := s.database.AgentMethodByID(r.Context(), userID, schedule.MethodID)
		if err != nil {
			writeAgentMethodError(w, err)
			return
		}
		conversationID := schedule.ConversationID
		if conversationID == "" {
			if conversationID, err = createScheduleConversation(r.Context(), s.database, userID, method.Definition.Title, method.AgentID); err != nil {
				WriteAIError(w, err)
				return
			}
		}
		due := now
		if schedule.DueAt != nil {
			due = *schedule.DueAt
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"claim": token, "schedule_id": schedule.ID, "agent_id": method.AgentID, "conversation_id": conversationID,
			"method_version_id": method.VersionID, "inputs": schedule.Inputs, "timezone": schedule.Timezone, "title": method.Definition.Title,
			"due_at": due, "missed_count": schedule.MissedCount,
			"idempotency_key": "workflow-schedule:" + schedule.ID + ":" + due.UTC().Format(time.RFC3339),
		})
	}
}

// deviceScheduledRunAdmission is a scheduled run a computer is starting.
type deviceScheduledRunAdmission struct {
	schedule *db.WorkflowSchedule
}

// admitDeviceScheduledRun checks a computer's claim on a waiting scheduled run
// and fills the invocation from the schedule, so the request can't run some
// other workflow under the schedule's name. The run must carry the browser
// grant of the schedule's computer.
func (s *AIService) admitDeviceScheduledRun(ctx context.Context, userID string, body *aiInvocationInput) (*deviceScheduledRunAdmission, error) {
	if body.ScheduleID == "" || body.ScheduleClaim == "" {
		return nil, errors.New("a scheduled run is started by its schedule")
	}
	devices := []string{}
	for _, grant := range body.DeviceContexts {
		if grant.Kind == "workspace" && grant.RunGrant != nil {
			devices = append(devices, strings.TrimSpace(grant.DeviceID))
		}
	}
	if len(devices) == 0 {
		return nil, errors.New("a scheduled browser run needs its computer's Misty browser")
	}
	now := time.Now().UTC()
	schedule, err := s.database.ConsumeDeviceScheduledRunClaim(ctx, userID, body.ScheduleID, security.HashToken(body.ScheduleClaim), devices, now)
	if err != nil {
		return nil, errors.New("this scheduled run isn't waiting for this computer")
	}
	method, err := s.database.AgentMethodByID(ctx, userID, schedule.MethodID)
	if err != nil {
		_ = s.database.CompleteWorkflowScheduleRun(ctx, *schedule, err, now)
		return nil, err
	}
	due := now
	if schedule.DueAt != nil {
		due = *schedule.DueAt
	}
	body.ScheduleClaim = ""
	body.AgentID = method.AgentID
	body.MethodVersionID = method.VersionID
	body.MethodInputs = schedule.Inputs
	body.Timezone = schedule.Timezone
	body.Mode, body.SurfaceID, body.Trigger = "drawer", "global", scheduledRunTrigger
	body.IdempotencyKey = "workflow-schedule:" + schedule.ID + ":" + due.UTC().Format(time.RFC3339)
	return &deviceScheduledRunAdmission{schedule: schedule}, nil
}

// release settles the occurrence as failed when its invocation couldn't be
// created, so the schedule moves on and says why.
func (admission *deviceScheduledRunAdmission) release(ctx context.Context, database *db.Database, cause error) {
	if admission == nil {
		return
	}
	_ = database.CompleteWorkflowScheduleRun(ctx, *admission.schedule, cause, time.Now().UTC())
}

// lateNotice says when a run started after it was due, and how many later
// runs it stands in for.
func (admission *deviceScheduledRunAdmission) lateNotice(now time.Time) string {
	schedule := admission.schedule
	if schedule.DueAt == nil || now.Sub(*schedule.DueAt) < 2*time.Minute {
		return ""
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		location = time.UTC
	}
	text := "This run was due " + schedule.DueAt.In(location).Format("Mon Jan 2 at 3:04 PM") + " and started now, when its computer was back."
	if schedule.MissedCount == 1 {
		text += " One later run passed while it waited; this run covers it."
	} else if schedule.MissedCount > 1 {
		text += fmt.Sprintf(" %d later runs passed while it waited; this run covers them.", schedule.MissedCount)
	}
	return text
}

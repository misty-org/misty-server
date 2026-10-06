package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// ConversationCollaboration returns a conversation's mode, current plan, goal,
// recent questions and any running invocation (a goal continuation started
// without the desktop), so a client can restore everything on open.
func (s *SpacesService) ConversationCollaboration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		conversation := chi.URLParam(r, "conversationID")
		if _, err := s.database.AgentConversationIdentity(r.Context(), user, conversation); err != nil {
			writeSpaceError(w, db.ErrSpaceNotFound)
			return
		}
		mode, err := s.database.ConversationMode(r.Context(), user, conversation)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		plan, err := s.database.CurrentAgentPlan(r.Context(), user, conversation)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		goal, err := s.database.CurrentAgentGoal(r.Context(), user, conversation)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		questions, err := s.database.AgentQuestionSets(r.Context(), user, conversation, 10)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		running, err := s.database.RunningConversationInvocation(r.Context(), user, conversation)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		history, err := s.database.AgentPlanHistory(r.Context(), user, conversation, 10)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "plan": plan, "planHistory": history, "goal": goal, "questionSets": questions, "runningInvocationId": running})
	}
}

// ConversationMode switches a conversation between Plan and Act. The next turn
// is admitted in the new mode; a running turn keeps the mode it started with.
func (s *SpacesService) ConversationModeControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Mode string `json:"mode"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.SetConversationMode(r.Context(), user, chi.URLParam(r, "conversationID"), strings.TrimSpace(body.Mode)); err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": strings.TrimSpace(body.Mode)})
	}
}

// questionContinuationPrompt is the visible turn that carries answers given
// after the asking run handed off.
func questionContinuationPrompt(set *db.AgentQuestionSet) string {
	var out strings.Builder
	out.WriteString("My answers to your questions:")
	for index, question := range set.Questions {
		if index >= len(set.Answers) {
			break
		}
		answer := set.Answers[index]
		parts := append([]string{}, answer.Selected...)
		if answer.Other != "" {
			parts = append(parts, answer.Other)
		}
		out.WriteString("\n- " + question.Header + ": " + strings.Join(parts, "; "))
	}
	out.WriteString("\n\nContinue the request above with these answers.")
	return out.String()
}

// AnswerAgentQuestions records answers once. While the asking run still waits
// it picks them up itself; after it handed off, the response carries the turn
// that continues the conversation, claimed exactly once.
func (s *SpacesService) AnswerAgentQuestions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Answers []db.AgentQuestionAnswer `json:"answers"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		set, err := s.database.AnswerAgentQuestionSet(r.Context(), user, chi.URLParam(r, "questionID"), body.Answers)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if record, lookupErr := s.database.AIInvocationByID(r.Context(), user, set.RunID); lookupErr == nil && !aiInvocationTerminal(record.State) {
			s.showCollaborationEvent(r.Context(), record, aiInvocationEvent{Type: "question.answered", aiCollaborationEventFields: aiCollaborationEventFields{QuestionSet: set}})
		}
		response := map[string]any{"questionSet": set}
		if set.HandedOff {
			claimed, claimErr := s.database.ClaimAgentQuestionContinuation(r.Context(), user, set.ID)
			if claimErr != nil {
				writeSpaceError(w, claimErr)
				return
			}
			if claimed {
				response["continuation"] = map[string]any{"prompt": questionContinuationPrompt(set)}
			}
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// AgentPlanControl approves, revises or rejects a plan. Approval switches the
// conversation to Act and returns the turn that runs the plan.
func (s *SpacesService) AgentPlanControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "planID")
		current, err := s.database.AgentPlanByID(r.Context(), user, id)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		switch chi.URLParam(r, "action") {
		case "approve":
			var body struct {
				Version int `json:"version"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			plan, err := s.database.ApproveAgentPlan(r.Context(), user, id, body.Version)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if err := s.database.SetConversationMode(r.Context(), user, plan.ConversationID, db.ConversationModeAct); err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "mode": db.ConversationModeAct,
				"prompt": "Run the approved plan “" + plan.Payload.Title + "”."})
		case "revise":
			var body struct {
				Plan db.AgentPlanPayload `json:"plan"`
			}
			if decodeJSON(w, r, &body) != nil {
				return
			}
			if current.State != "proposed" {
				writeSpaceError(w, db.ErrSpaceConflict)
				return
			}
			latest, err := s.database.CurrentAgentPlan(r.Context(), user, current.ConversationID)
			if err != nil || latest == nil || latest.ID != current.ID {
				writeSpaceError(w, db.ErrSpaceConflict)
				return
			}
			plan, err := s.database.ProposeAgentPlan(r.Context(), user, current.ConversationID, "", "user", body.Plan)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"plan": plan})
		case "reject":
			if err := s.database.RejectAgentPlan(r.Context(), user, id); err != nil {
				writeSpaceError(w, err)
				return
			}
			plan, _ := s.database.AgentPlanByID(r.Context(), user, id)
			writeJSON(w, http.StatusOK, map[string]any{"plan": plan})
		default:
			writeSpaceError(w, db.ErrSpaceNotFound)
		}
	}
}

// ConversationGoal sets a new goal on a conversation and returns the turn that
// starts working toward it. Later turns continue on the server.
func (s *SpacesService) ConversationGoal() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Objective       string   `json:"objective"`
			SuccessCriteria []string `json:"successCriteria"`
			BudgetTokens    int64    `json:"budgetTokens"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		goal, err := s.database.SetAgentGoal(r.Context(), user, chi.URLParam(r, "conversationID"), body.Objective, body.SuccessCriteria, body.BudgetTokens)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"goal": goal, "prompt": "Start working toward the goal: " + goal.Objective})
	}
}

// AgentGoalControl pauses, resumes (optionally with a larger budget) or clears a goal.
func (s *SpacesService) AgentGoalControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.appsUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Status       string `json:"status"`
			BudgetTokens int64  `json:"budgetTokens"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		goal, err := s.database.ControlAgentGoal(r.Context(), user, chi.URLParam(r, "goalID"), strings.TrimSpace(body.Status), body.BudgetTokens)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		response := map[string]any{"goal": goal}
		if goal.Status == "pursuing" {
			if running, _ := s.database.RunningConversationInvocation(r.Context(), user, goal.ConversationID); running == "" {
				response["prompt"] = "Resume working toward the goal: " + goal.Objective
			}
		}
		writeJSON(w, http.StatusOK, response)
	}
}

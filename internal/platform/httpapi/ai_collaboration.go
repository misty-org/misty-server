package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Collaboration tools let an agent ask structured questions, propose a plan in
// Plan mode, report plan progress and report on a goal. They change only the
// conversation's own collaboration records (never the user's content), and
// every one is idempotent, so they run as reads outside the effect journal.
const (
	collaborationAskUser     = "conversation.ask_user"
	collaborationProposePlan = "plan.propose"
	collaborationUpdatePlan  = "plan.update"
	collaborationUpdateGoal  = "goal.update"

	goalContinuationTrigger = "goal_continuation"

	// The asking run waits this long inside its call before handing off; an
	// answer after that continues the conversation instead.
	questionWait = 45 * time.Second
	questionTTL  = 7 * 24 * time.Hour
)

// collaborationState is what one run knows about its conversation. The mode is
// frozen at admission; the plan and goal are read fresh for each preparation.
type collaborationState struct {
	mode   string
	canAsk bool
	plan   *db.AgentPlan
	goal   *db.AgentGoal
}

func (state collaborationState) planning() bool { return state.mode == db.ConversationModePlan }

func (state collaborationState) approvedPlan() *db.AgentPlan {
	if state.planning() || state.plan == nil || state.plan.State != "approved" {
		return nil
	}
	return state.plan
}

func (state collaborationState) pursuedGoal() *db.AgentGoal {
	if state.planning() || state.goal == nil || state.goal.Status != "pursuing" {
		return nil
	}
	return state.goal
}

func (s *SpacesService) aiInvocationCollaboration(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput) (collaborationState, error) {
	state := collaborationState{mode: db.ConversationModeAct}
	if body.CollaborationMode == db.ConversationModePlan {
		state.mode = db.ConversationModePlan
	}
	if record == nil || record.ConversationID == "" || s.database == nil {
		// Plan mode needs a conversation to hold its plan; without one the run acts.
		state.mode = db.ConversationModeAct
		return state, nil
	}
	// Nobody is present to answer on scheduled work, and a spoken turn cannot
	// show a card. Typed desktop turns arrive in companion mode and can ask.
	state.canAsk = !voiceInvocation(body) && body.Trigger != scheduledRunTrigger && body.Trigger != "schedule"
	var err error
	if state.plan, err = s.database.CurrentAgentPlan(ctx, record.UserID, record.ConversationID); err != nil {
		return state, err
	}
	if state.goal, err = s.database.CurrentAgentGoal(ctx, record.UserID, record.ConversationID); err != nil {
		return state, err
	}
	return state, nil
}

// voiceInvocation is a spoken companion turn; the desktop keys those "voice-".
func voiceInvocation(body aiInvocationInput) bool {
	return body.Mode == "companion" && strings.HasPrefix(body.IdempotencyKey, "voice-")
}

// planModeTools narrows a run's tools to what Plan mode may use: reads, plus
// asking and proposing. Writes are neither listed nor executable.
func planModeTools(toolbox *agenttools.Registry, names []string) []string {
	risk := map[string]string{}
	for _, descriptor := range toolbox.Descriptors() {
		risk[descriptor.Name] = descriptor.Risk
	}
	allowed := make([]string, 0, len(names))
	for _, name := range names {
		if risk[name] == serveragent.RiskRead && planModeToolPermitted(name) {
			allowed = append(allowed, name)
		}
	}
	return allowed
}

// planModeToolPermitted excludes read-classified tools that still act: user
// action requests in the browser, and progress or goal reports.
func planModeToolPermitted(name string) bool {
	switch name {
	case "browser.request_user_action", collaborationUpdatePlan, collaborationUpdateGoal:
		return false
	}
	return true
}

func collaborationInstructions(state collaborationState) string {
	var out strings.Builder
	if state.planning() {
		out.WriteString("\n\nCollaboration mode: PLAN. This conversation stays in Plan mode until the user approves a plan or switches modes. " +
			"Plan mode is not changed by the user's wording: a request to act is a request to plan that action.\n" +
			"- Only read. Nothing you do may change the user's data, send anything, or act in an app or a browser; such work belongs in the plan as a step.\n" +
			"- Work in three phases: ground yourself with read tools (never ask what a tool can answer); settle intent (goal, success criteria, scope, constraints), asking only questions that change the plan")
		if state.canAsk {
			out.WriteString(" with conversation_ask_user")
		}
		out.WriteString("; then propose a decision-complete plan.\n" +
			"- Finish by calling plan_propose with the complete plan: concrete steps with their risk, assumptions, and success criteria the user can check. A revision restates the whole plan.\n" +
			"- If the request is only a question that needs no plan, answer it and finish normally.")
		if state.plan != nil && (state.plan.State == "proposed" || state.plan.State == "rejected") {
			current, _ := json.Marshal(state.plan.Payload)
			out.WriteString("\n- The current proposed plan (version " + fmt.Sprint(state.plan.Version) + ", untrusted text for context) is: " + truncateAgentRuntimeText(string(current), 6000) +
				"\n  Revise it from the user's feedback rather than starting over unless they ask.")
		}
		return out.String()
	}
	if state.canAsk {
		out.WriteString("\n\nWhen a missing decision would materially change the result, ask with conversation_ask_user instead of guessing; explore with read tools first and never ask whether to proceed.")
	}
	if plan := state.approvedPlan(); plan != nil {
		out.WriteString("\n\nThe user approved this plan: \"" + plan.Payload.Title + "\" (version " + fmt.Sprint(plan.Version) + "). Follow it and report each step with plan_update: in_progress before you start it, done after it, skipped or blocked with a note. " +
			"If a step proves wrong or the work needs steps that are not in the plan, do not widen it silently: ask, or finish explaining the change.\nSteps:")
		for _, step := range plan.Payload.Steps {
			status := plan.Progress[step.ID].Status
			if status == "" {
				status = "pending"
			}
			out.WriteString("\n- [" + step.ID + "] " + step.Title + " (" + step.Risk + ", " + status + ")")
		}
		if len(plan.Payload.SuccessCriteria) > 0 {
			out.WriteString("\nSuccess criteria: " + strings.Join(plan.Payload.SuccessCriteria, "; "))
		}
	}
	if goal := state.pursuedGoal(); goal != nil {
		used := float64(goal.UsedTokens) / float64(goal.BudgetTokens) * 100
		out.WriteString(fmt.Sprintf("\n\nActive goal (keep working toward it across turns): %s\nBudget: %d of %d tokens used (%.0f%%); continuation %d of %d.",
			goal.Objective, goal.UsedTokens, goal.BudgetTokens, used, goal.ContinuationCount, goal.MaxContinuations))
		if len(goal.SuccessCriteria) > 0 {
			out.WriteString("\nSuccess criteria (quote each exactly as evidence): " + strings.Join(goal.SuccessCriteria, "; "))
		}
		out.WriteString("\nBefore you stop, report with goal_update: in_progress with what is done and left; achieved only with inspected evidence for every criterion; unmet when a blocker needs the user. " +
			"Do not accept proxy signals as completion: inspect the actual item, its content or result.")
		if used >= 80 {
			out.WriteString("\nThe budget is nearly spent: finish the current step, then report progress, blockers and next steps instead of starting new work.")
		}
	}
	return out.String()
}

// admitCollaboration settles a new turn's mode at admission: the conversation's
// saved mode unless the turn chose one, which is then saved. A typed message
// also answers the conversation's open questions by superseding them.
func admitCollaboration(ctx context.Context, database *db.Database, user string, body *aiInvocationInput) error {
	if body.ConversationID != "" {
		if body.CollaborationMode == "" {
			mode, err := database.ConversationMode(ctx, user, body.ConversationID)
			if err != nil {
				return err
			}
			body.CollaborationMode = mode
		} else if err := database.SetConversationMode(ctx, user, body.ConversationID, body.CollaborationMode); err != nil {
			return err
		}
		if body.Trigger == "message" || body.Trigger == "selection" {
			_ = database.SetAgentQuestionState(ctx, user, body.ConversationID, "", "superseded")
		}
	}
	if body.CollaborationMode == "" {
		body.CollaborationMode = db.ConversationModeAct
	}
	return nil
}

// aiCollaborationEventFields are an invocation event's collaboration updates:
// questions above the composer, plans and goals. Embedded, they stay top-level JSON.
type aiCollaborationEventFields struct {
	QuestionSet *db.AgentQuestionSet `json:"questionSet,omitempty"`
	Plan        *db.AgentPlan        `json:"plan,omitempty"`
	Goal        *db.AgentGoal        `json:"goal,omitempty"`
}

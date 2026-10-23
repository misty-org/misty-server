package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

type goalEvidence = []struct {
	Criterion string `json:"criterion"`
	Proof     string `json:"proof"`
}

func TestGoalEvidenceComplete(t *testing.T) {
	criteria := []string{"The review Note exists", "It lists every open task"}
	full := goalEvidence{{Criterion: "the review note  exists", Proof: "Note n_1 titled Weekly review"}, {Criterion: "It lists every open task", Proof: "12 of 12 tasks listed"}}
	if !goalEvidenceComplete(criteria, full) {
		t.Fatal("complete evidence rejected")
	}
	if goalEvidenceComplete(criteria, full[:1]) {
		t.Fatal("missing criterion accepted")
	}
	if goalEvidenceComplete(criteria, goalEvidence{{Criterion: criteria[0], Proof: " "}, full[1]}) {
		t.Fatal("empty proof accepted")
	}
	if goalEvidenceComplete(nil, nil) || !goalEvidenceComplete(nil, full[:1]) {
		t.Fatal("a goal without criteria needs at least one piece of evidence")
	}
}

func TestPlanModeToolsAllowOnlyReads(t *testing.T) {
	handler := func(context.Context, agenttools.Invocation, agent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	tool := func(name, risk string) agenttools.Registration {
		descriptor := agenttools.Descriptor{Layer: agenttools.LayerPublic, Name: name, Version: 1, Description: name, Risk: risk, Locality: agenttools.LocalityServer, InputSchema: agentToolSchema(map[string]any{}, nil)}
		if risk != agent.RiskRead {
			descriptor.AuditEvent = "test.write"
		}
		return agenttools.Registration{Descriptor: descriptor, Handler: handler}
	}
	toolbox := agenttools.MustNew(
		tool("note.read", agent.RiskRead), tool("note.create", agent.RiskWrite),
		tool("browser.inspect", agent.RiskRead), tool("browser.act", agent.RiskWrite),
		tool("browser.request_user_action", agent.RiskRead), tool("agent.background", agent.RiskWrite),
		tool(collaborationAskUser, agent.RiskRead), tool(collaborationProposePlan, agent.RiskRead),
		tool(collaborationUpdatePlan, agent.RiskRead), tool("folder.delete", agent.RiskDangerous),
	)
	names := []string{}
	for _, descriptor := range toolbox.Descriptors() {
		names = append(names, descriptor.Name)
	}
	got := strings.Join(planModeTools(toolbox, names), ",")
	want := "note.read,browser.inspect," + collaborationAskUser + "," + collaborationProposePlan
	if got != want {
		t.Fatalf("plan tools = %s, want %s", got, want)
	}
	if len(planModeTools(toolbox, []string{"unknown.tool"})) != 0 {
		t.Fatal("an unknown tool passed the plan filter")
	}
}

func TestCollaborationInstructions(t *testing.T) {
	planning := collaborationInstructions(collaborationState{mode: db.ConversationModePlan, canAsk: true})
	for _, want := range []string{"Collaboration mode: PLAN", "plan_propose", "conversation_ask_user", "a request to act is a request to plan"} {
		if !strings.Contains(planning, want) {
			t.Errorf("plan instructions lack %q", want)
		}
	}
	if strings.Contains(collaborationInstructions(collaborationState{mode: db.ConversationModePlan}), "conversation_ask_user") {
		t.Error("plan instructions offer questions where nobody can answer")
	}
	approved := &db.AgentPlan{Version: 2, State: "approved", Payload: db.AgentPlanPayload{Title: "Review", Steps: []db.AgentPlanStep{{ID: "s1", Title: "Read", Risk: "read"}}}, Progress: map[string]db.AgentPlanStepProgress{"s1": {Status: "done"}}}
	acting := collaborationInstructions(collaborationState{mode: db.ConversationModeAct, plan: approved})
	if !strings.Contains(acting, "log_work_checklist") || !strings.Contains(acting, "[s1] Read (read, done)") {
		t.Errorf("act instructions = %s", acting)
	}
	goal := &db.AgentGoal{Status: "pursuing", Objective: "Inbox zero", BudgetTokens: 100, UsedTokens: 85, MaxContinuations: 20, SuccessCriteria: []string{"Inbox is empty"}}
	pursuing := collaborationInstructions(collaborationState{mode: db.ConversationModeAct, goal: goal})
	if !strings.Contains(pursuing, "goal_update") || !strings.Contains(pursuing, "nearly spent") || !strings.Contains(pursuing, "Inbox is empty") {
		t.Errorf("goal instructions = %s", pursuing)
	}
	// Plan mode hides plan progress and goal reports: neither applies while planning.
	if strings.Contains(collaborationInstructions(collaborationState{mode: db.ConversationModePlan, plan: approved, goal: goal}), "goal_update") {
		t.Error("plan mode asked for goal reports")
	}
}

func TestVoiceInvocationAndContinuationPrompt(t *testing.T) {
	if !voiceInvocation(aiInvocationInput{Mode: "companion", IdempotencyKey: "voice-1"}) || voiceInvocation(aiInvocationInput{Mode: "companion", IdempotencyKey: "global-answer-1"}) {
		t.Fatal("typed desktop turns must be able to ask; spoken ones cannot")
	}
	prompt := questionContinuationPrompt(&db.AgentQuestionSet{
		Questions: []db.AgentQuestion{{Header: "Scope"}, {Header: "Format"}},
		Answers:   []db.AgentQuestionAnswer{{Selected: []string{"This week"}}, {Selected: []string{"Risks"}, Other: "and blockers"}},
	})
	if !strings.Contains(prompt, "- Scope: This week") || !strings.Contains(prompt, "- Format: Risks; and blockers") {
		t.Fatalf("prompt = %s", prompt)
	}
}

func TestCollaborationEventsStayTopLevel(t *testing.T) {
	encoded, err := json.Marshal(aiInvocationEvent{Type: "plan.proposed", aiCollaborationEventFields: aiCollaborationEventFields{Plan: &db.AgentPlan{ID: "plan_1"}}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(encoded, &decoded) != nil || decoded["plan"] == nil || decoded["aiCollaborationEventFields"] != nil || decoded["questionSet"] != nil {
		t.Fatalf("event = %s", encoded)
	}
}

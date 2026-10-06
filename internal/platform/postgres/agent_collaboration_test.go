package db

import (
	"strings"
	"testing"
)

func validQuestions() []AgentQuestion {
	return []AgentQuestion{{
		Header: "Scope", Question: "Which notes should the summary cover?",
		Options: []AgentQuestionOption{{Label: "This week (Recommended)"}, {Label: "This month", Description: "Longer, slower"}},
	}, {
		Header: "Format", Question: "Which sections do you want?", MultiSelect: true,
		Options: []AgentQuestionOption{{Label: "Decisions"}, {Label: "Risks"}, {Label: "Next steps"}},
	}}
}

func TestNormalizeAgentQuestions(t *testing.T) {
	questions := validQuestions()
	questions[0].Header = "  Scope  "
	normalized, err := NormalizeAgentQuestions(questions)
	if err != nil || normalized[0].Header != "Scope" {
		t.Fatalf("normalized = %+v, %v", normalized, err)
	}
	cases := map[string]func([]AgentQuestion) []AgentQuestion{
		"no questions":   func([]AgentQuestion) []AgentQuestion { return nil },
		"five questions": func(q []AgentQuestion) []AgentQuestion { return append(q, q[0], q[0], q[0]) },
		"one option": func(q []AgentQuestion) []AgentQuestion {
			q[0].Options = q[0].Options[:1]
			return q
		},
		"five options": func(q []AgentQuestion) []AgentQuestion {
			q[1].Options = append(q[1].Options, AgentQuestionOption{Label: "A"}, AgentQuestionOption{Label: "B"})
			return q
		},
		"duplicate option": func(q []AgentQuestion) []AgentQuestion {
			q[0].Options[1].Label = "this week (recommended)"
			return q
		},
		"model-made Other": func(q []AgentQuestion) []AgentQuestion {
			q[0].Options[1].Label = "Other"
			return q
		},
		"empty header": func(q []AgentQuestion) []AgentQuestion {
			q[0].Header = " "
			return q
		},
		"long question": func(q []AgentQuestion) []AgentQuestion {
			q[0].Question = strings.Repeat("x", 501)
			return q
		},
	}
	for name, mutate := range cases {
		if _, err := NormalizeAgentQuestions(mutate(validQuestions())); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidateAgentQuestionAnswers(t *testing.T) {
	questions := validQuestions()
	answers, err := ValidateAgentQuestionAnswers(questions, []AgentQuestionAnswer{
		{Selected: []string{"This month"}},
		{Selected: []string{"Risks", "Decisions"}, Other: "  and blockers  "},
	})
	if err != nil || answers[1].Other != "and blockers" || len(answers[1].Selected) != 2 {
		t.Fatalf("answers = %+v, %v", answers, err)
	}
	if answers, err := ValidateAgentQuestionAnswers(questions, []AgentQuestionAnswer{{Other: "Only Tuesday"}, {Selected: []string{"Risks"}}}); err != nil || answers[0].Other != "Only Tuesday" {
		t.Fatalf("free-text answer = %+v, %v", answers, err)
	}
	bad := map[string][]AgentQuestionAnswer{
		"missing answer":            {{Selected: []string{"This month"}}},
		"unknown option":            {{Selected: []string{"Next year"}}, {Selected: []string{"Risks"}}},
		"two on single-select":      {{Selected: []string{"This month", "This week (Recommended)"}}, {Selected: []string{"Risks"}}},
		"duplicate selection":       {{Selected: []string{"This month"}}, {Selected: []string{"Risks", "Risks"}}},
		"nothing chosen or written": {{Selected: []string{}}, {Selected: []string{"Risks"}}},
		"other too long":            {{Other: strings.Repeat("x", 1001)}, {Selected: []string{"Risks"}}},
	}
	for name, input := range bad {
		if _, err := ValidateAgentQuestionAnswers(questions, input); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNormalizeAgentPlan(t *testing.T) {
	plan, err := NormalizeAgentPlan(AgentPlanPayload{
		Title: " Weekly review ", Summary: "Collect and write.",
		Steps: []AgentPlanStep{{Title: "Read this week's notes"}, {ID: "write", Title: "Write the review Note", Risk: "write"}},
		Assumptions: []string{" ", "Notes are in Journal"}, SuccessCriteria: []string{"A review Note exists"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != "Weekly review" || plan.Steps[0].ID != "step-1" || plan.Steps[0].Risk != "read" || plan.Steps[1].ID != "write" || len(plan.Assumptions) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	steps := make([]AgentPlanStep, MaxAgentPlanSteps+1)
	for i := range steps {
		steps[i] = AgentPlanStep{Title: "Step"}
	}
	bad := map[string]AgentPlanPayload{
		"no title":      {Steps: []AgentPlanStep{{Title: "A"}}},
		"no steps":      {Title: "Plan"},
		"too many":      {Title: "Plan", Steps: steps},
		"unknown risk":  {Title: "Plan", Steps: []AgentPlanStep{{Title: "A", Risk: "harmless"}}},
		"duplicate id":  {Title: "Plan", Steps: []AgentPlanStep{{ID: "a", Title: "A"}, {ID: "a", Title: "B"}}},
		"untitled step": {Title: "Plan", Steps: []AgentPlanStep{{Title: " "}}},
	}
	for name, input := range bad {
		if _, err := NormalizeAgentPlan(input); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

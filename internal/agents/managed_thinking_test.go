package agent

import "testing"

func TestHouseRouteIgnoresClientChoices(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if ClientModelChoice() {
		t.Fatal("production must never let a request choose, even with the switch set")
	}
	if got := HouseModel("anthropic/other"); got != FrontierDefaultModelID() {
		t.Fatalf("requested model leaked through: %s", got)
	}
	for _, c := range []struct{ mode, legacy string }{{"deep", "xhigh"}, {"normal", "high"}, {"", "max"}, {"", ""}} {
		if got := ManagedReasoning(c.mode, c.legacy); got != HouseReasoning {
			t.Fatalf("%+v: %s", c, got)
		}
	}
}

func TestDevelopmentSwitchAllowsEvalOverrides(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "")
	if ClientModelChoice() || HouseModel("anthropic/other") != FrontierDefaultModelID() {
		t.Fatal("development without the switch must still use the house model")
	}
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if !ClientModelChoice() {
		t.Fatal("the development switch did not open model choice")
	}
	if got := HouseModel(" anthropic/claude-haiku-5.5 "); got != "anthropic/claude-haiku-5.5" {
		t.Fatalf("model: %q", got)
	}
	if HouseModel("") != FrontierDefaultModelID() {
		t.Fatal("an empty request must fall back to the house model")
	}
	if HouseEffort("medium") != "medium" || HouseEffort("bogus") != HouseReasoning || HouseEffort("") != HouseReasoning {
		t.Fatal("effort choice not limited to valid levels in development")
	}
}

func TestRuntimeReasoningStillMapsEveryLevel(t *testing.T) {
	if runtimeReasoning("xhigh") != "xhigh" || runtimeReasoning("max") != "xhigh" || runtimeReasoning("low") != "low" {
		t.Fatal("a reasoning level was dropped from the model request")
	}
}

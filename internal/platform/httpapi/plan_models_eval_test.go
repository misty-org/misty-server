package api

import "testing"

func TestEvalTurnLimitOnlyInDevelopmentWithModelChoice(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if got := evalTurnLimit(90, 20); got != 20 {
		t.Fatalf("production honored an eval turn limit: %d", got)
	}
	t.Setenv("MISTY_ENVIRONMENT", "development")
	if got := evalTurnLimit(90, 20); got != 90 {
		t.Fatalf("development eval limit = %d", got)
	}
	if got := evalTurnLimit(500, 20); got != 200 {
		t.Fatalf("eval limit not capped: %d", got)
	}
	if got := evalTurnLimit(0, 20); got != 20 {
		t.Fatalf("absent eval limit changed the plan's: %d", got)
	}
}

func TestEvalExecutionLimitOnlyForDevelopmentEvals(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if got := evalExecutionLimitMS(120); got != 0 {
		t.Fatalf("production raised the execution limit: %d", got)
	}
	t.Setenv("MISTY_ENVIRONMENT", "development")
	if got := evalExecutionLimitMS(120); got != 4*60*60*1000 {
		t.Fatalf("development eval execution limit = %d", got)
	}
	if got := evalExecutionLimitMS(0); got != 0 {
		t.Fatalf("a run that is not an eval got an execution limit: %d", got)
	}
}

package api

import "testing"

func TestEvalTurnLimitOnlyOnADevelopmentServer(t *testing.T) {
	for _, environment := range []string{"production", "", "staging"} {
		t.Setenv("MISTY_ENVIRONMENT", environment)
		if got := evalTurnLimit(90); got != 0 {
			t.Fatalf("environment %q honored an eval turn limit: %d", environment, got)
		}
	}
	t.Setenv("MISTY_ENVIRONMENT", "development")
	if got := evalTurnLimit(90); got != 90 {
		t.Fatalf("development eval limit = %d", got)
	}
	if got := evalTurnLimit(500); got != maxEvalTurnLimit {
		t.Fatalf("eval limit not capped: %d", got)
	}
	if got := evalTurnLimit(0); got != 0 {
		t.Fatalf("a request that asked for nothing got a limit: %d", got)
	}
}

func TestEvalExecutionLimitOnlyOnADevelopmentServer(t *testing.T) {
	for _, environment := range []string{"production", "", "staging"} {
		t.Setenv("MISTY_ENVIRONMENT", environment)
		if got := evalExecutionLimitMS(120); got != 0 {
			t.Fatalf("environment %q raised the execution limit: %d", environment, got)
		}
	}
	t.Setenv("MISTY_ENVIRONMENT", "development")
	if got := evalExecutionLimitMS(120); got != 4*60*60*1000 {
		t.Fatalf("development eval execution limit = %d", got)
	}
	if got := evalExecutionLimitMS(0); got != 0 {
		t.Fatalf("a run that isn't an eval got an execution limit: %d", got)
	}
}

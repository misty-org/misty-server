package agent

import "testing"

func TestManagedThinkingPolicy(t *testing.T) {
	t.Setenv("MISTY_FRONTIER_DEFAULT_MODEL", "anthropic/legacy")
	if FrontierDefaultModelID() != "openai/gpt-6-astra" {
		t.Fatal("legacy override changed managed model")
	}
	for _, c := range []struct{ mode, legacy, want string }{{"normal", "xhigh", "high"}, {"deep", "low", "xhigh"}, {"", "low", "high"}, {"", "", "high"}, {"", "xhigh", "xhigh"}} {
		if got := ManagedReasoning(c.mode, c.legacy); got != c.want {
			t.Fatalf("%+v: %s", c, got)
		}
	}
	if normalizeReasoningEffort("xhigh") != "xhigh" {
		t.Fatal("deep thinking dropped from Responses request")
	}
}

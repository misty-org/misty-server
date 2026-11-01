package agent

import "testing"

func TestManagedReasoningIgnoresClientChoices(t *testing.T) {
	for _, c := range []struct{ mode, legacy string }{{"deep", "xhigh"}, {"normal", "high"}, {"", "max"}, {"", ""}} {
		if got := ManagedReasoning(c.mode, c.legacy); got != HouseReasoning {
			t.Fatalf("%+v: %s", c, got)
		}
	}
}

package api

import (
	"encoding/json"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
)

func TestScreenGoalsThatNameNothingConsequentialRunWithoutTheFlag(t *testing.T) {
	for goal, keeps := range map[string]bool{
		`Enter 55500 into the salary input field, then click Calculate to see the take-home pay`:                                 false,
		`Fill in the "to" date field with 2022-12-31 in the Release Dates filter section, then click Search to apply the filter`: false,
		`Add the cheapest charger to the cart and check out`:                                                                     true,
		`Send the drafted reply to the landlord`:                                                                                 true,
		`Delete the old playlist`:                                                                                                true,
	} {
		raw, _ := json.Marshal(map[string]any{"goal": goal, "allowConsequential": true})
		out := withoutNeedlessConsequence(agent.ToolRequest{Name: screenActTool, Arguments: raw})
		var args map[string]any
		_ = json.Unmarshal(out.Arguments, &args)
		if got := args["allowConsequential"] == true; got != keeps {
			t.Fatalf("%q: allowConsequential kept = %v, want %v", goal, got, keeps)
		}
	}
}

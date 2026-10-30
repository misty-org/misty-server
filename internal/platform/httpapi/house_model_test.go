package api

import (
	"context"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/aimodels"
)

func TestInvocationModelIsTheHouseModelForEveryone(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	body := aiInvocationInput{ModelID: "anthropic/other", ReasoningEffort: "xhigh", ThinkingMode: "deep", Mode: "companion", CompanionModel: "google/other"}
	model, reasoning := aiInvocationModel(body)
	if model != agent.FrontierDefaultModelID() || reasoning != agent.HouseReasoning {
		t.Fatalf("client choice leaked into the run: %s %s", model, reasoning)
	}
}

func TestInvocationModelHonorsEvalSwitchInDevelopment(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "development")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	model, reasoning := aiInvocationModel(aiInvocationInput{ModelID: "anthropic/claude-haiku-5.5", ReasoningEffort: "low"})
	if model != "anthropic/claude-haiku-5.5" || reasoning != "low" {
		t.Fatalf("eval override ignored: %s %s", model, reasoning)
	}
}

func TestPlansShareOneRoute(t *testing.T) {
	defaults := []aimodels.Route{{Role: "agent", Model: agent.FrontierDefaultModelID(), Enabled: true}}
	planned, forced := runRoutes(context.Background(), nil, "user", defaults)
	if len(forced) != 0 || planned[0].Model != agent.FrontierDefaultModelID() {
		t.Fatalf("a plan changed the model: %+v %+v", planned, forced)
	}
}

package agent

import (
	"context"
	"testing"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func TestInstanceModelCatalogWithoutGateway(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "")
	t.Setenv("AI_GATEWAY_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("MISTY_AGENT_MODEL_PROVIDER", "anthropic")
	t.Setenv("MISTY_AGENT_MODEL", "anthropic/operator-model")
	t.Setenv("MISTY_AGENT_MODEL_API_KEY", "fixture")
	t.Setenv("MISTY_AGENT_MODEL_BASE_URL", "")
	if FrontierDefaultModelID() != "anthropic/operator-model" {
		t.Fatal("operator model was not selected")
	}
	models, err := FrontierGatewayModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "anthropic/operator-model" {
		t.Fatalf("catalog: %+v %v", models, err)
	}
	if !FrontierModelAvailable(context.Background(), models[0].ID) || !FrontierModelReasoningAvailable(context.Background(), models[0].ID, "high") {
		t.Fatal("configured model rejected")
	}
	if FrontierModelAvailable(context.Background(), DefaultFrontierModelID) {
		t.Fatal("unconfigured model admitted")
	}
}

func TestInstanceModelConfigRejectsInvalidRouting(t *testing.T) {
	for _, c := range []struct {
		provider, model, key, base string
		valid                      bool
	}{
		{"gateway", "", "", "", true},
		{"openai", "openai/example", "fixture", "", true},
		{"google", "google/example", "fixture", "", true},
		{"openai-compatible", "openai-compatible/local/model", "", "http://localhost:11434/v1", true},
		{"openai", "openai/example", "", "", false},
		{"anthropic", "openai/example", "fixture", "", false},
		{"openai-compatible", "openai-compatible/local", "", "", false},
		{"openai", "openai/example", "fixture", "https://user:secret@example.com", false},
		{"unknown", "unknown/example", "fixture", "", false},
	} {
		t.Run(c.provider+"/"+c.model+"/"+c.base, func(t *testing.T) {
			t.Setenv("MISTY_AGENT_MODEL_PROVIDER", c.provider)
			t.Setenv("MISTY_AGENT_MODEL", c.model)
			t.Setenv("MISTY_AGENT_MODEL_API_KEY", c.key)
			t.Setenv("MISTY_AGENT_MODEL_BASE_URL", c.base)
			_, err := envconfig.AgentModel()
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v: %v", c.valid, err)
			}
		})
	}
}

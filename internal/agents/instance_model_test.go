package agent

import (
	"testing"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func TestInstanceModelPinsAGatewayModel(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL", "anthropic/operator-model")
	if FrontierDefaultModelID() != "anthropic/operator-model" {
		t.Fatal("operator model was not selected")
	}
	t.Setenv("MISTY_AGENT_MODEL", "")
	if FrontierDefaultModelID() != DefaultFrontierModelID {
		t.Fatal("release default was not used")
	}
}

func TestInstanceModelConfigRejectsInvalidModel(t *testing.T) {
	for _, c := range []struct {
		model string
		valid bool
	}{
		{"", true},
		{"openai/example", true},
		{"example", false},
		{"openai/", false},
		{"openai/has space", false},
	} {
		t.Run(c.model, func(t *testing.T) {
			t.Setenv("MISTY_AGENT_MODEL", c.model)
			_, err := envconfig.AgentModel()
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v: %v", c.valid, err)
			}
		})
	}
}

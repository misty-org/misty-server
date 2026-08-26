package agent

import (
	"testing"

	agent "github.com/kannachi323/misty/server/internal/agents"
)

func TestFrontierCapabilitiesRequireVisionAndTools(t *testing.T) {
	if !agent.TestingFrontierCapabilities([]string{"vision", "tool-use", "reasoning"}) {
		t.Fatal("multimodal tool-capable model should be eligible")
	}
	if agent.TestingFrontierCapabilities([]string{"vision", "reasoning"}) {
		t.Fatal("vision-only model must not enter the Misty frontier catalog")
	}
	if agent.TestingFrontierCapabilities([]string{"tools", "reasoning"}) {
		t.Fatal("text-only tool model must not enter the Misty frontier catalog")
	}
}

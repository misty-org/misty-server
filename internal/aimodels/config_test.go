package aimodels

import "testing"

func TestOpenAIModelsGoDirectOnlyWithAnInstanceKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if DirectOpenAI("openai/gpt-x") || UsageProvider("openai/gpt-x") != "ai-gateway" {
		t.Fatal("OpenAI skipped the Gateway without a key")
	}
	t.Setenv("OPENAI_API_KEY", "fixture")
	if !DirectOpenAI("openai/gpt-x") || UsageProvider("openai/gpt-x") != "openai" {
		t.Fatal("OpenAI model did not use the instance key")
	}
	if DirectOpenAI("anthropic/claude-x") {
		t.Fatal("a non-OpenAI model skipped the Gateway")
	}
}

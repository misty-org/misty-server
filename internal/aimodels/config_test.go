package aimodels

import "testing"

func TestOpenAIModelsGoDirectOnlyWithAnInstanceKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if DirectOpenAI("openai/gpt-x") {
		t.Fatal("OpenAI skipped the Gateway without a key")
	}
	t.Setenv("OPENAI_API_KEY", "fixture")
	if !DirectOpenAI("openai/gpt-x") {
		t.Fatal("OpenAI model did not use the instance key")
	}
	if DirectOpenAI("anthropic/claude-x") {
		t.Fatal("a non-OpenAI model skipped the Gateway")
	}
}

func TestOperatorModelsNeedTheOperatorsModelOrAnEvalStack(t *testing.T) {
	t.Setenv("MISTY_AGENT_MODEL", "")
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_ALLOW_MODEL_OVERRIDE", "1")
	if OperatorModels() {
		t.Fatal("production without MISTY_AGENT_MODEL has no models of its own")
	}
	t.Setenv("MISTY_ENVIRONMENT", "development")
	if !OperatorModels() {
		t.Fatal("a development eval stack runs on its own keys")
	}
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_AGENT_MODEL", "anthropic/claude-sonnet-5-5")
	if !OperatorModels() {
		t.Fatal("MISTY_AGENT_MODEL gives the server its own models")
	}
}

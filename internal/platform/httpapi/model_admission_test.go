package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/byok"
)

func admissionCredentials(user string) context.Context {
	c := &byok.Credentials{Version: 1, Reasoning: "high",
		Providers: map[string]byok.Credential{"anthropic": {Provider: "anthropic", APIKey: "sk-test"}},
		Models:    byok.Models{Agent: "anthropic/claude-sonnet-5-5", Transcription: "soniox/stt-async-v4"}}
	byok.Default.Put(user, c)
	return context.Background()
}

func TestModelAdmissionPrefersTheAccountThenTheOperatorThenRefuses(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_AGENT_MODEL", "")
	ctx := admissionCredentials("byok-account")
	t.Cleanup(func() { byok.Default.Forget("byok-account") })

	// The account's own model and key, with its reasoning on reasoning roles.
	resolved, err := resolveRoleModel(ctx, nil, "byok-account", "agent")
	if err != nil || resolved == nil || resolved.Model != "anthropic/claude-sonnet-5-5" || resolved.Credential == nil || resolved.Credential.APIKey != "sk-test" || resolved.Reasoning != "high" {
		t.Fatalf("account agent = %+v, %v", resolved, err)
	}
	// The fast role falls back to the agent model.
	if resolved, err = resolveRoleModel(ctx, nil, "byok-account", "routing"); err != nil || resolved.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("account fast = %+v, %v", resolved, err)
	}
	// Embeddings are off without an embedding model.
	if _, err = resolveRoleModel(ctx, nil, "byok-account", "embedding"); !errors.Is(err, aimodels.ErrDisabled) {
		t.Fatalf("no embedding model = %v", err)
	}
	// A chosen model without its provider's key needs a key.
	if _, err = resolveRoleModel(ctx, nil, "byok-account", "transcription"); !errors.Is(err, byok.ErrModelKeyNeeded) {
		t.Fatalf("transcription without a Soniox key = %v", err)
	}
	// No account keys and no operator model: 409 model_key_needed.
	if _, err = resolveRoleModel(ctx, nil, "nobody", "agent"); !errors.Is(err, byok.ErrModelKeyNeeded) {
		t.Fatalf("no keys anywhere = %v", err)
	}
	recorder := httptest.NewRecorder()
	if !writeModelError(recorder, byok.ErrModelKeyNeeded) || recorder.Code != 409 || !strings.Contains(recorder.Body.String(), `"code":"model_key_needed"`) {
		t.Fatalf("key needed response = %d %s", recorder.Code, recorder.Body)
	}
	// The operator's model runs accounts that brought none.
	t.Setenv("MISTY_AGENT_MODEL", "openai/gpt-6-astra")
	if resolved, err = resolveRoleModel(ctx, nil, "nobody", "agent"); err != nil || resolved != nil {
		t.Fatalf("operator default = %+v, %v", resolved, err)
	}
	// An account's routes replace the run's other roles, never its thinking model.
	routes := accountRunRoutes(ctx, "byok-account", defaultAIRoutes("openai/eval-pick", "low"))
	if modelRoute(routes, "agent").Model != "openai/eval-pick" || modelRoute(routes, "routing").Model != "anthropic/claude-sonnet-5-5" || modelRoute(routes, "transcription").Model != "soniox/stt-async-v4" {
		t.Fatalf("account run routes = %+v", routes)
	}
}

package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"

	"golang.org/x/oauth2"
)

func TestGeminiProviderADCPrefersTokenOverAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer adc-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "" {
			t.Fatalf("X-Goog-Api-Key = %q", got)
		}
		writeJSONResponse(t, w, map[string]any{
			"output_text": `{"text":"Ready.","tool_requests":[],"file_plan":null}`,
		})
	}))
	defer server.Close()

	provider := NewGeminiProvider(GeminiProviderConfig{
		APIKey:   "api-key",
		AuthMode: "adc",
		BaseURL:  server.URL,
		Model:    "gemini-test",
		TokenSource: staticTokenSource{token: &oauth2.Token{
			AccessToken: "adc-token",
			TokenType:   "Bearer",
		}},
		Client: server.Client(),
	})
	if _, err := provider.Next(ModelRequest{
		SessionID: "s",
		UserID:    "u",
		Mode:      ModeAuto,
		Messages:  []Message{{Role: "user", Content: "Organize this"}},
	}); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
}

func TestNewProviderFromEnvSelectsConfiguredProvider(t *testing.T) {
	t.Setenv("MISTY_AI_PROVIDER", "gemini")
	t.Setenv("GEMINI_API_KEY", "key")
	t.Setenv("MISTY_AI_MODEL", "gemini-test")

	if _, ok := NewProviderFromEnv().(*ADKGeminiProvider); !ok {
		t.Fatalf("NewProviderFromEnv() did not select ADK Gemini provider")
	}

	t.Setenv("MISTY_AI_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "key")
	if _, ok := NewProviderFromEnv().(*OpenAIProvider); !ok {
		t.Fatalf("NewProviderFromEnv() did not select OpenAI provider")
	}
}

func TestNewAgentProviderFromEnvUsesPerTierRoutes(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "gateway-key")
	t.Setenv("OPENAI_API_KEY", "openai-key-that-must-not-be-used")
	t.Setenv("MISTY_AI_LOW_MODEL", "google/gemini-low")
	t.Setenv("MISTY_AI_MED_MODEL", "anthropic/claude-med")
	t.Setenv("MISTY_AI_HIGH_MODEL", "openai/gpt-high")

	router, ok := NewAgentProviderFromEnv().(*AgentProviderRouter)
	if !ok {
		t.Fatalf("NewAgentProviderFromEnv() did not return a agent router")
	}
	if provider, model := TestingProviderStatus(router.ProviderForTier(TierLow)); provider != ProviderVercelAI || model != "google/gemini-low" {
		t.Fatalf("low route = %s/%s", provider, model)
	}
	if provider, model := TestingProviderStatus(router.ProviderForTier(TierMed)); provider != ProviderVercelAI || model != "anthropic/claude-med" {
		t.Fatalf("med route = %s/%s", provider, model)
	}
	if provider, model := TestingProviderStatus(router.ProviderForTier(TierHigh)); provider != ProviderVercelAI || model != "openai/gpt-high" {
		t.Fatalf("high route = %s/%s", provider, model)
	}
	lowProvider, ok := router.ProviderForTier(TierLow).(*OpenAIProvider)
	if !ok || lowProvider.TestingApiKey != "gateway-key" || lowProvider.TestingBaseURL != TestingDefaultVercelAIBaseURL {
		t.Fatalf("low gateway provider = %#v", lowProvider)
	}
}

func TestNewAgentProviderFromEnvUsesGatewayDefaultsAndRequiresGatewayAuth(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "gateway-key")
	router := NewAgentProviderFromEnv().(*AgentProviderRouter)
	if _, model := TestingProviderStatus(router.ProviderForTier(TierLow)); model != TestingDefaultAgentLowGatewayModel {
		t.Fatalf("low default model = %q", model)
	}
	if _, model := TestingProviderStatus(router.ProviderForTier(TierMed)); model != TestingDefaultAgentMedGatewayModel {
		t.Fatalf("med default model = %q", model)
	}
	if _, model := TestingProviderStatus(router.ProviderForTier(TierHigh)); model != TestingDefaultAgentHighGatewayModel {
		t.Fatalf("high default model = %q", model)
	}

	t.Setenv("AI_GATEWAY_API_KEY", "")
	t.Setenv("VERCEL_OIDC_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "direct-provider-key")
	router = NewAgentProviderFromEnv().(*AgentProviderRouter)
	if provider, _ := TestingProviderStatus(router.ProviderForTier(TierHigh)); provider != ProviderMock {
		t.Fatalf("agent bypassed gateway with provider %q", provider)
	}
}

func TestParseFirstProviderJSONResponseUsesConcatenatedCandidate(t *testing.T) {
	response, err := TestingParseFirstProviderJSONResponse([]string{
		`{"text":"I will inspect`,
		`{"text":"I will inspect the folder.","tool_requests":[],"file_plan":null}`,
	})
	if err != nil {
		t.Fatalf("parseFirstProviderJSONResponse() error = %v", err)
	}
	if response.Text != "I will inspect the folder." {
		t.Fatalf("Text = %q", response.Text)
	}
}

func TestParseFirstProviderJSONResponseExtractsWrappedJSON(t *testing.T) {
	response, err := TestingParseFirstProviderJSONResponse([]string{
		`Here is the plan: {"text":"Plan ready.","tool_requests":[],"file_plan":null}`,
	})
	if err != nil {
		t.Fatalf("parseFirstProviderJSONResponse() error = %v", err)
	}
	if response.Text != "Plan ready." {
		t.Fatalf("Text = %q", response.Text)
	}
}

func TestGroundedAgentCitationsRejectInventedFilesAndLocations(t *testing.T) {
	request := ModelRequest{ToolResults: []ToolResult{{
		Name: ToolPreviewFile, OK: true,
		Result: json.RawMessage(`{"scopeId":"scope_12345678","fileName":"report.pdf","relativePath":"reports/report.pdf","sections":[{"kind":"page","locator":"1"},{"kind":"page","locator":"2"}]}`),
	}, {
		Name: ToolPreviewFile, OK: true,
		Result: json.RawMessage(`{"scopeId":"scope_12345678","fileName":"totals.xlsx","relativePath":"reports/totals.xlsx","sections":[{"kind":"sheet","locator":"Summary!A1:D12"}]}`),
	}}}
	citations := []AgentCitation{
		{ID: "ok", ScopeID: "scope_12345678", FileName: "report.pdf", RelativePath: "reports/report.pdf", Kind: "pdf_page", Label: "Page 2", Page: 2},
		{ID: "sheet", ScopeID: "scope_12345678", FileName: "totals.xlsx", RelativePath: "reports/totals.xlsx", Kind: "sheet_range", Label: "Summary totals", Sheet: "Summary", Range: "A1:D12"},
		{ID: "wrong-page", ScopeID: "scope_12345678", FileName: "report.pdf", RelativePath: "reports/report.pdf", Kind: "pdf_page", Label: "Page 9", Page: 9},
		{ID: "wrong-file", ScopeID: "scope_12345678", FileName: "secret.pdf", RelativePath: "secret.pdf", Kind: "pdf_page", Label: "Page 1", Page: 1},
	}
	grounded := TestingGroundedAgentCitations(request, citations)
	if len(grounded) != 2 || grounded[0].ID != "ok" || grounded[1].ID != "sheet" {
		t.Fatalf("grounded citations = %#v", grounded)
	}
}

func TestNewProviderFromEnvSelectsGeminiRESTFallback(t *testing.T) {
	t.Setenv("MISTY_AI_PROVIDER", "gemini_rest")
	t.Setenv("GEMINI_API_KEY", "key")
	t.Setenv("MISTY_AI_MODEL", "gemini-test")

	if provider, ok := NewProviderFromEnv().(*GeminiProvider); !ok {
		t.Fatalf("NewProviderFromEnv() = %T, want *GeminiProvider", provider)
	}
}

func TestNewProviderFromEnvAcceptsGoogleAPIKeyForADKGemini(t *testing.T) {
	t.Setenv("MISTY_AI_PROVIDER", "gemini")
	t.Setenv("GEMINI_AUTH_MODE", "api_key")
	t.Setenv("GOOGLE_API_KEY", "key")
	t.Setenv("MISTY_AI_MODEL", "gemini-test")

	if provider, ok := NewProviderFromEnv().(*ADKGeminiProvider); !ok {
		t.Fatalf("NewProviderFromEnv() = %T, want *ADKGeminiProvider", provider)
	}
}

func TestProviderUsageParsing(t *testing.T) {
	openAI := TestingExtractOpenAIUsage([]byte(`{"usage":{"input_tokens":120,"output_tokens":40,"input_tokens_details":{"cached_tokens":20},"output_tokens_details":{"reasoning_tokens":10}}}`))
	if openAI.InputTokens != 120 || openAI.CachedInputTokens != 20 || openAI.OutputTokens != 40 || openAI.ReasoningTokens != 10 {
		t.Fatalf("OpenAI usage = %#v", openAI)
	}
	gemini := TestingExtractGeminiUsage([]byte(`{"usageMetadata":{"promptTokenCount":90,"cachedContentTokenCount":10,"candidatesTokenCount":30,"thoughtsTokenCount":5}}`))
	if gemini.InputTokens != 90 || gemini.CachedInputTokens != 10 || gemini.OutputTokens != 35 || gemini.ReasoningTokens != 5 {
		t.Fatalf("Gemini usage = %#v", gemini)
	}
	interaction := TestingExtractGeminiUsage([]byte(`{"usage":{"total_input_tokens":100,"total_cached_tokens":20,"total_output_tokens":40,"total_thought_tokens":8}}`))
	if interaction.InputTokens != 100 || interaction.CachedInputTokens != 20 || interaction.OutputTokens != 48 || interaction.ReasoningTokens != 8 {
		t.Fatalf("Gemini interaction usage = %#v", interaction)
	}
}

func TestNewProviderFromEnvSelectsGeminiForADC(t *testing.T) {
	t.Setenv("MISTY_AI_PROVIDER", "gemini")
	t.Setenv("GEMINI_AUTH_MODE", "adc")

	if _, ok := NewProviderFromEnv().(*ADKGeminiProvider); !ok {
		t.Fatalf("NewProviderFromEnv() did not select ADK Gemini provider")
	}
}

type staticTokenSource struct {
	token *oauth2.Token
	err   error
}

func (s staticTokenSource) Token() (*oauth2.Token, error) {
	return s.token, s.err
}

func writeJSONResponse(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("response JSON error = %v", err)
	}
}

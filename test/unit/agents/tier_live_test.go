package agent

import (
	"encoding/json"
	"strings"
	"testing"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	. "github.com/kannachi323/misty/server/internal/agents"
)

// This opt-in test spends a small amount of gateway credit. It validates the
// real Vercel route, Misty's full JSON schema, usage parsing, tool requests, and
// the tool-result-to-file-plan continuation used by the desktop client.
func TestAgentGatewayLiveCapabilities(t *testing.T) {
	if envconfig.Getenv("MISTY_RUN_LIVE_AI_TEST") != "1" {
		t.Skip("set MISTY_RUN_LIVE_AI_TEST=1 to exercise Vercel AI Gateway")
	}
	provider := TestingResolveAgentProvider(NewAgentProviderFromEnv(), TierLow)
	if name, _ := TestingProviderStatus(provider); name != ProviderVercelAI {
		t.Fatalf("agent gateway is not configured; provider = %q", name)
	}

	request := ModelRequest{
		SessionID:  "agent-live-smoke",
		UserID:     "agent-live-smoke",
		AgentTier:  TierLow,
		Mode:       ModeAuto,
		ActiveRoot: "Documents",
		Messages: []Message{{
			Role:    "user",
			Content: "Inspect this folder with list_directory, then organize the loose invoice into an Invoices folder.",
		}},
		Capabilities: ToolManifest{Tools: []ToolDefinition{{Name: ToolListDirectory, Risk: RiskRead}}},
	}
	first, err := provider.Next(request)
	if err != nil {
		if isLiveGatewayRateLimit(err) {
			t.Skipf("gateway account rate-limited tier Low: %v", err)
		}
		t.Fatal(err)
	}
	if first.Usage.InputTokens <= 0 || first.Usage.OutputTokens <= 0 {
		t.Fatalf("gateway did not return token usage: %#v", first.Usage)
	}
	if len(first.ToolRequests) == 0 || first.ToolRequests[0].Name != ToolListDirectory {
		t.Fatalf("expected list_directory request, got %#v", first)
	}

	listing, err := json.Marshal(map[string]any{
		"path": "Documents",
		"entries": []map[string]any{{
			"name": "invoice.pdf", "path": "invoice.pdf", "kind": "file", "extension": "pdf",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request.ToolResults = []ToolResult{{
		RequestID: first.ToolRequests[0].ID,
		Name:      ToolListDirectory,
		OK:        true,
		Result:    listing,
	}}
	request.KnownPaths = []string{"invoice.pdf"}
	second, err := provider.Next(request)
	if err != nil {
		if isLiveGatewayRateLimit(err) {
			t.Skipf("gateway account rate-limited tier Low continuation: %v", err)
		}
		t.Fatal(err)
	}
	if second.Usage.InputTokens <= 0 || second.Usage.OutputTokens <= 0 {
		t.Fatalf("continuation did not return token usage: %#v", second.Usage)
	}
	if second.FilePlan == nil || len(second.FilePlan.Operations) == 0 {
		t.Fatalf("expected a file plan after tool results, got %#v", second)
	}
	if problems := ValidateFilePlan(*second.FilePlan, PlanValidationContext{KnownPaths: request.KnownPaths}); len(problems) > 0 {
		t.Fatalf("live file plan failed validation: %v; plan=%#v", problems, second.FilePlan)
	}

	for _, tier := range []AgentTier{TierMed, TierHigh} {
		t.Run(string(tier), func(t *testing.T) {
			tierProvider := TestingResolveAgentProvider(NewAgentProviderFromEnv(), tier)
			response, err := tierProvider.Next(ModelRequest{
				SessionID: "agent-live-" + string(tier),
				UserID:    "agent-live-smoke",
				AgentTier: tier,
				Mode:      ModeAsk,
				Messages: []Message{{
					Role:    "user",
					Content: "Reply with a short confirmation. Do not request tools or plan file operations.",
				}},
			})
			if err != nil {
				if isLiveGatewayRateLimit(err) {
					t.Skipf("gateway account rate-limited this tier: %v", err)
				}
				t.Fatal(err)
			}
			if response.Text == "" || response.Usage.InputTokens <= 0 || response.Usage.OutputTokens <= 0 {
				t.Fatalf("incomplete tier response: %#v", response)
			}
		})
	}

	completionService := NewService(nil, NewAgentProviderFromEnv())
	text, _, err := completionService.CompleteWithTier(
		"agent-live-smoke", "Summarize this in one sentence: Misty organized the files successfully.", "automation_ai", TierLow,
	)
	if err != nil {
		if isLiveGatewayRateLimit(err) {
			t.Skipf("gateway account rate-limited automation completion: %v", err)
		}
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("automation completion returned empty text")
	}
}

func isLiveGatewayRateLimit(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "status 429") || strings.Contains(message, "rate_limit_exceeded")
}

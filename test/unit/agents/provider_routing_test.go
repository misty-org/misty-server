package agent

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func testModelRuntime() *modelruntime.Client {
	return modelruntime.New("https://runtime.test", []byte(strings.Repeat("s", 32)), nil)
}

func TestNewAgentProviderUsesPerTierRoutes(t *testing.T) {
	t.Setenv("MISTY_AI_LOW_MODEL", "google/gemini-low")
	t.Setenv("MISTY_AI_MED_MODEL", "anthropic/claude-med")
	t.Setenv("MISTY_AI_HIGH_MODEL", "openai/gpt-high")

	router, ok := NewAgentProvider(testModelRuntime()).(*AgentProviderRouter)
	if !ok {
		t.Fatalf("NewAgentProvider() did not return an agent router")
	}
	for tier, want := range map[AgentTier]string{TierLow: "google/gemini-low", TierMed: "anthropic/claude-med", TierHigh: "openai/gpt-high"} {
		if provider, model := TestingProviderStatus(router.ProviderForTier(tier)); provider != ProviderVercelAI || model != want {
			t.Fatalf("%s route = %s/%s", tier, provider, model)
		}
		if _, ok := router.ProviderForTier(tier).(*RuntimeProvider); !ok {
			t.Fatalf("%s does not call models through the runtime", tier)
		}
	}
}

func TestNewAgentProviderUsesGatewayDefaultsAndNeedsTheRuntime(t *testing.T) {
	router := NewAgentProvider(testModelRuntime()).(*AgentProviderRouter)
	for tier, want := range map[AgentTier]string{TierLow: TestingDefaultAgentLowGatewayModel, TierMed: TestingDefaultAgentMedGatewayModel, TierHigh: TestingDefaultAgentHighGatewayModel} {
		if _, model := TestingProviderStatus(router.ProviderForTier(tier)); model != want {
			t.Fatalf("%s default model = %q", tier, model)
		}
	}
	router = NewAgentProvider(&modelruntime.Client{}).(*AgentProviderRouter)
	if provider, _ := TestingProviderStatus(router.ProviderForTier(TierHigh)); provider != ProviderMock {
		t.Fatalf("agent called a model without the runtime: %q", provider)
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

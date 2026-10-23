package agent

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func testModelRuntime() *modelruntime.Client {
	return modelruntime.New("https://runtime.test", []byte(strings.Repeat("s", 32)), nil)
}

func TestNewAgentProviderUsesTheFastModelThroughTheRuntime(t *testing.T) {
	t.Setenv("MISTY_AI_LOW_MODEL", "google/gemini-low")
	provider := NewAgentProvider(testModelRuntime())
	if name, model := ProviderStatus(provider); name != ProviderVercelAI || model != "google/gemini-low" {
		t.Fatalf("route = %s/%s", name, model)
	}
	if _, ok := provider.(*RuntimeProvider); !ok {
		t.Fatal("the agent does not call models through the runtime")
	}
}

func TestNewAgentProviderUsesGatewayDefaultsAndNeedsTheRuntime(t *testing.T) {
	if _, model := ProviderStatus(NewAgentProvider(testModelRuntime())); model != DefaultAgentLowGatewayModel {
		t.Fatalf("default model = %q", model)
	}
	if provider, _ := ProviderStatus(NewAgentProvider(&modelruntime.Client{})); provider != ProviderMock {
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
	grounded := GroundedAgentCitations(request, citations)
	if len(grounded) != 2 || grounded[0].ID != "ok" || grounded[1].ID != "sheet" {
		t.Fatalf("grounded citations = %#v", grounded)
	}
}

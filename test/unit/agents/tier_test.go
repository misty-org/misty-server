package agent

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

type namedTestProvider struct {
	provider string
	model    string
	text     string
	err      error
	tools    []ToolRequest
}

func (provider namedTestProvider) ProviderName() string { return provider.provider }
func (provider namedTestProvider) ModelName() string    { return provider.model }
func (provider namedTestProvider) Next(ModelRequest) (ModelResponse, error) {
	return ModelResponse{Text: provider.text, ToolRequests: provider.tools, Usage: ModelUsage{InputTokens: 10, OutputTokens: 5}}, provider.err
}

type recordingUsageMeter struct {
	userID   string
	spaceID  string
	key      string
	provider string
	model    string
	releases int
	refunds  int
}

func (meter *recordingUsageMeter) ReserveForSpace(userID, spaceID, key string, _ string, provider, model string, _, _ int64) (*UsageReservation, error) {
	meter.spaceID = spaceID
	return meter.Reserve(userID, key, "", provider, model, 0, 0)
}

func TestSpaceContentCompletionBillsOnlyRequestingAccount(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		manifest ToolManifest
		execute  ToolExecutor
	}{
		{name: "tool session", manifest: ToolManifest{Tools: []ToolDefinition{{Name: "tasks.query", Risk: RiskRead}}}, execute: func(context.Context, ToolRequest) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }},
		{name: "plain fallback"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			meter := &recordingUsageMeter{}
			service := NewService(nil, namedTestProvider{provider: "gateway", model: "model", text: "done"}, WithUsageMeter(meter))
			if _, err := service.CompleteWithToolsForSpaceContext(context.Background(), "member", "space-owner", "space-123", "identity", "prompt", TierLow, testCase.manifest, testCase.execute); err != nil {
				t.Fatal(err)
			}
			if meter.userID != "member" || meter.spaceID != "" {
				t.Fatalf("usage scope = user %q space %q", meter.userID, meter.spaceID)
			}
		})
	}
}

func (meter *recordingUsageMeter) Reserve(userID string, key string, _ string, provider, model string, _, _ int64) (*UsageReservation, error) {
	meter.userID = userID
	meter.key = key
	meter.provider = provider
	meter.model = model
	return &UsageReservation{ID: "reservation", ReservedCredits: 10}, nil
}

func (*recordingUsageMeter) Settle(*UsageReservation, string, string, string, string, ModelUsage) (UsageSettlement, error) {
	return UsageSettlement{ChargedMicrousd: 2, CreditsUsed: 2, CreditsRemaining: 98}, nil
}

func (meter *recordingUsageMeter) Release(*UsageReservation) error {
	meter.releases++
	return nil
}

func (meter *recordingUsageMeter) Refund(*UsageReservation, string, string) (UsageSettlement, error) {
	meter.refunds++
	return UsageSettlement{CreditsRemaining: 100}, nil
}

func TestNormalizeAgentTierDefaultsToLow(t *testing.T) {
	if got := NormalizeAgentTier("unknown"); got != TierLow {
		t.Fatalf("NormalizeAgentTier() = %q", got)
	}
}

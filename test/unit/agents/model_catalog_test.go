package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestGatewayTokenRateUsesHostedAIRateCardUnits(t *testing.T) {
	rate, ok := TestingGatewayTokenRate(json.RawMessage(`"0.00000125"`))
	if !ok || rate != 1250 {
		t.Fatalf("gatewayTokenRate() = %d, %v; want 1250, true", rate, ok)
	}
	if rate, ok := TestingGatewayTokenRate(json.RawMessage(`"0"`)); !ok || rate != 0 {
		t.Fatalf("free-model pricing = %d, %v; want 0, true", rate, ok)
	}
}

func TestFilterChatModelsExcludesNonChatAndDuplicates(t *testing.T) {
	models := TestingFilterChatModels([]GatewayModel{
		{ID: "provider/chat", Name: "Chat"},
		{ID: "provider/chat", Name: "Duplicate"},
		{ID: "provider/text-embedding", Name: "Embedding"},
		{ID: "provider/image-model", Name: "Image"},
	})
	if len(models) != 1 || models[0].ID != "provider/chat" {
		t.Fatalf("filterChatModels() = %#v", models)
	}
}

func TestGatewayCapabilitiesAcceptsObjectMetadata(t *testing.T) {
	capabilities := TestingGatewayCapabilities(json.RawMessage(`{"tool_calling":true,"image_generation":false,"vision":true}`))
	if len(capabilities) != 2 || capabilities[0] != "tool_calling" || capabilities[1] != "vision" {
		t.Fatalf("gatewayCapabilities() = %#v", capabilities)
	}
}

func TestFetchGatewayModelsUsesPublicCatalogWithoutCredentials(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"provider/chat","name":"Chat","type":"language","tags":["reasoning","tool-use"],"pricing":{"input":"0.000001","output":"0.000002"}},
			{"id":"provider/free","name":"Free","type":"language","tags":[],"pricing":{"input":"0","output":"0"}},
			{"id":"provider/search","name":"Search","type":"language","tags":["web-search"],"pricing":{}},
			{"id":"provider/embed","name":"Embed","type":"embedding","tags":[],"pricing":{"input":"0.000001"}}
		]}`))
	}))
	defer server.Close()

	t.Setenv("AI_GATEWAY_BASE_URL", server.URL)
	t.Setenv("AI_GATEWAY_API_KEY", "")
	t.Setenv("VERCEL_OIDC_TOKEN", "")
	models, err := TestingFetchGatewayModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		t.Fatalf("public catalog Authorization = %q, want empty", authorization)
	}
	if len(models) != 3 {
		t.Fatalf("fetchGatewayModels() returned %d models, want 3", len(models))
	}
	if !models[0].HasTokenPricing || !models[1].HasTokenPricing || models[2].HasTokenPricing {
		t.Fatal("catalog models must distinguish token-priced, free, and fallback-priced models")
	}
	if !gatewayModelSupportsToolsFrom(models[0]) {
		t.Fatalf("tool-use tag was not normalized: %#v", models[0].Capabilities)
	}
}

func gatewayModelSupportsToolsFrom(model GatewayModel) bool {
	for _, capability := range model.Capabilities {
		switch capability {
		case "tools", "tool-use", "tool_calling", "function_calling":
			return true
		}
	}
	return false
}

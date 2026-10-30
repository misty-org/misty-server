package agent

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agent"
)

func TestFilterChatModelsExcludesNonChatAndDuplicates(t *testing.T) {
	models := FilterChatModels([]GatewayModel{
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
	capabilities := GatewayCapabilities(json.RawMessage(`{"tool_calling":true,"image_generation":false,"vision":true}`))
	if len(capabilities) != 2 || capabilities[0] != "tool_calling" || capabilities[1] != "vision" {
		t.Fatalf("gatewayCapabilities() = %#v", capabilities)
	}
}

func TestModelsAreAcceptedWithoutACatalog(t *testing.T) {
	t.Setenv("MISTY_AI_MODEL_CATALOG_JSON", `[{"id":"provider/chat","name":"Chat","capabilities":["tool-use"],"context_window":400000}]`)
	for _, id := range []string{"anthropic/claude-sonnet-5-5", "openrouter/anthropic/claude-sonnet-5.5", "ollama/qwen3:14b"} {
		if !FrontierModelAvailable(context.Background(), id) {
			t.Fatalf("%s was refused", id)
		}
	}
	for _, id := range []string{"", "no-slash", "provider/", "/model", "has space/model"} {
		if FrontierModelAvailable(context.Background(), id) {
			t.Fatalf("%q was accepted", id)
		}
	}
	if ModelContextWindow(context.Background(), "provider/chat") != 400_000 || ModelContextWindow(context.Background(), "provider/other") != DefaultContextWindow {
		t.Fatal("context windows come from the operator's catalog, else the default")
	}
}

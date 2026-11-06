package agent

import (
	"context"
	"testing"

	. "github.com/misty-org/misty-server/internal/agent"
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

func TestContextWindowsComeFromTheOperatorsCatalog(t *testing.T) {
	t.Setenv("MISTY_AI_MODEL_CATALOG_JSON", `[{"id":"provider/chat","name":"Chat","capabilities":["tool-use"],"context_window":400000}]`)
	if ModelContextWindow(context.Background(), "provider/chat") != 400_000 || ModelContextWindow(context.Background(), "provider/other") != DefaultContextWindow {
		t.Fatal("context windows come from the operator's catalog, else the default")
	}
}

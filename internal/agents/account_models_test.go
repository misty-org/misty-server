package agent

import (
	"context"
	"net/http"
	"testing"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestAccountLibraryUsesTheChosenModelAndDisabledFallbackMakesNoCall(t *testing.T) {
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		return http.StatusOK, map[string]any{"text": `{"assets":[]}`, "object": map[string]any{"assets": []any{}}, "usage": map[string]int{"inputTokens": 3, "outputTokens": 2}}
	})
	analyzer := (&SmartLibraryAnalyzer{Models: runtime.Client, ModelResolver: func(ctx context.Context, user, role string) (*aimodels.Resolved, error) {
		if user != "owner" {
			t.Fatal("wrong account")
		}
		if role == "library-fallback" {
			return nil, aimodels.ErrDisabled
		}
		return &aimodels.Resolved{Model: "openai/gpt-6-luna", Reasoning: "low"}, nil
	}}).WithAIAccount("owner")
	analysis, err := analyzer.Analyze(t.Context(), []SmartLibraryAsset{{AssetID: "asset", AssetKind: "text", ExtractedText: "hello"}})
	calls := runtime.Calls()
	if err != nil || len(calls) != 1 || len(analysis.Failures) != 1 {
		t.Fatal("disabled fallback made an extra attempt", len(calls), err)
	}
	body := calls[0].Body
	route := body["route"].(map[string]any)
	if calls[0].Path != "/v1/models/text" || route["provider"] != "instance" || route["reasoning"] != "low" || body["model"] != "openai/gpt-6-luna" {
		t.Fatalf("account choice ignored: %v", body)
	}
	if _, ok := route["apiKey"]; ok {
		t.Fatal("a key traveled with the route")
	}
	if _, ok := body["reasoning"]; ok {
		t.Fatal("Gateway reasoning overrode the account's choice")
	}
	if body["schema"].(map[string]any)["name"] != "smart_library_analysis_v2" {
		t.Fatal("structured output schema missing")
	}
}

func TestAccountEmbeddingUsesSelectedModelAndImageDescription(t *testing.T) {
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		return http.StatusOK, map[string]any{"embeddings": [][]float64{make([]float64, SmartLibraryEmbeddingDims)}, "usage": map[string]int{"inputTokens": 3}}
	})
	analyzer := (&SmartLibraryAnalyzer{Models: runtime.Client, ModelResolver: func(ctx context.Context, user, role string) (*aimodels.Resolved, error) {
		if user != "owner" || role != "embedding" {
			t.Fatal("wrong embedding authority")
		}
		return &aimodels.Resolved{Model: "openai/text-embedding-3-small"}, nil
	}}).WithAIAccount("owner")
	embeddings, _, err := analyzer.EmbedAssets(t.Context(), []SmartLibraryAsset{{AssetID: "asset", AssetKind: "image", MimeType: "image/png", Bytes: []byte("fixture-image")}}, map[string]SmartLibraryMetadata{"asset": {Description: "A red bicycle"}})
	calls := runtime.Calls()
	if err != nil || len(calls) != 1 || len(embeddings) != 1 || embeddings[0].Model != "openai/text-embedding-3-small" {
		t.Fatal("selected embedding model was not recorded", err)
	}
	body := calls[0].Body
	if calls[0].Path != "/v1/models/embed" || body["model"] != "openai/text-embedding-3-small" || body["dimensions"] != float64(SmartLibraryEmbeddingDims) {
		t.Fatalf("embedding model or dimensions ignored: %v", body)
	}
	if _, ok := body["images"]; ok {
		t.Fatal("image bytes sent to a text embedding model")
	}
	if values := body["values"].([]any); len(values) != 1 {
		t.Fatal("wrong input")
	}
}

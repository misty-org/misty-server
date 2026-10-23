package agent

import (
	"context"
	"net/http"
	"testing"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestAccountEmbeddingUsesSelectedModel(t *testing.T) {
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		return http.StatusOK, map[string]any{"embeddings": [][]float64{make([]float64, SmartLibraryEmbeddingDims)}, "usage": map[string]int{"inputTokens": 3}}
	})
	analyzer := (&SmartLibraryAnalyzer{Models: runtime.Client, ModelResolver: func(ctx context.Context, user, role string) (*aimodels.Resolved, error) {
		if user != "owner" || role != "embedding" {
			t.Fatal("wrong embedding authority")
		}
		return &aimodels.Resolved{Model: "openai/text-embedding-3-small"}, nil
	}}).WithAIAccount("owner")
	embeddings, _, err := analyzer.Embed(t.Context(), []string{"A red bicycle"})
	calls := runtime.Calls()
	if err != nil || len(calls) != 1 || len(embeddings) != 1 {
		t.Fatal("selected embedding model was not used", err)
	}
	body := calls[0].Body
	if calls[0].Path != "/v1/models/embed" || body["model"] != "openai/text-embedding-3-small" || body["dimensions"] != float64(SmartLibraryEmbeddingDims) {
		t.Fatalf("embedding model or dimensions ignored: %v", body)
	}
	if values := body["values"].([]any); len(values) != 1 {
		t.Fatal("wrong input")
	}
}

package agent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	SmartLibraryEmbeddingModel = "google/gemini-embedding-2"
	SmartLibraryEmbeddingDims  = 768
)

// SmartLibraryAnalyzer holds the shared model services Go calls itself:
// embeddings, transcription and the realtime voice socket.
type SmartLibraryAnalyzer struct {
	ModelResolver  aimodels.Resolver
	account        string
	roleConfigs    map[string]*aimodels.Resolved
	realtimeConfig *aimodels.Resolved
	// Models makes every Library model call through the agent runtime.
	Models *modelruntime.Client
	// APIKey, BaseURL and Client open the realtime voice socket, the one model
	// connection Go makes itself.
	APIKey, BaseURL string
	EmbeddingModel  string
	Client          *http.Client
}

// Available reports whether Library model calls can run.
func (a *SmartLibraryAnalyzer) Available() bool { return a != nil && a.Models.Enabled() }

// EmbedQuery embeds a user query in the same multimodal space as indexed assets.
func (a *SmartLibraryAnalyzer) EmbedQuery(ctx context.Context, query string) ([]float64, ModelUsage, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 512 || utf8.RuneCountInString(query) > 256 {
		return nil, ModelUsage{}, errors.New("invalid semantic query")
	}
	vectors, usage, err := a.embedGateway(ctx, []string{"task: search result | query: " + query})
	if err != nil || len(vectors) != 1 {
		return nil, usage, err
	}
	return vectors[0], usage, nil
}

// gatewayBaseURL and realtimeBaseURL locate the realtime voice socket.
func (a *SmartLibraryAnalyzer) gatewayBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if base == "" {
		return "https://ai-gateway.vercel.sh/v1"
	}
	return base
}

func (a *SmartLibraryAnalyzer) realtimeBaseURL() string {
	base := a.gatewayBaseURL()
	if strings.HasSuffix(base, "/v1") {
		return strings.TrimSuffix(base, "/v1") + "/v4/ai"
	}
	return base
}

func (a *SmartLibraryAnalyzer) embeddingModel() string {
	if value := strings.TrimSpace(a.EmbeddingModel); value != "" {
		return value
	}
	if value := strings.TrimSpace(config.Getenv("SMART_LIBRARY_EMBEDDING_MODEL")); value != "" {
		return value
	}
	return SmartLibraryEmbeddingModel
}

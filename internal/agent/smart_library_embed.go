package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// Embed retains the old text API for callers while routing it through Gemini 2.
func (a *SmartLibraryAnalyzer) Embed(ctx context.Context, inputs []string) ([][]float64, ModelUsage, error) {
	values := make([]string, len(inputs))
	for i, value := range inputs {
		values[i] = "title: none | text: " + value
	}
	return a.embedGateway(ctx, values)
}

func (a *SmartLibraryAnalyzer) embedGateway(ctx context.Context, values []string) ([][]float64, ModelUsage, error) {
	if len(values) == 0 || len(values) > 100 {
		return nil, ModelUsage{}, errors.New("invalid embedding batch")
	}
	route, model, err := a.embeddingRoute(ctx)
	if err != nil {
		return nil, ModelUsage{}, err
	}
	result, err := a.Models.Embed(ctx, modelruntime.EmbedRequest{Route: route, Model: model, Values: values, Dimensions: SmartLibraryEmbeddingDims})
	if err != nil {
		return nil, ModelUsage{}, err
	}
	if len(result.Embeddings) != len(values) {
		return nil, ModelUsage{}, errors.New("embedding model returned an unexpected embedding count")
	}
	if err := validateEmbeddingVectors(result.Embeddings); err != nil {
		return nil, ModelUsage{}, err
	}
	return result.Embeddings, ModelUsage{InputTokens: result.Usage.InputTokens}, nil
}

func validateEmbeddingVectors(vectors [][]float64) error {
	for _, vector := range vectors {
		if len(vector) != SmartLibraryEmbeddingDims {
			return fmt.Errorf("embedding model returned %d dimensions", len(vector))
		}
	}
	return nil
}

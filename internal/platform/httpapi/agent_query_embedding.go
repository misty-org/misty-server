package api

import (
	"context"
	"strings"
)

// agentQueryEmbedding embeds the text an agent searches its memory and the
// person's Misty content with. It makes one embedding call on the account's
// embedding model (none: no vector); search the person types themselves stays
// lexical.
func (s *SpacesService) agentQueryEmbedding(ctx context.Context, userID, query string) ([]float64, bool) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if len([]rune(normalized)) < 3 || s.searchAnalyzer == nil || s.database == nil {
		return nil, false
	}
	model := configuredEmbeddingModel(ctx, s.searchAnalyzer, userID)
	if model == "" {
		return nil, false
	}
	analyzer := s.searchAnalyzer.WithAIAccount(userID)
	vector, _, err := analyzer.EmbedQuery(ctx, normalized)
	if err != nil || len(vector) != 768 {
		return nil, false
	}
	return vector, true
}

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// agentQueryEmbedding embeds the text an agent searches its memory and the
// person's Misty content with. It bills one embedding call; search the person
// types themselves stays lexical and free.
func (s *SpacesService) agentQueryEmbedding(ctx context.Context, userID, query string) ([]float64, bool) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if len([]rune(normalized)) < 3 || s.searchAnalyzer == nil || s.database == nil {
		return nil, false
	}
	model := configuredEmbeddingModel(ctx, s.searchAnalyzer, userID)
	if model == "" {
		return nil, false
	}
	digest := sha256.Sum256([]byte(userID + "\x00" + model + "\x00" + normalized))
	analyzer := s.searchAnalyzer.WithBilling(s.database.BillingService(), userID, "agent-query:"+hex.EncodeToString(digest[:]))
	vector, _, err := analyzer.EmbedQuery(ctx, normalized)
	if err != nil || len(vector) != 768 {
		return nil, false
	}
	return vector, true
}

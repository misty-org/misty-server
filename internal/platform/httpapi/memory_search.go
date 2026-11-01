package api

import (
	"context"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// memorySearchFunc ranks one person's records in one Space by meaning.
type memorySearchFunc func(ctx context.Context, user, space string, branches []string, query string, limit int) ([]db.MemoryHit, error)

// searchMemory ranks one Space's records by meaning, on the vector the desktop
// made for the query on the device's embedding model. Records still waiting for
// their vector are not found yet; there is no word search to fall back on,
// so without an embedding model memory search finds nothing.
func (s *SpacesService) searchMemory(ctx context.Context, user, space string, branches []string, query string, limit int) ([]db.MemoryHit, error) {
	embedding, ok := agentQueryEmbedding(ctx, query)
	if !ok {
		return []db.MemoryHit{}, nil
	}
	return s.database.SearchMemory(ctx, user, space, branches, embedding, queryEmbeddingModel(ctx), limit)
}

func memoryHitIDs(hits []db.MemoryHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.RecordID)
	}
	return ids
}

// eventsInHitOrder puts events back in the order search ranked them.
func eventsInHitOrder(events []db.MemoryEvent, ids []string) []db.MemoryEvent {
	byID := make(map[string]db.MemoryEvent, len(events))
	for _, event := range events {
		byID[event.ID] = event
	}
	ordered := make([]db.MemoryEvent, 0, len(events))
	for _, id := range ids {
		if event, ok := byID[id]; ok {
			ordered = append(ordered, event)
		}
	}
	return ordered
}

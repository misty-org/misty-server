package api

import (
	"net/http"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// Embeddings for search and memory are made on the person's desktop
// (docs/agents/runtime.md). The desktop claims its owner's
// chunks that wait for a vector, embeds them on the device's embedding model
// and completes them; a chunk whose text changed meanwhile, or whose lease
// another claim took, is left for the next claim.

const deviceEmbeddingDimensions = 768

type deviceEmbeddingChunk struct {
	Kind        string `json:"kind"`
	DocumentID  string `json:"document_id,omitempty"`
	Ordinal     int    `json:"ordinal,omitempty"`
	Branch      string `json:"branch,omitempty"`
	RecordID    string `json:"record_id,omitempty"`
	Chunk       int    `json:"chunk,omitempty"`
	SpaceID     string `json:"space_id,omitempty"`
	ContentHash string `json:"content_hash"`
	AttemptID   string `json:"attempt_id"`
	Text        string `json:"text,omitempty"`
}

func (c deviceEmbeddingChunk) retrieval(userID string) db.AIEmbeddingChunk {
	return db.AIEmbeddingChunk{AccountID: userID, AttemptID: c.AttemptID, DocumentID: c.DocumentID, Ordinal: c.Ordinal, ContentHash: c.ContentHash}
}

func (c deviceEmbeddingChunk) memory(userID string) db.MemoryChunk {
	return db.MemoryChunk{OwnerUserID: userID, SpaceID: c.SpaceID, Branch: c.Branch, RecordID: c.RecordID, Chunk: c.Chunk, ContentHash: c.ContentHash, AttemptID: c.AttemptID}
}

func (s *SpacesService) deviceEmbeddingUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := sessionUserID(r, s.database)
	if err != nil || userID == "" {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return "", false
	}
	if _, ok := requestDevice(r); !ok {
		http.Error(w, "device signature required", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

// ClaimDeviceEmbeddings hands a desktop up to 32 of its owner's chunks to embed.
func (s *SpacesService) ClaimDeviceEmbeddings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.deviceEmbeddingUser(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		chunks := []deviceEmbeddingChunk{}
		retrieval, err := s.database.PendingAIEmbeddingChunksFor(r.Context(), userID, 32)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		for _, item := range retrieval {
			chunks = append(chunks, deviceEmbeddingChunk{Kind: "retrieval", DocumentID: item.DocumentID, Ordinal: item.Ordinal, ContentHash: item.ContentHash, AttemptID: item.AttemptID, Text: item.Content})
		}
		if len(chunks) < 32 {
			memory, err := s.database.PendingMemoryChunksFor(r.Context(), userID, 32-len(chunks))
			if err != nil {
				writeAgentError(w, err)
				return
			}
			for _, item := range memory {
				chunks = append(chunks, deviceEmbeddingChunk{Kind: "memory", Branch: item.Branch, RecordID: item.RecordID, Chunk: item.Chunk, SpaceID: item.SpaceID, ContentHash: item.ContentHash, AttemptID: item.AttemptID, Text: item.Text})
			}
		}
		if len(chunks) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"chunks": chunks, "dimensions": deviceEmbeddingDimensions})
	}
}

// CompleteDeviceEmbeddings stores the vectors a desktop made, and backs off the
// chunks it could not embed.
func (s *SpacesService) CompleteDeviceEmbeddings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.deviceEmbeddingUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Model  string `json:"model"`
			Chunks []struct {
				deviceEmbeddingChunk
				Vector []float64 `json:"vector"`
			} `json:"chunks"`
			Failed []deviceEmbeddingChunk `json:"failed"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if decodeAIJSON(w, r, &body) != nil || len(body.Chunks)+len(body.Failed) > 64 || len(body.Model) > 200 || (len(body.Chunks) > 0 && strings.TrimSpace(body.Model) == "") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_embeddings"})
			return
		}
		ctx := r.Context()
		stored := 0
		for _, item := range body.Chunks {
			if len(item.Vector) != deviceEmbeddingDimensions {
				continue
			}
			var err error
			switch item.Kind {
			case "retrieval":
				err = s.database.CompleteAIEmbeddingChunk(ctx, item.retrieval(userID), item.Vector, body.Model)
			case "memory":
				err = s.database.CompleteMemoryChunk(ctx, item.memory(userID), item.Vector, body.Model)
			default:
				continue
			}
			if err != nil {
				writeAgentError(w, err)
				return
			}
			stored++
		}
		for _, item := range body.Failed {
			switch item.Kind {
			case "retrieval":
				_ = s.database.RetryAIEmbeddingChunk(ctx, item.retrieval(userID))
			case "memory":
				_ = s.database.RetryMemoryChunk(ctx, item.memory(userID))
			}
		}
		writeJSON(w, http.StatusOK, map[string]int{"stored": stored})
	}
}

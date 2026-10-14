package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type globalSearchHit struct {
	ID            string   `json:"id"`
	AccountID     string   `json:"accountId"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	Body          string   `json:"body"`
	Keywords      []string `json:"keywords"`
	Href          string   `json:"href"`
	SpaceID       string   `json:"spaceId,omitempty"`
	SpaceName     string   `json:"spaceName,omitempty"`
	UpdatedAt     string   `json:"updatedAt,omitempty"`
	Source        string   `json:"source"`
	CanonicalID   string   `json:"canonicalId"`
	Revision      string   `json:"revision,omitempty"`
	Score         float64  `json:"score"`
	LexicalScore  float64  `json:"lexicalScore"`
	SemanticScore float64  `json:"semanticScore"`
}

type globalSearchEmbeddingCacheEntry struct {
	vector    []float64
	expiresAt time.Time
}

type globalSearchEmbeddingFlight struct {
	done   chan struct{}
	vector []float64
}

// GlobalSearch returns only records that the current account can read. Each
// backing query performs its own Space/audience permission check so membership
// changes are respected on every request.
func (s *SpacesService) GlobalSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		requestID := uuid.NewString()
		if query == "" {
			writeJSON(w, http.StatusOK, map[string]any{"hits": []globalSearchHit{}, "request_id": requestID, "semantic_enrichment_used": false})
			return
		}
		limit := parseGlobalSearchLimit(r.URL.Query().Get("limit"))
		kindFilter := parseGlobalSearchKinds(r.URL.Query().Get("kinds"))
		spaceFilter := strings.TrimSpace(r.URL.Query().Get("space_id"))
		spaces, err := s.database.ListSpaces(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		lowerQuery := strings.ToLower(query)
		collector := newGlobalSearchCollector(userID, limit, kindFilter, spaceFilter)
		spaceNames := map[string]string{}
		for _, space := range spaces {
			spaceNames[space.ID] = space.Name
		}
		var embedding []float64
		semanticUsed := false
		if r.URL.Query().Get("assist") == "1" {
			if _, err := s.database.SpaceByID(r.Context(), userID, strings.TrimSpace(r.URL.Query().Get("space_id"))); err != nil {
				writeSpaceError(w, err)
				return
			}
			embedding, semanticUsed = s.globalSearchQueryEmbedding(r.Context(), userID, query)
		}
		indexed, indexErr := s.database.SearchAIRetrieval(r.Context(), userID, query, embedding, 100, "", configuredEmbeddingModel(r.Context(), s.searchAnalyzer, userID))
		if indexErr != nil {
			indexed = nil
			semanticUsed = false
		}
		collectIndexedHits(collector, indexed, spaceNames)

		for _, space := range spaces {
			if strings.Contains(strings.ToLower(space.Name), lowerQuery) {
				if !collector.add(globalSearchHit{
					ID: "space:" + space.ID, Kind: "space", Title: space.Name,
					Body: "Space", Keywords: []string{"space", space.Name}, Href: "/spaces/" + url.PathEscape(space.ID),
					SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(space.UpdatedAt),
				}) {
					break
				}
			}
			if collector.full() {
				break
			}
			if space.Permissions[db.PermissionTasksView] {
				s.collectSpaceTasks(r.Context(), collector, space, query)
			}
			if collector.full() {
				break
			}
			s.collectSpaceNotes(r.Context(), collector, space, lowerQuery)
			if collector.full() {
				break
			}
			s.collectSpaceDrawings(r.Context(), collector, space, lowerQuery)
			if collector.full() {
				break
			}
			s.collectSpaceRoadmaps(r.Context(), collector, space, lowerQuery)
			if collector.full() {
				break
			}
			s.collectSpaceCalendarEvents(r.Context(), collector, space, lowerQuery)
			if collector.full() {
				break
			}
			s.collectSpaceConversations(r.Context(), collector, space, lowerQuery)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hits":                     collector.hits,
			"request_id":               requestID,
			"semantic_enrichment_used": semanticUsed,
		})
	}
}

// parseGlobalSearchLimit reads the optional result limit, defaulting to 40 and
// accepting values from 1 to 100.
func parseGlobalSearchLimit(value string) int {
	if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 && parsed <= 100 {
		return parsed
	}
	return 40
}

func parseGlobalSearchKinds(value string) map[string]bool {
	allowed := map[string]bool{
		"space": true, "task": true, "note": true, "message": true, "conversation": true,
		"calendar": true, "roadmap": true, "drawing": true, "agent": true, "workflow": true,
	}
	result := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		kind := strings.ToLower(strings.TrimSpace(part))
		if allowed[kind] {
			result[kind] = true
		}
	}
	return result
}

func normalizedSearchScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (s *SpacesService) globalSearchQueryEmbedding(ctx context.Context, userID, query string) ([]float64, bool) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if len([]rune(normalized)) < 3 || s.searchAnalyzer == nil {
		return nil, false
	}
	model := configuredEmbeddingModel(ctx, s.searchAnalyzer, userID)
	if model == "" {
		return nil, false
	}
	key := userID + "\x00" + model + "\x00" + normalized
	now := time.Now()
	s.searchEmbeddingMu.Lock()
	if cached, ok := s.searchEmbeddings[key]; ok && cached.expiresAt.After(now) {
		vector := append([]float64(nil), cached.vector...)
		s.searchEmbeddingMu.Unlock()
		return vector, len(vector) == 768
	}
	if flight, ok := s.searchEmbeddingInflight[key]; ok {
		s.searchEmbeddingMu.Unlock()
		select {
		case <-flight.done:
			vector := append([]float64(nil), flight.vector...)
			return vector, len(vector) == 768
		case <-ctx.Done():
			return nil, false
		}
	}
	flight := &globalSearchEmbeddingFlight{done: make(chan struct{})}
	s.searchEmbeddingInflight[key] = flight
	s.searchEmbeddingMu.Unlock()

	digest := sha256.Sum256([]byte(key))
	operation, err := beginHostedSemanticQuery(ctx, s.database, s.searchAnalyzer, userID, "", "global-search-query:"+hex.EncodeToString(digest[:]), normalized)
	var vector []float64
	if err == nil && operation != nil && len(operation.Vector) == 768 {
		if settleErr := operation.Settle(s.database); settleErr == nil {
			vector = append([]float64(nil), operation.Vector...)
		} else {
			operation.Release(s.database)
		}
	}
	s.searchEmbeddingMu.Lock()
	if len(vector) == 768 {
		s.searchEmbeddings[key] = globalSearchEmbeddingCacheEntry{vector: vector, expiresAt: now.Add(2 * time.Minute)}
	}
	flight.vector = vector
	delete(s.searchEmbeddingInflight, key)
	close(flight.done)
	s.searchEmbeddingMu.Unlock()
	return append([]float64(nil), vector...), len(vector) == 768
}

func searchTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

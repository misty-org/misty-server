package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
		// Search is lexical: matching on words costs nothing, while embedding every
		// query would bill a model call per keystroke.
		semanticUsed := false
		indexed, indexErr := s.database.SearchAIRetrieval(r.Context(), userID, query, nil, 100, "", configuredEmbeddingModel(r.Context(), s.searchAnalyzer, userID))
		if indexErr != nil {
			indexed = nil
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
			s.collectSpaceNotes(r.Context(), collector, space, lowerQuery)
			if collector.full() {
				break
			}
			s.collectSpaceDrawings(r.Context(), collector, space, lowerQuery)
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
		"space": true, "note": true, "message": true, "conversation": true,
		"drawing": true, "agent": true, "workflow": true,
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

func searchTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

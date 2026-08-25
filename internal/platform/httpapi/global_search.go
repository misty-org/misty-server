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
		limit := 40
		if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed >= 1 && parsed <= 100 {
			limit = parsed
		}
		kindFilter := parseGlobalSearchKinds(r.URL.Query().Get("kinds"))
		spaceFilter := strings.TrimSpace(r.URL.Query().Get("space_id"))
		spaces, err := s.database.ListSpaces(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		lowerQuery := strings.ToLower(query)
		hits := make([]globalSearchHit, 0, limit)
		seenHits := map[string]bool{}
		appendHit := func(hit globalSearchHit) bool {
			if len(hits) >= limit {
				return false
			}
			if len(kindFilter) > 0 && !kindFilter[hit.Kind] {
				return true
			}
			if spaceFilter != "" && hit.SpaceID != spaceFilter {
				return true
			}
			if hit.CanonicalID == "" {
				hit.CanonicalID = hit.Kind + ":" + strings.TrimPrefix(hit.ID, hit.Kind+":")
			}
			if seenHits[hit.CanonicalID] {
				return true
			}
			seenHits[hit.CanonicalID] = true
			hit.AccountID = userID
			hit.Source = "server"
			hits = append(hits, hit)
			return len(hits) < limit
		}
		spaceNames := map[string]string{}
		for _, space := range spaces {
			spaceNames[space.ID] = space.Name
		}
		embedding, semanticUsed := s.globalSearchQueryEmbedding(r.Context(), userID, query)
		indexed, indexErr := s.database.SearchAIRetrieval(r.Context(), userID, query, embedding, 100)
		if indexErr != nil {
			indexed = nil
			semanticUsed = false
		}
		for _, item := range indexed {
			kind := item.SourceKind
			if kind == "provider" {
				kind = "message"
			}
			if kind != "note" && kind != "task" && kind != "roadmap" && kind != "calendar" && kind != "message" {
				continue
			}
			href := item.Href
			if kind == "note" && item.SpaceID != "" {
				href = "/spaces/" + url.PathEscape(item.SpaceID) + "/notes?note=" + url.QueryEscape(item.SourceID)
			}
			if !appendHit(globalSearchHit{
				ID: item.SourceKind + ":" + item.SourceID, Kind: kind, Title: item.Title,
				Body: aiExcerpt(item.Content), Keywords: []string{kind, spaceNames[item.SpaceID]}, Href: href,
				SpaceID: item.SpaceID, SpaceName: spaceNames[item.SpaceID], Source: "server",
				CanonicalID: item.SourceKind + ":" + item.SourceID, Revision: item.SourceRevision,
				Score: normalizedSearchScore(item.Score), LexicalScore: normalizedSearchScore(item.LexicalScore), SemanticScore: normalizedSearchScore(item.SemanticScore),
			}) {
				break
			}
		}

		for _, space := range spaces {
			if strings.Contains(strings.ToLower(space.Name), lowerQuery) {
				if !appendHit(globalSearchHit{
					ID: "space:" + space.ID, Kind: "space", Title: space.Name,
					Body: space.Kind, Keywords: []string{"space", space.Name}, Href: "/spaces/" + url.PathEscape(space.ID),
					SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(space.UpdatedAt),
				}) {
					break
				}
			}
			if len(hits) >= limit {
				break
			}

			if space.Permissions[db.PermissionTasksView] {
				tasks, taskErr := s.database.SpaceTasks(r.Context(), userID, space.ID, db.SpaceTaskQuery{
					Search: query, Sort: "updated", Limit: min(8, limit-len(hits)),
				})
				if taskErr == nil {
					for _, task := range tasks {
						if !appendHit(globalSearchHit{
							ID: "task:" + task.ID, Kind: "task", Title: task.Title, Body: task.Notes,
							Keywords: []string{"task", task.TaskKey, task.Status, task.Priority, space.Name},
							Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/tasks/board?task=" + url.QueryEscape(task.ID),
							SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(task.UpdatedAt),
						}) {
							break
						}
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			notes, noteErr := s.database.AccessibleSpaceNotes(r.Context(), userID, space.ID)
			if noteErr == nil {
				for _, note := range notes {
					if !strings.Contains(strings.ToLower(note.TitleProjection), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "note:" + note.ID, Kind: "note", Title: note.TitleProjection, Body: "Note in " + space.Name,
						Keywords: []string{"note", space.Name}, Href: "/spaces/" + url.PathEscape(space.ID) + "/notes?note=" + url.QueryEscape(note.ID),
						SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(note.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			drawings, drawingErr := s.database.AccessibleSpaceDrawings(r.Context(), userID, space.ID)
			if drawingErr == nil {
				for _, drawing := range drawings {
					if !strings.Contains(strings.ToLower(drawing.Title), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "drawing:" + drawing.ID, Kind: "drawing", Title: drawing.Title,
						Body: "Drawing in " + space.Name, Keywords: []string{"drawing", space.Name},
						Href:    "/spaces/" + url.PathEscape(space.ID) + "/drawings/" + url.PathEscape(drawing.ID),
						SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(drawing.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			roadmaps, roadmapErr := s.database.SpaceRoadmaps(r.Context(), userID, space.ID)
			if roadmapErr == nil {
				for _, roadmap := range roadmaps {
					if !strings.Contains(strings.ToLower(roadmap.Name+" "+roadmap.Description), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "roadmap:" + roadmap.ID, Kind: "roadmap", Title: roadmap.Name, Body: roadmap.Description,
						Keywords: []string{"roadmap", space.Name},
						Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/roadmaps/" + url.PathEscape(roadmap.ID),
						SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(roadmap.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			now := time.Now().UTC()
			events, calendarErr := s.database.SpaceCalendarEvents(r.Context(), userID, space.ID, now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0))
			if calendarErr == nil {
				for _, event := range events {
					if !strings.Contains(strings.ToLower(event.Title+" "+event.Description+" "+event.Location), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "calendar:" + event.ID, Kind: "calendar", Title: event.Title, Body: event.Description,
						Keywords: []string{"calendar", "event", event.Location, space.Name},
						Href:     "/spaces/" + url.PathEscape(space.ID) + "/planner/agenda/day?date=" + url.QueryEscape(event.StartsAt.Format("2006-01-02")),
						SpaceID:  space.ID, SpaceName: space.Name, UpdatedAt: searchTime(event.UpdatedAt),
					}) {
						break
					}
				}
			}
			if len(hits) >= limit {
				break
			}

			conversations, conversationErr := s.database.SpaceConversations(r.Context(), userID, space.ID)
			if conversationErr == nil {
				for _, conversation := range conversations {
					if !strings.Contains(strings.ToLower(conversation.Title), lowerQuery) {
						continue
					}
					if !appendHit(globalSearchHit{
						ID: "conversation:" + conversation.ID, Kind: "conversation", Title: conversation.Title,
						Body: "Conversation in " + space.Name, Keywords: []string{"conversation", "chat", space.Name},
						Href:    "/spaces/" + url.PathEscape(space.ID) + "/chat?conversation=" + url.QueryEscape(conversation.ID),
						SpaceID: space.ID, SpaceName: space.Name, UpdatedAt: searchTime(conversation.UpdatedAt),
					}) {
						break
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"hits":                     hits,
			"request_id":               requestID,
			"semantic_enrichment_used": semanticUsed,
		})
	}
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
	key := userID + "\x00" + normalized
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
	operation, err := beginHostedSemanticQuery(ctx, s.database, s.searchAnalyzer, userID, "global-search-query:"+hex.EncodeToString(digest[:]), normalized)
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

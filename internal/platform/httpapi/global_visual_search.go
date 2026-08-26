package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *SpacesService) GlobalVisualSearch() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if s.searchAnalyzer == nil || s.library == nil || s.library.TestingStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "visual_search_unavailable", "message": "Visual search is temporarily unavailable."})
			return
		}
		var body struct {
			AttachmentID string `json:"attachment_id"`
			Query        string `json:"query"`
			Limit        int    `json:"limit"`
		}
		if decodeAIJSON(w, r, &body) != nil || strings.TrimSpace(body.AttachmentID) == "" || len([]rune(body.Query)) > 256 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_visual_query"})
			return
		}
		attachment, err := s.database.AIConversationAttachment(r.Context(), userID, body.AttachmentID)
		if err != nil || attachment.Scope != "visual_query" || attachment.LifecycleState != "ready" || attachment.ExpiresAt == nil || attachment.ExpiresAt.Before(time.Now()) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "visual_query_not_found"})
			return
		}
		reader, _, err := s.library.TestingStore.Open(r.Context(), attachment.ModelObjectKey)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		bytes, readErr := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		_ = reader.Close()
		if readErr != nil || len(bytes) > 1<<20 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_visual_query"})
			return
		}
		vector, _, err := s.searchAnalyzer.EmbedVisualQuery(r.Context(), attachment.ModelMIMEType, bytes, body.Query)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "visual_embedding_failed", "message": "Misty could not understand that image yet."})
			return
		}
		limit := body.Limit
		if limit < 1 || limit > 100 {
			limit = 40
		}
		query := strings.TrimSpace(body.Query)
		if query == "" {
			query = "visual reference"
		}
		hits := make([]globalSearchHit, 0, limit)
		seen := map[string]bool{}
		appendHit := func(hit globalSearchHit) {
			if len(hits) >= limit || seen[hit.CanonicalID] {
				return
			}
			seen[hit.CanonicalID] = true
			hit.AccountID, hit.Source = userID, "server"
			hits = append(hits, hit)
		}
		if smartHits, searchErr := s.database.SearchSmartLibraryHybrid(userID, "", query, vector, limit); searchErr == nil {
			for _, item := range smartHits {
				title := item.Description
				if title == "" {
					title = item.AssetID
				}
				appendHit(globalSearchHit{ID: "smart:" + item.AssetID, Kind: "file", Title: title, Body: item.Description, Keywords: item.Tags, Href: "/files", CanonicalID: "smart:" + item.FolderID + ":" + item.AssetID, Score: item.Score, LexicalScore: item.LexicalScore, SemanticScore: item.SemanticScore})
			}
		}
		spaces, _ := s.database.ListSpaces(r.Context(), userID)
		for _, space := range spaces {
			if !space.Permissions["library.view"] || len(hits) >= limit {
				continue
			}
			items, searchErr := s.database.SearchSpaceLibraryIntelligence(r.Context(), userID, space.ID, query, vector, min(20, limit-len(hits)))
			if searchErr != nil {
				continue
			}
			for _, item := range items {
				appendHit(globalSearchHit{ID: "library:" + item.ID, Kind: "library", Title: item.DisplayName, Body: item.Caption, Keywords: item.Tags, Href: "/spaces/" + url.PathEscape(space.ID) + "/library?item=" + url.QueryEscape(item.ID), SpaceID: space.ID, SpaceName: space.Name, CanonicalID: "library:" + item.ID, Score: .8, SemanticScore: .8})
			}
		}
		if indexed, searchErr := s.database.SearchAIRetrieval(r.Context(), userID, query, vector, limit); searchErr == nil {
			for _, item := range indexed {
				kind := item.SourceKind
				if kind == "provider" {
					kind = "message"
				}
				appendHit(globalSearchHit{ID: item.SourceKind + ":" + item.SourceID, Kind: kind, Title: item.Title, Body: aiExcerpt(item.Content), Href: item.Href, SpaceID: item.SpaceID, CanonicalID: item.SourceKind + ":" + item.SourceID, Revision: item.SourceRevision, Score: normalizedSearchScore(item.Score), LexicalScore: normalizedSearchScore(item.LexicalScore), SemanticScore: normalizedSearchScore(item.SemanticScore)})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "request_id": "visual_" + attachment.ID, "semantic_enrichment_used": true})
	}
}

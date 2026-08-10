package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
)

func (s *SmartLibraryService) SetAssetTags() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		folderID, assetID := chi.URLParam(r, "folderID"), chi.URLParam(r, "assetID")
		var body struct {
			Tags []string `json:"tags"`
		}
		if decodeAIJSON(w, r, &body) != nil || !TestingValidOpaqueID(folderID, "slf_") || !TestingValidOpaqueID(assetID, "asset_") {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		tags, valid := normalizeEditableTags(body.Tags)
		if !valid {
			http.Error(w, "invalid tags", http.StatusBadRequest)
			return
		}
		result, err := s.database.SetSmartLibraryAssetTags(userID, folderID, assetID, tags)
		if !s.writeFolderError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"result": map[string]any{"assetId": result.AssetID, "status": result.Status, "assetKind": result.AssetKind, "mimeType": result.MimeType, "description": result.Description, "tags": result.Tags, "suggestedCollections": result.Collections, "metadata": result.Metadata, "confidence": result.Confidence, "failure": result.FailureCode}})
	}
}

func normalizeEditableTags(values []string) ([]string, bool) {
	if len(values) > 24 {
		return nil, false
	}
	seen := map[string]bool{}
	tags := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" || len(value) > 80 || utf8.RuneCountInString(value) > 40 {
			return nil, false
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			tags = append(tags, value)
		}
	}
	return tags, true
}

func (s *SmartLibraryService) Rescan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			ChangedImages   int `json:"changedImages"`
			NewImages       int `json:"newImages"`
			RequestedImages int `json:"requestedImages"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.ChangedImages < 0 || body.NewImages < 0 {
			http.Error(w, "invalid request", 400)
			return
		}
		folder, err := s.database.SetSmartLibraryEstimate(userID, chi.URLParam(r, "folderID"), min(body.RequestedImages, body.ChangedImages+body.NewImages, smartLibraryLimit))
		if !s.writeFolderError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"estimate": s.estimate(userID, folder)})
	}
}

func (s *SmartLibraryService) Search() http.HandlerFunc {
	return s.searchHandler(true)
}

func (s *SmartLibraryService) GlobalSearch() http.HandlerFunc {
	return s.searchHandler(false)
}

func (s *SmartLibraryService) searchHandler(folderFromPath bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Query    string `json:"query"`
			Limit    int    `json:"limit"`
			FolderID string `json:"folderId,omitempty"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		body.Query = strings.TrimSpace(body.Query)
		if body.Query == "" || len(body.Query) > 512 || utf8.RuneCountInString(body.Query) > 256 || (body.FolderID != "" && !TestingValidOpaqueID(body.FolderID, "slf_")) {
			http.Error(w, "invalid request", 400)
			return
		}
		folderID := body.FolderID
		if folderFromPath {
			folderID = chi.URLParam(r, "folderID")
		}
		var vector []float64
		var semanticOperation *hostedSemanticQueryOperation
		semanticAvailable := false
		if s.analyzer != nil && strings.TrimSpace(s.analyzer.APIKey) != "" && !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_SEARCH_EMERGENCY_DISABLE")), "true") {
			var embedErr error
			vector, semanticOperation, embedErr = s.cachedQueryEmbedding(r.Context(), userID, body.Query)
			if semanticOperation != nil {
				defer semanticOperation.Release(s.database)
			}
			if errors.Is(embedErr, errSemanticSearchRateLimited) {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": "semantic_search_rate_limited", "message": "Too many new semantic searches. Try again shortly."})
				return
			}
			semanticAvailable = embedErr == nil
		}
		hits, err := s.database.SearchSmartLibraryHybrid(userID, folderID, body.Query, vector, min(max(body.Limit, 1), 50))
		if !s.writeFolderError(w, err) {
			return
		}
		if semanticOperation != nil {
			if err := semanticOperation.Settle(s.database); err != nil {
				http.Error(w, "internal error", 500)
				return
			}
		}
		writeJSON(w, 200, map[string]any{"hits": hits, "queryModel": semanticModelName(s.analyzer, semanticAvailable), "indexVersion": serveragent.SmartLibraryIndexVersion, "semanticAvailable": semanticAvailable})
	}
}

func (s *SmartLibraryService) Delete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if !s.writeFolderError(w, s.database.DeleteSmartLibraryFolder(userID, chi.URLParam(r, "folderID"))) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SmartLibraryService) IndexStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		folderID := strings.TrimSpace(r.URL.Query().Get("folderId"))
		if folderID != "" && !TestingValidOpaqueID(folderID, "slf_") {
			http.Error(w, "invalid request", 400)
			return
		}
		status, err := s.database.SmartLibraryIndexStatusForUser(userID, folderID, currentEmbeddingModel(), serveragent.SmartLibraryIndexVersion)
		if !s.writeFolderError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]any{"currentVersion": status.CurrentVersion, "embeddingModel": status.EmbeddingModel, "outdatedAssets": status.OutdatedAssets, "failedAssets": status.FailedAssets, "upgradeNeeded": status.OutdatedAssets > 0})
	}
}

// PlanReindex is deliberately free: it creates durable, explicit work for the
// client to approve, but uploads nothing and invokes no model.
func (s *SmartLibraryService) PlanReindex() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			FolderID      string `json:"folderId,omitempty"`
			Cursor        string `json:"cursor,omitempty"`
			Limit         int    `json:"limit,omitempty"`
			TargetVersion int    `json:"targetVersion,omitempty"`
		}
		if decodeAIJSON(w, r, &body) != nil || (body.FolderID != "" && !TestingValidOpaqueID(body.FolderID, "slf_")) || len(body.Cursor) > 200 || (body.TargetVersion != 0 && body.TargetVersion != serveragent.SmartLibraryIndexVersion) {
			http.Error(w, "invalid request", 400)
			return
		}
		if body.Limit == 0 {
			body.Limit = 100
		}
		job, err := s.database.PlanSmartLibraryReindex(userID, body.FolderID, body.Cursor, currentEmbeddingModel(), serveragent.SmartLibraryIndexVersion, body.Limit)
		if !s.writeFolderError(w, err) {
			return
		}
		assets := make([]map[string]any, 0, len(job.Assets))
		for _, asset := range job.Assets {
			assets = append(assets, map[string]any{"assetId": asset.AssetID, "folderId": asset.FolderID, "fingerprint": asset.Fingerprint, "assetKind": asset.AssetKind, "mimeType": asset.MimeType, "requiresPreview": asset.RequiresPreview})
		}
		writeJSON(w, http.StatusCreated, map[string]any{"jobId": job.ID, "status": job.Status, "targetVersion": job.Version, "embeddingModel": job.Model, "nextCursor": job.Cursor, "assets": assets, "hostedAIWeeklyRatio": s.hostedAIWeeklyRatio(userID, appbilling.EstimateSmartLibraryCharge(len(job.Assets)))})
	}
}

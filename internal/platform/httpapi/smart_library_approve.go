package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
)

func (s *SmartLibraryService) Approve(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_EMERGENCY_DISABLE")), "true") {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "smart_library_disabled", "message": "Image analysis is temporarily disabled. Weekly usage was not charged."})
			return
		}
		if s.analyzer == nil || strings.TrimSpace(s.analyzer.APIKey) == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "smart_library_unavailable", "message": "Image analysis is not configured. Weekly usage was not charged."})
			return
		}
		var body smartLibraryApproval
		if decodeSmartLibraryJSON(w, r, &body) != nil || body.BillingMeter != db.CreditMeterAssetAnalysisImage || len(body.Previews) == 0 || len(body.Previews) > smartLibraryBatchSize || (body.MaximumSuccessfulImages != 0 && body.MaximumSuccessfulImages != smartLibraryLimit) {
			http.Error(w, "invalid request", 400)
			return
		}
		refs := make([]db.SmartLibraryPreviewRef, 0, len(body.Previews))
		assets := make([]serveragent.SmartLibraryAsset, 0, len(body.Previews))
		totalBytes := 0
		for _, preview := range body.Previews {
			var raw []byte
			var err error
			if preview.Base64 != "" {
				raw, err = base64.StdEncoding.DecodeString(preview.Base64)
			}
			assetKind := strings.ToLower(strings.TrimSpace(preview.AssetKind))
			if assetKind == "" {
				assetKind = "image"
			}
			asset := serveragent.SmartLibraryAsset{AssetID: preview.AssetID, AssetKind: assetKind, MimeType: strings.ToLower(strings.TrimSpace(preview.MimeType)), Bytes: raw, ExtractedText: preview.ExtractedText, Metadata: preview.Metadata}
			if preview.Truncated {
				if asset.Metadata == nil {
					asset.Metadata = map[string]string{}
				}
				asset.Metadata["contentTruncated"] = "true"
			}
			if err != nil || !TestingValidOpaqueID(preview.AssetID, "asset_") || len(preview.Fingerprint) != 64 || len(raw) > 1<<20 || serveragent.ValidateSmartLibraryAsset(asset) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			totalBytes += len(raw)
			refs = append(refs, db.SmartLibraryPreviewRef{AssetID: preview.AssetID, Fingerprint: preview.Fingerprint, AssetKind: asset.AssetKind, MimeType: asset.MimeType})
			assets = append(assets, asset)
		}
		if totalBytes > 4<<20 {
			http.Error(w, "preview batch too large", http.StatusRequestEntityTooLarge)
			return
		}
		folderID := chi.URLParam(r, "folderID")
		batch, err := s.database.CreateSmartLibraryBatch(userID, folderID, kind, refs)
		if !s.writeFolderError(w, err) {
			return
		}
		var reservation *db.CreditReservation
		tier := db.TierBasic
		license, licenseErr := s.database.GetLicenseByUserID(userID)
		if licenseErr != nil {
			_ = s.database.ResetSmartLibraryBatch(batch.ID)
			http.Error(w, "internal error", 500)
			return
		}
		if license != nil {
			tier = license.Tier
		}
		var usageWallet *db.CreditWallet
		reservation, usageWallet, err = s.database.ReserveCredits(userID, tier, db.CreditMeterAssetAnalysisImage, "smart-library:"+batch.ID, appbilling.EstimateSmartLibraryCharge(len(assets)), time.Now())
		if err != nil {
			_ = s.database.ResetSmartLibraryBatch(batch.ID)
			var insufficient db.HostedAILimitReachedError
			if errors.As(err, &insufficient) {
				response := map[string]any{"code": "hosted_ai_limit_reached", "message": "Your weekly AI agent usage is fully used."}
				if usageWallet != nil {
					response["reset_at"] = usageWallet.ResetAt
				}
				writeJSON(w, http.StatusPaymentRequired, response)
				return
			}
			http.Error(w, "internal error", 500)
			return
		}
		analysis, analysisErr := s.analyzer.Analyze(r.Context(), assets)
		failures := analysis.Failures
		if failures == nil {
			failures = map[string]string{}
		}
		resolved := map[string]bool{}
		completions := make([]db.SmartLibraryCompletion, 0, len(analysis.Results))
		var embeddingTokens int64
		embeddingCount := 0
		assetsByID := map[string]serveragent.SmartLibraryAsset{}
		for _, asset := range assets {
			assetsByID[asset.AssetID] = asset
		}
		for _, result := range analysis.Results {
			resolved[result.AssetID] = true
			delete(failures, result.AssetID)
			completion := db.SmartLibraryCompletion{AssetID: result.AssetID, Description: result.Description, Tags: result.Tags, Collections: result.SuggestedCollections, Confidence: result.Confidence, Model: result.Model, FallbackReason: result.FallbackReason, AssetKind: assetsByID[result.AssetID].AssetKind, MimeType: assetsByID[result.AssetID].MimeType, Metadata: richMetadata(result)}
			embeddings, usage, embedErr := s.analyzer.EmbedAssets(r.Context(), []serveragent.SmartLibraryAsset{assetsByID[result.AssetID]}, map[string]serveragent.SmartLibraryMetadata{result.AssetID: result})
			analysis.Usage.InputTokens += usage.InputTokens
			embeddingTokens += usage.InputTokens
			if embedErr == nil && len(embeddings) == 1 {
				embeddingCount++
				completion.Embedding = embeddings[0].Vector
				completion.EmbeddingModel = embeddings[0].Model
				completion.EmbeddingVersion = embeddings[0].Version
				completion.EmbeddingInputHash = embeddings[0].InputHash
			}
			completions = append(completions, completion)
		}
		for id := range failures {
			resolved[id] = true
		}
		for _, asset := range assets {
			if !resolved[asset.AssetID] {
				code := "analysis_failed"
				if analysisErr != nil {
					code = "provider_failed"
				}
				failures[asset.AssetID] = code
			}
		}
		_ = s.database.RecordSmartLibraryCostEvent(userID, folderID, batch.ID, "smart-library-routing", len(assets), analysis.Usage.InputTokens, analysis.Usage.OutputTokens, len(analysis.Results) > 0)
		if embeddingTokens > 0 {
			_ = s.database.RecordSmartLibrarySemanticUsage(userID, folderID, "semantic_index", currentEmbeddingModel(), embeddingCount, embeddingTokens, 0, true)
		}
		folder, err := s.database.CompleteSmartLibraryBatch(userID, batch.ID, completions, failures, body.FinalBatch)
		if err != nil {
			if reservation != nil {
				_ = s.database.ReleaseCreditReservation(reservation.ID)
			}
			_ = s.database.ResetSmartLibraryBatch(batch.ID)
			http.Error(w, "internal error", 500)
			return
		}
		if reservation != nil {
			if len(completions) == 0 {
				_ = s.database.ReleaseCreditReservation(reservation.ID)
			} else {
				charge := appbilling.SmartLibraryCharge(analysis.Usage, embeddingCount)
				if _, err = s.database.SettleCreditReservation(reservation.ID, "smart-library-settle:"+batch.ID, db.CreditUsage{Provider: "vercel_ai_gateway", Model: "smart-library-routing", InputTokens: analysis.Usage.InputTokens, CachedInputTokens: analysis.Usage.CachedInputTokens, OutputTokens: analysis.Usage.OutputTokens, ProviderCost: appbilling.SmartLibraryProviderCost(analysis.Usage, embeddingCount), ChargeMicrousd: charge}); err != nil {
					_ = s.database.ReleaseCreditReservation(reservation.ID)
					http.Error(w, "internal error", 500)
					return
				}
			}
		}
		batch.Status = "completed"
		if len(completions) == 0 {
			batch.Status = "failed"
		} else if len(failures) > 0 {
			batch.Status = "partially_failed"
		}
		batch.SuccessfulImages = len(completions)
		batch.FailedImages = len(failures)
		writeJSON(w, http.StatusOK, progressPayload(folder, []db.SmartLibraryBatch{*batch}, s.estimate(userID, folder)))
	}
}

func (s *SmartLibraryService) Progress() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		folderID := chi.URLParam(r, "folderID")
		if err := s.database.RecoverStaleSmartLibraryBatches(userID, folderID); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		folder, err := s.database.SmartLibraryFolder(userID, folderID)
		if !s.writeFolderError(w, err) {
			return
		}
		phase := folder.State
		if phase == "preflight" {
			phase = "sample_ready"
		}
		batches, err := s.database.SmartLibraryBatches(userID, folder.ID)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		payload := progressPayload(folder, batches, s.estimate(userID, folder))
		sampleAssetIDs, err := s.database.SmartLibrarySampleAssetIDs(userID, folder.ID)
		if err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		payload["sampleAssetIds"] = sampleAssetIDs
		payload["phase"] = phase
		if indexStatus, indexErr := s.database.SmartLibraryIndexStatusForUser(userID, folder.ID, currentEmbeddingModel(), serveragent.SmartLibraryIndexVersion); indexErr == nil {
			payload["indexStatus"] = map[string]any{"currentVersion": indexStatus.CurrentVersion, "embeddingModel": indexStatus.EmbeddingModel, "outdatedAssets": indexStatus.OutdatedAssets, "failedAssets": indexStatus.FailedAssets, "upgradeNeeded": indexStatus.OutdatedAssets > 0}
		}
		writeJSON(w, 200, payload)
	}
}

func (s *SmartLibraryService) Results() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		results, next, err := s.database.SmartLibraryResults(userID, chi.URLParam(r, "folderID"), after)
		if !s.writeFolderError(w, err) {
			return
		}
		payload := make([]map[string]any, 0, len(results))
		for _, result := range results {
			payload = append(payload, map[string]any{"assetId": result.AssetID, "status": result.Status, "assetKind": result.AssetKind, "mimeType": result.MimeType, "description": result.Description, "tags": result.Tags, "suggestedCollections": result.Collections, "metadata": result.Metadata, "confidence": result.Confidence, "failure": result.FailureCode})
		}
		writeJSON(w, 200, map[string]any{"results": payload, "nextSequence": next})
	}
}

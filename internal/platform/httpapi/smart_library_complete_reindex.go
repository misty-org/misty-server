package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
)

func (s *SmartLibraryService) CompleteReindex() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if strings.EqualFold(strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_EMERGENCY_DISABLE")), "true") {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "smart_library_disabled", "message": "Semantic indexing is temporarily disabled. No model call was made."})
			return
		}
		if s.analyzer == nil || strings.TrimSpace(s.analyzer.APIKey) == "" {
			writeJSON(w, 503, map[string]any{"code": "smart_library_unavailable", "message": "Semantic indexing is not configured."})
			return
		}
		var body struct {
			Assets []struct {
				AssetID       string            `json:"assetId"`
				Fingerprint   string            `json:"fingerprint"`
				AssetKind     string            `json:"assetKind"`
				MimeType      string            `json:"mimeType"`
				Base64        string            `json:"base64,omitempty"`
				ExtractedText string            `json:"extractedText,omitempty"`
				Metadata      map[string]string `json:"metadata,omitempty"`
				Truncated     bool              `json:"truncated,omitempty"`
			} `json:"assets"`
		}
		if decodeSmartLibraryJSON(w, r, &body) != nil || len(body.Assets) == 0 || len(body.Assets) > smartLibraryBatchSize {
			http.Error(w, "invalid request", 400)
			return
		}
		refs := make([]db.SmartLibraryPreviewRef, 0, len(body.Assets))
		inputs := make([]serveragent.SmartLibraryAsset, 0, len(body.Assets))
		totalBytes := 0
		for _, item := range body.Assets {
			var raw []byte
			var err error
			if item.Base64 != "" {
				raw, err = base64.StdEncoding.DecodeString(item.Base64)
			}
			if item.Truncated {
				if item.Metadata == nil {
					item.Metadata = map[string]string{}
				}
				item.Metadata["contentTruncated"] = "true"
			}
			input := serveragent.SmartLibraryAsset{AssetID: item.AssetID, AssetKind: item.AssetKind, MimeType: item.MimeType, Bytes: raw, ExtractedText: item.ExtractedText, Metadata: item.Metadata}
			if err != nil || !TestingValidOpaqueID(item.AssetID, "asset_") || len(item.Fingerprint) != 64 || len(raw) > 1<<20 || serveragent.ValidateSmartLibraryAsset(input) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			totalBytes += len(raw)
			refs = append(refs, db.SmartLibraryPreviewRef{AssetID: item.AssetID, Fingerprint: item.Fingerprint, AssetKind: item.AssetKind, MimeType: item.MimeType})
			inputs = append(inputs, input)
		}
		if totalBytes > 4<<20 {
			http.Error(w, "preview batch too large", http.StatusRequestEntityTooLarge)
			return
		}
		tier := db.TierBasic
		license, licenseErr := s.database.GetLicenseByUserID(userID)
		if licenseErr != nil {
			http.Error(w, "internal error", 500)
			return
		}
		if license != nil {
			tier = license.Tier
		}
		requestPayload, _ := json.Marshal(body.Assets)
		requestDigest := sha256.Sum256(requestPayload)
		requestKey := "smart-library-reindex:" + chi.URLParam(r, "jobID") + ":" + base64.RawURLEncoding.EncodeToString(requestDigest[:])
		reservation, usageWallet, err := s.database.ReserveCredits(userID, tier, db.CreditMeterAssetAnalysisImage, requestKey, appbilling.EstimateSmartLibraryCharge(len(inputs)), time.Now())
		if err != nil {
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
		settled := false
		defer func() {
			if !settled && reservation != nil {
				_ = s.database.ReleaseCreditReservation(reservation.ID)
			}
		}()
		job, records, err := s.database.SmartLibraryReindexRecords(userID, chi.URLParam(r, "jobID"), refs)
		if !s.writeFolderError(w, err) {
			return
		}
		if len(records) == 0 {
			writeJSON(w, 200, map[string]any{"jobId": job.ID, "status": job.Status, "completedAssets": job.Completed, "failedAssets": job.Failed, "failures": map[string]string{}})
			return
		}
		inputByID := map[string]serveragent.SmartLibraryAsset{}
		for _, input := range inputs {
			inputByID[input.AssetID] = input
		}
		completions := map[string]db.SmartLibraryCompletion{}
		failures := map[string]string{}
		refreshByFolder := map[string][]serveragent.SmartLibraryAsset{}
		refreshNeeded := map[string]bool{}
		for _, record := range records {
			input, found := inputByID[record.AssetID]
			if !found {
				http.Error(w, "invalid claimed asset", http.StatusConflict)
				return
			}
			key := record.FolderID + "\x00" + record.AssetID
			if serveragent.SmartLibraryMetadataNeedsRefresh(agentMetadata(record)) {
				refreshNeeded[key] = true
				refreshByFolder[record.FolderID] = append(refreshByFolder[record.FolderID], input)
			}
		}
		refreshedMetadata := map[string]serveragent.SmartLibraryMetadata{}
		var totalUsage serveragent.ModelUsage
		embeddingCount := 0
		for folderID, refreshInputs := range refreshByFolder {
			analysis, analyzeErr := s.analyzer.Analyze(r.Context(), refreshInputs)
			totalUsage.InputTokens += analysis.Usage.InputTokens
			totalUsage.CachedInputTokens += analysis.Usage.CachedInputTokens
			totalUsage.OutputTokens += analysis.Usage.OutputTokens
			model := "smart-library-metadata-refresh"
			if len(analysis.Results) > 0 && analysis.Results[0].Model != "" {
				model = analysis.Results[0].Model
			}
			success := analyzeErr == nil && len(analysis.Results) == len(refreshInputs)
			_ = s.database.RecordSmartLibrarySemanticUsage(userID, folderID, "reindex", model, len(refreshInputs), analysis.Usage.InputTokens, analysis.Usage.OutputTokens, success)
			if analyzeErr == nil {
				for _, metadata := range analysis.Results {
					refreshedMetadata[folderID+"\x00"+metadata.AssetID] = metadata
				}
			}
		}
		for _, record := range records {
			input := inputByID[record.AssetID]
			key := record.FolderID + "\x00" + record.AssetID
			metadata := agentMetadata(record)
			metadataRefreshed := refreshNeeded[key]
			if metadataRefreshed {
				var found bool
				metadata, found = refreshedMetadata[key]
				if !found {
					failures[key] = "metadata_refresh_failed"
					continue
				}
			}
			embeddings, usage, embedErr := s.analyzer.EmbedAssets(r.Context(), []serveragent.SmartLibraryAsset{input}, map[string]serveragent.SmartLibraryMetadata{input.AssetID: metadata})
			totalUsage.InputTokens += usage.InputTokens
			totalUsage.CachedInputTokens += usage.CachedInputTokens
			totalUsage.OutputTokens += usage.OutputTokens
			_ = s.database.RecordSmartLibrarySemanticUsage(userID, record.FolderID, "reindex", currentEmbeddingModel(), 1, usage.InputTokens, 0, embedErr == nil)
			if embedErr != nil || len(embeddings) != 1 {
				failures[key] = "embedding_failed"
				continue
			}
			embeddingCount++
			completion := db.SmartLibraryCompletion{AssetID: record.AssetID, Embedding: embeddings[0].Vector, EmbeddingInputHash: embeddings[0].InputHash}
			if metadataRefreshed {
				completion.Description = metadata.Description
				completion.Tags = metadata.Tags
				completion.Collections = metadata.SuggestedCollections
				completion.Confidence = metadata.Confidence
				completion.Model = metadata.Model
				completion.FallbackReason = metadata.FallbackReason
				completion.Metadata = richMetadata(metadata)
			}
			completions[key] = completion
		}
		if len(records) > 0 {
			job, err = s.database.CompleteSmartLibraryReindexJob(userID, job.ID, completions, failures)
			if !s.writeFolderError(w, err) {
				return
			}
		}
		if len(completions) > 0 {
			charge := appbilling.SmartLibraryCharge(totalUsage, embeddingCount)
			if _, err = s.database.SettleCreditReservation(reservation.ID, "smart-library-reindex-settle:"+requestKey, db.CreditUsage{Provider: "vercel_ai_gateway", Model: "smart-library-reindex", InputTokens: totalUsage.InputTokens, CachedInputTokens: totalUsage.CachedInputTokens, OutputTokens: totalUsage.OutputTokens, ProviderCost: appbilling.SmartLibraryProviderCost(totalUsage, embeddingCount), ChargeMicrousd: charge}); err != nil {
				http.Error(w, "internal error", 500)
				return
			}
			settled = true
		}
		writeJSON(w, 200, map[string]any{"jobId": job.ID, "status": job.Status, "completedAssets": job.Completed, "failedAssets": job.Failed, "failures": failures})
	}
}

func (s *SmartLibraryService) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := sessionUserID(r, s.database)
	if err != nil {
		http.Error(w, "internal error", 500)
		return "", false
	}
	if userID == "" {
		http.Error(w, "not authenticated", 401)
		return "", false
	}
	return userID, true
}

func (s *SmartLibraryService) writeFolderError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, db.ErrSmartLibraryNotFound):
		http.Error(w, "folder not found", 404)
	case errors.Is(err, db.ErrSmartLibraryLimit):
		writeJSON(w, 409, map[string]any{"code": "smart_library_limit", "message": "The 500-image pilot limit has been reached."})
	default:
		http.Error(w, "internal error", 500)
	}
	return false
}

func allowance(folder *db.SmartLibraryFolder) map[string]any {
	return map[string]any{"sampleImages": smartLibrarySampleSize, "maximumAnalyzedImages": smartLibraryLimit, "sampleIncluded": false, "remainingImages": max(0, smartLibraryLimit-folder.SuccessfulImages)}
}

func (s *SmartLibraryService) estimate(userID string, folder *db.SmartLibraryFolder) map[string]any {
	if folder == nil {
		return map[string]any{}
	}
	return map[string]any{"eligibleImages": folder.EligibleImages, "includedImages": folder.IncludedImages, "billableImages": folder.BillableImages, "hostedAIWeeklyRatio": s.hostedAIWeeklyRatio(userID, appbilling.EstimateSmartLibraryCharge(folder.BillableImages))}
}

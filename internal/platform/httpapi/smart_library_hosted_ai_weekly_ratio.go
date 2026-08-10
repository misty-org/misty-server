package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func (s *SmartLibraryService) hostedAIWeeklyRatio(userID string, estimatedCharge int64) float64 {
	tier := db.TierBasic
	if license, err := s.database.GetLicenseByUserID(userID); err == nil && license != nil {
		tier = license.Tier
	}
	allowance := db.EntitlementsForTier(tier).WeeklyHostedAIAllowance
	if allowance <= 0 || estimatedCharge <= 0 {
		return 0
	}
	return float64(estimatedCharge) / float64(allowance)
}

func TestingValidOpaqueID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) <= 96 && !strings.ContainsAny(value, "/\\ \t\r\n")
}

func validAssetDescriptor(kind, mimeType string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if kind == "video" || strings.HasPrefix(mimeType, "video/") {
		return false
	}
	switch kind {
	case "", "image", "document", "text", "audio", "archive", "binary":
		return true
	default:
		return false
	}
}

type smartLibraryApproval struct {
	Previews []struct {
		AssetID       string            `json:"assetId"`
		Fingerprint   string            `json:"fingerprint"`
		MimeType      string            `json:"mimeType"`
		AssetKind     string            `json:"assetKind,omitempty"`
		Base64        string            `json:"base64,omitempty"`
		ExtractedText string            `json:"extractedText,omitempty"`
		Metadata      map[string]string `json:"metadata,omitempty"`
		Truncated     bool              `json:"truncated,omitempty"`
	} `json:"previews"`
	FinalBatch              bool   `json:"finalBatch"`
	BillingMeter            string `json:"billingMeter"`
	MaximumSuccessfulImages int    `json:"maximumSuccessfulImages,omitempty"`
}

func decodeSmartLibraryJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func min(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func progressPayload(folder *db.SmartLibraryFolder, batches []db.SmartLibraryBatch, estimate map[string]any) map[string]any {
	queued := 0
	items := make([]map[string]any, 0, len(batches))
	for _, batch := range batches {
		pending := len(batch.AssetIDs) - batch.SuccessfulImages - batch.FailedImages
		if pending > 0 {
			queued += pending
		}
		items = append(items, map[string]any{"batchId": batch.ID, "assetIds": batch.AssetIDs, "status": batch.Status, "completedImages": batch.SuccessfulImages, "failedImages": batch.FailedImages})
	}
	return map[string]any{"folderId": folder.ID, "phase": folder.State, "successfulImages": folder.SuccessfulImages, "failedImages": folder.FailedImages, "queuedImages": queued, "batches": items, "estimate": estimate, "nextResultSequence": 0, "message": nil}
}

func richMetadata(value serveragent.SmartLibraryMetadata) db.SmartLibraryRichMetadata {
	return db.SmartLibraryRichMetadata{ContentType: value.ContentType, PrimarySubject: value.PrimarySubject, SearchTerms: value.SearchTerms, Entities: value.Entities, Characters: value.Characters, Brands: value.Brands, Applications: value.Applications, Objects: value.Objects, Scenes: value.Scenes, Activities: value.Activities, Colors: value.Colors, VisibleText: value.VisibleText, Topics: value.Topics}
}

func agentMetadata(value db.SmartLibraryReindexAsset) serveragent.SmartLibraryMetadata {
	return serveragent.SmartLibraryMetadata{AssetID: value.AssetID, ContentType: value.Metadata.ContentType, PrimarySubject: value.Metadata.PrimarySubject, Description: value.Description, Tags: value.Tags, SearchTerms: value.Metadata.SearchTerms, Entities: value.Metadata.Entities, Characters: value.Metadata.Characters, Brands: value.Metadata.Brands, Applications: value.Metadata.Applications, Objects: value.Metadata.Objects, Scenes: value.Metadata.Scenes, Activities: value.Metadata.Activities, Colors: value.Metadata.Colors, VisibleText: value.Metadata.VisibleText, Topics: value.Metadata.Topics, SuggestedCollections: value.Collections, Confidence: 1}
}

func currentEmbeddingModel() string {
	if value := strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_EMBEDDING_MODEL")); value != "" {
		return value
	}
	return serveragent.SmartLibraryEmbeddingModel
}

func semanticModelName(analyzer *serveragent.SmartLibraryAnalyzer, available bool) any {
	if !available {
		return nil
	}
	return currentEmbeddingModel()
}

func (s *SmartLibraryService) cachedQueryEmbedding(ctx context.Context, userID, query string) ([]float64, *hostedSemanticQueryOperation, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	cacheKey := sha256.Sum256([]byte(userID + "\x00" + normalized))
	userKey := sha256.Sum256([]byte(userID))
	now := time.Now()
	s.searchMu.Lock()
	if cached, ok := s.queryCache[cacheKey]; ok && now.Before(cached.expires) {
		vector := append([]float64(nil), cached.vector...)
		s.searchMu.Unlock()
		return vector, nil, nil
	}
	window := s.queryWindows[userKey]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = semanticQueryWindow{started: now}
	}
	if window.count >= 30 {
		s.queryWindows[userKey] = window
		s.searchMu.Unlock()
		return nil, nil, errSemanticSearchRateLimited
	}
	window.count++
	s.queryWindows[userKey] = window
	s.searchMu.Unlock()
	dailyLimit := 500
	if raw := strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_SEARCH_DAILY_LIMIT")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			dailyLimit = parsed
		}
	}
	if count, countErr := s.database.SmartLibrarySemanticCallsToday(userID, "semantic_query"); countErr == nil && count >= dailyLimit {
		return nil, nil, errSemanticSearchRateLimited
	}
	operation, err := beginHostedSemanticQuery(ctx, s.database, s.analyzer, userID, "smart-library-query:"+strconv.FormatInt(now.UnixNano(), 10), normalized)
	usage := serveragent.ModelUsage{}
	if operation != nil {
		usage = operation.Usage
	}
	_ = s.database.RecordSmartLibrarySemanticUsage(userID, "", "semantic_query", currentEmbeddingModel(), 1, usage.InputTokens, 0, err == nil)
	if err != nil {
		return nil, nil, err
	}
	vector := operation.Vector
	operation.OnSettled = func() {
		s.searchMu.Lock()
		defer s.searchMu.Unlock()
		if len(s.queryCache) >= 256 {
			for key, item := range s.queryCache {
				if now.After(item.expires) {
					delete(s.queryCache, key)
				}
			}
			if len(s.queryCache) >= 256 {
				for key := range s.queryCache {
					delete(s.queryCache, key)
					break
				}
			}
		}
		s.queryCache[cacheKey] = cachedSemanticQuery{vector: append([]float64(nil), vector...), expires: now.Add(5 * time.Minute)}
	}
	return vector, operation, nil
}

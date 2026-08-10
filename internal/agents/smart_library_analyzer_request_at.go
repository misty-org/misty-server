package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func (a *SmartLibraryAnalyzer) requestAt(ctx context.Context, url string, body any, headers map[string]string, dst any) error {
	key := strings.TrimSpace(a.APIKey)
	if key == "" {
		return errors.New("AI Gateway key is required")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 75 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("AI Gateway status %d", response.StatusCode)
	}
	if err = json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("AI Gateway returned invalid JSON: %w", err)
	}
	return nil
}

func (a *SmartLibraryAnalyzer) chatBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if base == "" {
		return "https://ai-gateway.vercel.sh/v1"
	}
	return base
}

func (a *SmartLibraryAnalyzer) embeddingBaseURL() string {
	if override := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("AI_GATEWAY_EMBEDDING_BASE_URL")), "/"); override != "" {
		return override
	}
	base := a.chatBaseURL()
	if strings.HasSuffix(base, "/v1") {
		return strings.TrimSuffix(base, "/v1") + "/v4/ai"
	}
	return base
}

func (a *SmartLibraryAnalyzer) primaryModel() string {
	if value := strings.TrimSpace(a.PrimaryModel); value != "" {
		return value
	}
	if value := strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_PRIMARY_MODEL")); value != "" {
		return value
	}
	return SmartLibraryPrimaryModel
}

func (a *SmartLibraryAnalyzer) fallbackModel() string {
	if value := strings.TrimSpace(a.FallbackModel); value != "" {
		return value
	}
	if value := strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_FALLBACK_MODEL")); value != "" {
		return value
	}
	return SmartLibraryFallbackModel
}

func (a *SmartLibraryAnalyzer) embeddingModel() string {
	if value := strings.TrimSpace(a.EmbeddingModel); value != "" {
		return value
	}
	if value := strings.TrimSpace(envconfig.Getenv("SMART_LIBRARY_EMBEDDING_MODEL")); value != "" {
		return value
	}
	return SmartLibraryEmbeddingModel
}

func (a *SmartLibraryAnalyzer) threshold() float64 {
	if a.ConfidenceThreshold > 0 && a.ConfidenceThreshold <= 1 {
		return a.ConfidenceThreshold
	}
	return .62
}

func metadataFallbackReason(result SmartLibraryMetadata, found bool, threshold float64) string {
	if !found {
		return "schema_invalid"
	}
	if len(strings.TrimSpace(result.Description)) < 24 || len(strings.TrimSpace(result.PrimarySubject)) < 3 {
		return "description_empty_or_generic"
	}
	if len(result.Tags) < 5 || len(result.SearchTerms) < 5 {
		return "required_categories_missing"
	}
	signals := len(result.Entities) + len(result.Characters) + len(result.Brands) + len(result.Applications) + len(result.Objects) + len(result.Scenes) + len(result.Activities) + len(result.VisibleText) + len(result.Topics)
	if signals < 3 {
		return "insufficient_search_signals"
	}
	contentType := strings.ToLower(result.ContentType)
	if strings.Contains(contentType, "screenshot") && len(result.Applications) == 0 && len(result.VisibleText) == 0 {
		return "interface_context_missing"
	}
	if result.Confidence < threshold {
		return "confidence_below_evaluated_threshold"
	}
	return ""
}

// SmartLibraryMetadataNeedsRefresh reports whether stored metadata predates or
// fails the current retrieval schema. Reindex callers use this to decide when
// an explicitly approved upgrade must regenerate labels before embedding.
func SmartLibraryMetadataNeedsRefresh(result SmartLibraryMetadata) bool {
	result = normalizeSmartLibraryMetadata(result)
	return metadataFallbackReason(result, true, 0) != "" || missingVisualEntityAudit(result)
}

func missingVisualEntityAudit(result SmartLibraryMetadata) bool {
	if len(result.Characters) > 0 || len(result.Brands) > 0 {
		return false
	}
	return shouldRunVisualEntityAudit(result)
}

func shouldRunVisualEntityAudit(result SmartLibraryMetadata) bool {
	document := strings.ToLower(strings.Join([]string{
		result.Description, result.PrimarySubject, result.ContentType,
		strings.Join(result.Tags, " "), strings.Join(result.SearchTerms, " "),
		strings.Join(result.Applications, " "), strings.Join(result.Scenes, " "),
	}, " "))
	return strings.Contains(document, "interface") || strings.Contains(document, "file manager") || strings.Contains(document, "file explorer") || strings.Contains(document, "desktop") || strings.Contains(document, "screenshot")
}

func mergeSmartLibraryMetadata(primary, audit SmartLibraryMetadata) SmartLibraryMetadata {
	merged := audit
	merged.Tags = mergeMetadataLists(primary.Tags, audit.Tags)
	merged.SearchTerms = mergeMetadataLists(primary.SearchTerms, audit.SearchTerms)
	merged.Entities = mergeMetadataLists(primary.Entities, audit.Entities)
	merged.Characters = mergeMetadataLists(primary.Characters, audit.Characters)
	merged.Brands = mergeMetadataLists(primary.Brands, audit.Brands)
	merged.Applications = mergeMetadataLists(primary.Applications, audit.Applications)
	merged.Objects = mergeMetadataLists(primary.Objects, audit.Objects)
	merged.Scenes = mergeMetadataLists(primary.Scenes, audit.Scenes)
	merged.Activities = mergeMetadataLists(primary.Activities, audit.Activities)
	merged.Colors = mergeMetadataLists(primary.Colors, audit.Colors)
	merged.VisibleText = mergeMetadataLists(primary.VisibleText, audit.VisibleText)
	merged.Topics = mergeMetadataLists(primary.Topics, audit.Topics)
	merged.SuggestedCollections = mergeMetadataLists(primary.SuggestedCollections, audit.SuggestedCollections)
	if merged.Confidence < primary.Confidence {
		merged.Confidence = primary.Confidence
	}
	return normalizeSmartLibraryMetadata(merged)
}

func mergeMetadataLists(values ...[]string) []string {
	seen := map[string]bool{}
	merged := []string{}
	for _, list := range values {
		for _, value := range list {
			key := strings.ToLower(strings.TrimSpace(value))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, value)
		}
	}
	return merged
}

func normalizeSmartLibraryMetadata(value SmartLibraryMetadata) SmartLibraryMetadata {
	value.ContentType = cleanMetadataString(value.ContentType, 80)
	value.PrimarySubject = cleanMetadataString(value.PrimarySubject, 160)
	value.Description = cleanMetadataString(value.Description, 640)
	value.Tags = cleanMetadataList(value.Tags, 24, 80)
	value.SearchTerms = cleanMetadataList(value.SearchTerms, 24, 120)
	value.Entities = cleanMetadataList(value.Entities, 16, 120)
	value.Characters = cleanMetadataList(value.Characters, 12, 120)
	value.Brands = cleanMetadataList(value.Brands, 12, 120)
	value.Applications = cleanMetadataList(value.Applications, 12, 120)
	value.Objects = cleanMetadataList(value.Objects, 20, 80)
	value.Scenes = cleanMetadataList(value.Scenes, 12, 120)
	value.Activities = cleanMetadataList(value.Activities, 12, 120)
	value.Colors = cleanMetadataList(value.Colors, 12, 40)
	value.VisibleText = cleanMetadataList(value.VisibleText, 20, 160)
	value.Topics = cleanMetadataList(value.Topics, 16, 120)
	value.SuggestedCollections = cleanMetadataList(value.SuggestedCollections, 8, 120)
	return value
}

func cleanMetadataString(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > max {
		value = value[:max]
	}
	return strings.TrimSpace(value)
}

func cleanMetadataList(values []string, maxItems, maxBytes int) []string {
	result := make([]string, 0, minInt(len(values), maxItems))
	seen := map[string]bool{}
	for _, value := range values {
		value = cleanMetadataString(value, maxBytes)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
		if len(result) == maxItems {
			break
		}
	}
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func stringArraySchema(minItems, maxItems int) map[string]any {
	return map[string]any{"type": "array", "minItems": minItems, "maxItems": maxItems, "items": map[string]any{"type": "string"}}
}

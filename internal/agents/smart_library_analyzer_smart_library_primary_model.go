package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	// The evaluated low-cost route remains primary; sparse or incomplete search
	// signals are retried once with the stronger fallback.
	SmartLibraryPrimaryModel   = "google/gemini-2.5-flash-lite"
	SmartLibraryFallbackModel  = "google/gemini-3.1-flash-lite"
	SmartLibraryEmbeddingModel = "google/gemini-embedding-2"
	SmartLibraryEmbeddingDims  = 768
	// Version 4 uses a dedicated, narrow visual-entity pass for interface
	// screenshots. Version 3 retried the stronger model with the same broad
	// captioning prompt, which could still focus on UI chrome and miss a dominant
	// background character such as Pikachu.
	SmartLibraryIndexVersion  = 4
	SmartLibraryMaxBatchSize  = 8
	SmartLibraryMaxTextBytes  = 64 << 10
	SmartLibraryMaxAssetBytes = 4 << 20
)

var allowedSmartLibraryKinds = map[string]bool{
	"image": true, "document": true, "text": true, "audio": true,
	"archive": true, "binary": true,
}

// SmartLibraryAsset contains only a short-lived, path-free representation.
// Bytes are accepted only for safe preview formats, never arbitrary binaries.
type SmartLibraryAsset struct {
	AssetID       string            `json:"assetId"`
	AssetKind     string            `json:"assetKind"`
	MimeType      string            `json:"mimeType"`
	Bytes         []byte            `json:"-"`
	ExtractedText string            `json:"extractedText,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// SmartLibraryImage remains an alias for source compatibility with the pilot.
type SmartLibraryImage = SmartLibraryAsset

type SmartLibraryMetadata struct {
	AssetID              string   `json:"assetId"`
	ContentType          string   `json:"contentType"`
	PrimarySubject       string   `json:"primarySubject"`
	Description          string   `json:"description"`
	Tags                 []string `json:"tags"`
	SearchTerms          []string `json:"searchTerms"`
	Entities             []string `json:"entities"`
	Characters           []string `json:"characters"`
	Brands               []string `json:"brands"`
	Applications         []string `json:"applications"`
	Objects              []string `json:"objects"`
	Scenes               []string `json:"scenes"`
	Activities           []string `json:"activities"`
	Colors               []string `json:"colors"`
	VisibleText          []string `json:"visibleText"`
	Topics               []string `json:"topics"`
	SuggestedCollections []string `json:"suggestedCollections"`
	Confidence           float64  `json:"confidence"`
	Model                string   `json:"-"`
	FallbackReason       string   `json:"-"`
}

func (m SmartLibraryMetadata) SearchDocument() string {
	parts := []string{
		m.PrimarySubject, m.Description, m.ContentType,
		strings.Join(m.Tags, " "), strings.Join(m.SearchTerms, " "),
		strings.Join(m.Entities, " "), strings.Join(m.Characters, " "),
		strings.Join(m.Brands, " "), strings.Join(m.Applications, " "),
		strings.Join(m.Objects, " "), strings.Join(m.Scenes, " "),
		strings.Join(m.Activities, " "), strings.Join(m.Colors, " "),
		strings.Join(m.VisibleText, " "), strings.Join(m.Topics, " "),
		strings.Join(m.SuggestedCollections, " "),
	}
	return strings.Join(parts, " | ")
}

type SmartLibraryAnalysis struct {
	Results  []SmartLibraryMetadata
	Failures map[string]string
	Usage    ModelUsage
}

type SmartLibraryEmbedding struct {
	AssetID, Model, InputHash string
	Version                   int
	Vector                    []float64
}

type SmartLibraryAnalyzer struct {
	APIKey, BaseURL             string
	PrimaryModel, FallbackModel string
	EmbeddingModel              string
	Client                      *http.Client
	ConfidenceThreshold         float64
}

func (a *SmartLibraryAnalyzer) Analyze(ctx context.Context, assets []SmartLibraryAsset) (SmartLibraryAnalysis, error) {
	if len(assets) == 0 || len(assets) > SmartLibraryMaxBatchSize {
		return SmartLibraryAnalysis{}, errors.New("smart library batches must contain one to eight assets")
	}
	for _, asset := range assets {
		if err := ValidateSmartLibraryAsset(asset); err != nil {
			return SmartLibraryAnalysis{}, err
		}
	}
	primaryModel := a.primaryModel()
	fallbackModel := a.fallbackModel()
	primary, usage, err := a.analyzeWithModel(ctx, primaryModel, assets)
	analysis := SmartLibraryAnalysis{Failures: map[string]string{}, Usage: usage}
	byID := map[string]SmartLibraryMetadata{}
	for _, result := range primary {
		result = normalizeSmartLibraryMetadata(result)
		byID[result.AssetID] = result
	}
	for _, asset := range assets {
		result, found := byID[asset.AssetID]
		reason := metadataFallbackReason(result, found, a.threshold())
		if err != nil && reason == "" {
			reason = "primary_request_failed"
		}
		if reason == "" && shouldRunVisualEntityAudit(result) {
			audit, auditUsage, auditErr := a.analyzeWithModelPrompt(ctx, fallbackModel, []SmartLibraryAsset{asset}, visualEntityAuditPrompt)
			analysis.Usage.InputTokens += auditUsage.InputTokens
			analysis.Usage.CachedInputTokens += auditUsage.CachedInputTokens
			analysis.Usage.OutputTokens += auditUsage.OutputTokens
			if auditErr == nil && len(audit) == 1 && audit[0].AssetID == asset.AssetID {
				audit[0] = normalizeSmartLibraryMetadata(audit[0])
				if metadataFallbackReason(audit[0], true, a.threshold()) == "" {
					result = mergeSmartLibraryMetadata(result, audit[0])
					result.Model = fallbackModel
					result.FallbackReason = "visual_entity_audit"
				}
			}
		}
		if reason == "" {
			result.Model = primaryModel
			if result.FallbackReason == "visual_entity_audit" {
				result.Model = fallbackModel
			}
			analysis.Results = append(analysis.Results, result)
			continue
		}
		fallback, fallbackUsage, fallbackErr := a.analyzeWithModel(ctx, fallbackModel, []SmartLibraryAsset{asset})
		analysis.Usage.InputTokens += fallbackUsage.InputTokens
		analysis.Usage.CachedInputTokens += fallbackUsage.CachedInputTokens
		analysis.Usage.OutputTokens += fallbackUsage.OutputTokens
		if len(fallback) == 1 {
			fallback[0] = normalizeSmartLibraryMetadata(fallback[0])
		}
		if fallbackErr != nil || len(fallback) != 1 || fallback[0].AssetID != asset.AssetID || metadataFallbackReason(fallback[0], len(fallback) == 1, a.threshold()) != "" {
			analysis.Failures[asset.AssetID] = "analysis_quality_failed"
			continue
		}
		fallback[0].Model = fallbackModel
		fallback[0].FallbackReason = reason
		analysis.Results = append(analysis.Results, fallback[0])
	}
	if len(analysis.Results) == 0 && err != nil {
		return analysis, err
	}
	return analysis, nil
}

// AnalyzeForEvaluation runs one named candidate without production fallback so
// benchmark reports measure that model rather than the router.
func (a *SmartLibraryAnalyzer) AnalyzeForEvaluation(ctx context.Context, model string, assets []SmartLibraryAsset) ([]SmartLibraryMetadata, ModelUsage, error) {
	if strings.TrimSpace(model) == "" || len(assets) == 0 || len(assets) > SmartLibraryMaxBatchSize {
		return nil, ModelUsage{}, errors.New("invalid evaluation batch")
	}
	for _, asset := range assets {
		if err := ValidateSmartLibraryAsset(asset); err != nil {
			return nil, ModelUsage{}, err
		}
	}
	results, usage, err := a.analyzeWithModel(ctx, model, assets)
	if err != nil {
		return nil, usage, err
	}
	for i := range results {
		results[i] = normalizeSmartLibraryMetadata(results[i])
	}
	return results, usage, nil
}

// EmbedQuery embeds a user query in the same multimodal space as indexed assets.
func (a *SmartLibraryAnalyzer) EmbedQuery(ctx context.Context, query string) ([]float64, ModelUsage, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 512 || utf8.RuneCountInString(query) > 256 {
		return nil, ModelUsage{}, errors.New("invalid semantic query")
	}
	vectors, usage, err := a.embedGateway(ctx, []string{"task: search result | query: " + query}, nil)
	if err != nil || len(vectors) != 1 {
		return nil, usage, err
	}
	return vectors[0], usage, nil
}

// EmbedVisualQuery places a short-lived user image in the same Gemini
// multimodal embedding space as Smart Library, Space Library, and retrieval
// documents. Bytes are never persisted by the analyzer.
func (a *SmartLibraryAnalyzer) EmbedVisualQuery(ctx context.Context, mimeType string, image []byte, query string) ([]float64, ModelUsage, error) {
	if len(image) == 0 || len(image) > 1<<20 || (mimeType != "image/jpeg" && mimeType != "image/png" && mimeType != "image/webp") {
		return nil, ModelUsage{}, errors.New("invalid visual query")
	}
	query = strings.TrimSpace(query)
	if len(query) > 512 || utf8.RuneCountInString(query) > 256 {
		return nil, ModelUsage{}, errors.New("invalid visual query text")
	}
	if query == "" {
		query = "Find visually similar or semantically related Misty content."
	}
	return a.embedImageV1(ctx, "task: visual search result | query: "+query, SmartLibraryAsset{
		AssetID: "visual-query", AssetKind: "image", MimeType: mimeType, Bytes: image,
	})
}

// EmbedAssets uses direct image/PDF content when it is available and combines
// that signal with normalized generated metadata. Text-only assets share the
// exact same embedding space.
func (a *SmartLibraryAnalyzer) EmbedAssets(ctx context.Context, assets []SmartLibraryAsset, metadata map[string]SmartLibraryMetadata) ([]SmartLibraryEmbedding, ModelUsage, error) {
	if len(assets) == 0 {
		return nil, ModelUsage{}, nil
	}
	results := make([]SmartLibraryEmbedding, 0, len(assets))
	var total ModelUsage
	for _, asset := range assets {
		if err := ValidateSmartLibraryAsset(asset); err != nil {
			return nil, total, err
		}
		value := TestingEmbeddingDocument(asset, metadata[asset.AssetID])
		var vectors [][]float64
		var usage ModelUsage
		var err error
		switch {
		case len(asset.Bytes) > 0 && (asset.MimeType == "image/jpeg" || asset.MimeType == "image/png"):
			var vector []float64
			vector, usage, err = a.embedImageV1(ctx, value, asset)
			vectors = [][]float64{vector}
		default:
			vectors, usage, err = a.embedGateway(ctx, []string{value}, nil)
		}
		total.InputTokens += usage.InputTokens
		if err != nil {
			return nil, total, err
		}
		if len(vectors) != 1 {
			return nil, total, errors.New("gateway returned an unexpected embedding count")
		}
		inputHash := sha256.Sum256([]byte(value + "\x00" + asset.MimeType + "\x00" + string(asset.Bytes)))
		results = append(results, SmartLibraryEmbedding{AssetID: asset.AssetID, Model: a.embeddingModel(), Version: SmartLibraryIndexVersion, InputHash: hex.EncodeToString(inputHash[:]), Vector: vectors[0]})
	}
	return results, total, nil
}

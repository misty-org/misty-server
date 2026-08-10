package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

type manifest struct {
	Models            []string                            `json:"models"`
	Assets            []manifestAsset                     `json:"assets"`
	Queries           []serveragent.SmartLibraryEvalQuery `json:"queries"`
	RetrievalK        int                                 `json:"retrievalK"`
	MinimumTermRecall float64                             `json:"minimumTermRecall"`
	MinimumRecallAtK  float64                             `json:"minimumRecallAtK"`
}
type manifestAsset struct {
	AssetID       string            `json:"assetId"`
	Path          string            `json:"path,omitempty"`
	AssetKind     string            `json:"assetKind"`
	MimeType      string            `json:"mimeType,omitempty"`
	ExtractedText string            `json:"extractedText,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	ExpectedTerms []string          `json:"expectedTerms"`
}
type modelReport struct {
	Model        string                              `json:"model"`
	Metrics      serveragent.SmartLibraryEvalMetrics `json:"metrics"`
	InputTokens  int64                               `json:"inputTokens"`
	OutputTokens int64                               `json:"outputTokens"`
	Passed       bool                                `json:"passed"`
	Error        string                              `json:"error,omitempty"`
}

func main() {
	manifestPath := flag.String("manifest", "", "path to a labeled JSON manifest")
	live := flag.Bool("live", false, "allow paid AI Gateway calls")
	flag.Parse()
	_ = godotenv.Load()
	if !*live || strings.TrimSpace(os.Getenv("SMART_LIBRARY_EVAL_LIVE")) != "1" {
		fatal(errors.New("live evaluation requires both -live and SMART_LIBRARY_EVAL_LIVE=1"))
	}
	if *manifestPath == "" {
		fatal(errors.New("-manifest is required"))
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatal(err)
	}
	var spec manifest
	if err = json.Unmarshal(raw, &spec); err != nil {
		fatal(err)
	}
	if len(spec.Assets) == 0 || len(spec.Assets) > 100 {
		fatal(errors.New("manifest must contain 1-100 assets"))
	}
	if spec.RetrievalK < 1 {
		spec.RetrievalK = 5
	}
	if len(spec.Models) == 0 {
		spec.Models = []string{"google/gemini-2.5-flash-lite", "google/gemini-3-flash", "openai/gpt-4.1-nano", "openai/gpt-5.4-nano"}
	}
	assets, cases, err := loadAssets(filepath.Dir(*manifestPath), spec.Assets)
	if err != nil {
		fatal(err)
	}
	analyzer := &serveragent.SmartLibraryAnalyzer{APIKey: strings.TrimSpace(os.Getenv("AI_GATEWAY_API_KEY")), BaseURL: strings.TrimSpace(os.Getenv("AI_GATEWAY_BASE_URL"))}
	if analyzer.APIKey == "" {
		fatal(errors.New("AI_GATEWAY_API_KEY is required"))
	}
	reports := make([]modelReport, 0, len(spec.Models))
	allPassed := true
	for _, model := range spec.Models {
		report := evaluate(context.Background(), analyzer, model, assets, cases, spec)
		reports = append(reports, report)
		if !report.Passed {
			allPassed = false
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"reports": reports, "passed": allPassed, "embeddingModel": serveragent.SmartLibraryEmbeddingModel, "embeddingDimensions": serveragent.SmartLibraryEmbeddingDims})
	if !allPassed {
		os.Exit(1)
	}
}

func evaluate(ctx context.Context, analyzer *serveragent.SmartLibraryAnalyzer, model string, assets []serveragent.SmartLibraryAsset, cases []serveragent.SmartLibraryEvalMetadataCase, spec manifest) modelReport {
	report := modelReport{Model: model}
	results := []serveragent.SmartLibraryMetadata{}
	for start := 0; start < len(assets); start += serveragent.SmartLibraryMaxBatchSize {
		end := start + serveragent.SmartLibraryMaxBatchSize
		if end > len(assets) {
			end = len(assets)
		}
		batch, usage, err := analyzer.AnalyzeForEvaluation(ctx, model, assets[start:end])
		report.InputTokens += usage.InputTokens
		report.OutputTokens += usage.OutputTokens
		if err != nil {
			report.Error = err.Error()
			return report
		}
		results = append(results, batch...)
	}
	report.Metrics = serveragent.EvaluateSmartLibraryMetadata(results, cases)
	metadata := map[string]serveragent.SmartLibraryMetadata{}
	for _, result := range results {
		metadata[result.AssetID] = result
	}
	embeddings, usage, err := analyzer.EmbedAssets(ctx, assets, metadata)
	report.InputTokens += usage.InputTokens
	if err != nil {
		report.Error = err.Error()
		return report
	}
	assetIDs := make([]string, len(embeddings))
	assetVectors := make([][]float64, len(embeddings))
	for i, item := range embeddings {
		assetIDs[i] = item.AssetID
		assetVectors[i] = item.Vector
	}
	queryVectors := make([][]float64, 0, len(spec.Queries))
	for _, query := range spec.Queries {
		vector, usage, queryErr := analyzer.EmbedQuery(ctx, query.Query)
		report.InputTokens += usage.InputTokens
		if queryErr != nil {
			report.Error = queryErr.Error()
			return report
		}
		queryVectors = append(queryVectors, vector)
	}
	report.Metrics.Queries = len(spec.Queries)
	report.Metrics.RecallAtK = serveragent.EvaluateSmartLibraryRetrieval(assetIDs, assetVectors, spec.Queries, queryVectors, spec.RetrievalK)
	report.Passed = report.Metrics.TermRecall >= spec.MinimumTermRecall && report.Metrics.RecallAtK >= spec.MinimumRecallAtK
	return report
}

func loadAssets(base string, items []manifestAsset) ([]serveragent.SmartLibraryAsset, []serveragent.SmartLibraryEvalMetadataCase, error) {
	assets := make([]serveragent.SmartLibraryAsset, 0, len(items))
	cases := make([]serveragent.SmartLibraryEvalMetadataCase, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if item.AssetID == "" || seen[item.AssetID] {
			return nil, nil, errors.New("asset IDs must be non-empty and unique")
		}
		seen[item.AssetID] = true
		asset := serveragent.SmartLibraryAsset{AssetID: item.AssetID, AssetKind: item.AssetKind, MimeType: item.MimeType, ExtractedText: item.ExtractedText, Metadata: item.Metadata}
		if item.Path != "" {
			path := item.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(base, path)
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, nil, err
			}
			if info.Size() > serveragent.SmartLibraryMaxAssetBytes {
				return nil, nil, fmt.Errorf("fixture %s is too large", item.AssetID)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, err
			}
			if asset.MimeType == "" {
				asset.MimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
			}
			if asset.AssetKind == "image" {
				asset.Bytes = data
			} else if asset.AssetKind == "text" {
				if len(data) > serveragent.SmartLibraryMaxTextBytes {
					data = data[:serveragent.SmartLibraryMaxTextBytes]
				}
				asset.ExtractedText = string(data)
			} else {
				return nil, nil, fmt.Errorf("fixture paths are accepted only for image or text assets: %s", item.AssetID)
			}
		}
		if err := serveragent.ValidateSmartLibraryAsset(asset); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", item.AssetID, err)
		}
		assets = append(assets, asset)
		cases = append(cases, serveragent.SmartLibraryEvalMetadataCase{AssetID: item.AssetID, ExpectedTerms: item.ExpectedTerms})
	}
	return assets, cases, nil
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(2) }

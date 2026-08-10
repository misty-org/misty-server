package agent

import (
	"math"
	"sort"
	"strings"
)

type SmartLibraryEvalMetadataCase struct {
	AssetID       string
	ExpectedTerms []string
}

type SmartLibraryEvalQuery struct {
	Query            string   `json:"query"`
	ExpectedAssetIDs []string `json:"expectedAssetIds"`
}

type SmartLibraryEvalMetrics struct {
	TermRecall    float64 `json:"termRecall"`
	RecallAtK     float64 `json:"recallAtK"`
	Queries       int     `json:"queries"`
	ExpectedTerms int     `json:"expectedTerms"`
}

func EvaluateSmartLibraryMetadata(results []SmartLibraryMetadata, cases []SmartLibraryEvalMetadataCase) SmartLibraryEvalMetrics {
	byID := map[string]string{}
	for _, result := range results {
		byID[result.AssetID] = strings.ToLower(result.SearchDocument())
	}
	matched, total := 0, 0
	for _, item := range cases {
		document := byID[item.AssetID]
		for _, term := range item.ExpectedTerms {
			term = strings.ToLower(strings.TrimSpace(term))
			if term == "" {
				continue
			}
			total++
			if strings.Contains(document, term) {
				matched++
			}
		}
	}
	metrics := SmartLibraryEvalMetrics{ExpectedTerms: total}
	if total > 0 {
		metrics.TermRecall = float64(matched) / float64(total)
	}
	return metrics
}

func EvaluateSmartLibraryRetrieval(assetIDs []string, assetVectors [][]float64, queries []SmartLibraryEvalQuery, queryVectors [][]float64, k int) float64 {
	if k < 1 || len(queries) == 0 || len(assetIDs) != len(assetVectors) || len(queries) != len(queryVectors) {
		return 0
	}
	hits := 0
	for queryIndex, query := range queries {
		type scored struct {
			id    string
			score float64
		}
		scores := make([]scored, 0, len(assetIDs))
		for i, id := range assetIDs {
			scores = append(scores, scored{id: id, score: cosineSimilarity(queryVectors[queryIndex], assetVectors[i])})
		}
		sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
		if k > len(scores) {
			k = len(scores)
		}
		expected := map[string]bool{}
		for _, id := range query.ExpectedAssetIDs {
			expected[id] = true
		}
		found := false
		for _, item := range scores[:k] {
			if expected[item.id] {
				found = true
				break
			}
		}
		if found {
			hits++
		}
	}
	return float64(hits) / float64(len(queries))
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}
	var dot, aa, bb float64
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return -1
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}

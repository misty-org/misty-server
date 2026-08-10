package agent

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestSmartLibraryQualityMetrics(t *testing.T) {
	metadata := []SmartLibraryMetadata{{AssetID: "asset_pika", Description: "A file manager over character artwork", Characters: []string{"Pikachu"}, Applications: []string{"file manager"}, SearchTerms: []string{"Pokemon desktop"}}}
	metrics := EvaluateSmartLibraryMetadata(metadata, []SmartLibraryEvalMetadataCase{{AssetID: "asset_pika", ExpectedTerms: []string{"pikachu", "file manager", "pokemon"}}})
	if metrics.TermRecall != 1 {
		t.Fatalf("term recall=%v", metrics.TermRecall)
	}
	assetIDs := []string{"asset_pika", "asset_kitchen"}
	assetVectors := [][]float64{{1, 0}, {0, 1}}
	queries := []SmartLibraryEvalQuery{{Query: "electric mascot file browser", ExpectedAssetIDs: []string{"asset_pika"}}}
	queryVectors := [][]float64{{.9, .1}}
	if recall := EvaluateSmartLibraryRetrieval(assetIDs, assetVectors, queries, queryVectors, 1); recall != 1 {
		t.Fatalf("recall@1=%v", recall)
	}
}

func TestProductionPromptDoesNotContainBenchmarkAnswer(t *testing.T) {
	if containsFold(TestingRichMetadataPrompt, "pikachu") {
		t.Fatal("production prompt leaked the labeled evaluation answer")
	}
}

func containsFold(value, needle string) bool {
	return len(needle) > 0 && len(value) >= len(needle) && indexFold(value, needle) >= 0
}
func indexFold(value, needle string) int {
	for i := 0; i+len(needle) <= len(value); i++ {
		match := true
		for j := range needle {
			a, b := value[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

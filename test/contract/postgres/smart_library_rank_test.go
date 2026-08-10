package db

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestPruneWeakSemanticMatchesAfterExactMetadataHit(t *testing.T) {
	hits := []SmartLibrarySearchHit{
		{AssetID: "pikachu", Score: 0.70, SemanticScore: 0.56, LexicalScore: 1},
		{AssetID: "strong-neighbor", Score: 0.58, SemanticScore: 0.85},
		{AssetID: "cartoon-lookalike", Score: 0.41, SemanticScore: 0.61},
	}

	got := TestingPruneWeakSemanticMatches(hits)
	if len(got) != 2 || got[0].AssetID != "pikachu" || got[1].AssetID != "strong-neighbor" {
		t.Fatalf("pruned hits = %#v", got)
	}
}

func TestPruneWeakSemanticMatchesPreservesPureSemanticSearch(t *testing.T) {
	hits := []SmartLibrarySearchHit{
		{AssetID: "first", Score: 0.70, SemanticScore: 0.70},
		{AssetID: "distant", Score: 0.20, SemanticScore: 0.20},
	}

	got := TestingPruneWeakSemanticMatches(hits)
	if len(got) != len(hits) {
		t.Fatalf("pure semantic hits were pruned: %#v", got)
	}
}

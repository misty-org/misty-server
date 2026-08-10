package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestLibraryIntelligencePolicyProcessingSearchAndCleanup(t *testing.T) {
	database := openTestDatabase(t)
	ctx := context.Background()
	owner, _ := database.CreateUser("Intelligence Owner", "intelligence-owner@example.com", "password123")
	spaceID := createTestSpace(t, database, ctx, owner.ID, "Intelligence").ID
	item := createPeopleTestImage(t, database, owner.ID, spaceID, "bakery-receipt.jpg", "6")

	policy, err := database.UpdateLibraryIntelligencePolicy(ctx, owner.ID, spaceID, 0, false, false, true, true)
	if err != nil || !policy.AIEnabled || !policy.SemanticSearch || policy.QueuedAIJobs != 1 {
		t.Fatalf("enabled intelligence policy = %#v, %v", policy, err)
	}
	job, err := database.ClaimLibraryIntelligenceJob(ctx, "intelligence-test-worker", time.Minute)
	if err != nil || job == nil || job.ItemID != item.ID {
		t.Fatalf("ClaimLibraryIntelligenceJob() = %#v, %v", job, err)
	}
	vector := make([]float64, 768)
	vector[0] = 1
	metadata := json.RawMessage(`{"summary":"Bakery receipt for coffee","visible_text":"Coffee total $8.50","categories":["receipt","food"]}`)
	if err := database.CompleteLibraryIntelligenceJob(ctx, job, LibraryIntelligenceResult{Metadata: metadata, SearchText: "Bakery receipt coffee total", Embedding: vector, Model: "test-embedding", Version: 1}); err != nil {
		t.Fatalf("CompleteLibraryIntelligenceJob() error = %v", err)
	}

	lexical, err := database.SearchSpaceLibraryIntelligence(ctx, owner.ID, spaceID, "coffee receipt", nil, 10)
	if err != nil || len(lexical) != 1 || lexical[0].ID != item.ID {
		t.Fatalf("lexical intelligence search = %#v, %v", lexical, err)
	}
	semantic, err := database.SearchSpaceLibraryIntelligence(ctx, owner.ID, spaceID, "unmatched words", vector, 10)
	if err != nil || len(semantic) != 1 || semantic[0].ID != item.ID {
		t.Fatalf("semantic intelligence search = %#v, %v", semantic, err)
	}
	facets, err := database.LibraryFacets(ctx, owner.ID, spaceID, "")
	if err != nil || libraryFacetCount(facets.Utilities, "receipts") != 1 {
		t.Fatalf("receipt utility facets = %#v, %v", facets, err)
	}

	policy, err = database.UpdateLibraryIntelligencePolicy(ctx, owner.ID, spaceID, policy.Version, false, false, false, false)
	if err != nil || policy.AIEnabled || policy.SemanticSearch || policy.QueuedAIJobs != 0 {
		t.Fatalf("disabled intelligence policy = %#v, %v", policy, err)
	}
	lexical, err = database.SearchSpaceLibraryIntelligence(ctx, owner.ID, spaceID, "coffee", nil, 10)
	if err != nil || len(lexical) != 0 {
		t.Fatalf("disabled intelligence search retained documents = %#v, %v", lexical, err)
	}
}

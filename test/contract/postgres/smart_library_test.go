package db

import (
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestSmartLibraryLifecycleUsesRLSServiceContext(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Smart Library User", "smart-library@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := database.RegisterSmartLibraryFolder(user.ID, "lib_test", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.SetSmartLibraryEstimate(user.ID, folder.ID, 1); err != nil {
		t.Fatal(err)
	}
	candidate := SmartLibraryCandidate{AssetID: "asset_test", Fingerprint: "fingerprint", Extension: "jpg", SizeBytes: 10}
	ids, err := database.CreateSmartLibrarySample(user.ID, folder.ID, []SmartLibraryCandidate{candidate})
	if err != nil || len(ids) != 1 {
		t.Fatalf("CreateSmartLibrarySample() ids=%v err=%v", ids, err)
	}
	batch, err := database.CreateSmartLibraryBatch(user.ID, folder.ID, "sample", []SmartLibraryPreviewRef{{AssetID: candidate.AssetID, Fingerprint: candidate.Fingerprint}})
	if err != nil {
		t.Fatal(err)
	}
	confidence := .9
	completed, err := database.CompleteSmartLibraryBatch(user.ID, batch.ID, []SmartLibraryCompletion{{AssetID: candidate.AssetID, Description: "A concrete test image description", Tags: []string{"test", "image", "sample"}, Collections: []string{"Tests"}, Confidence: confidence, Model: "test-model"}}, map[string]string{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if completed.SuccessfulImages != 1 || completed.State != "sample_review" {
		t.Fatalf("completed folder=%+v", completed)
	}
	results, _, err := database.SmartLibraryResults(user.ID, folder.ID, 0)
	if err != nil || len(results) != 1 || results[0].AssetID != candidate.AssetID {
		t.Fatalf("SmartLibraryResults() results=%v err=%v", results, err)
	}
	hits, err := database.SearchSmartLibrary(user.ID, folder.ID, "concrete", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("SearchSmartLibrary() hits=%v err=%v", hits, err)
	}
	indexStatus, err := database.SmartLibraryIndexStatusForUser(user.ID, folder.ID, "google/gemini-embedding-2", 1)
	if err != nil || indexStatus.OutdatedAssets != 1 {
		t.Fatalf("index status=%+v err=%v", indexStatus, err)
	}
	job, err := database.PlanSmartLibraryReindex(user.ID, folder.ID, "", "google/gemini-embedding-2", 1, 100)
	if err != nil || len(job.Assets) != 1 || !job.Assets[0].RequiresPreview {
		t.Fatalf("reindex job=%+v err=%v", job, err)
	}
	var claimWG sync.WaitGroup
	claimCounts := make(chan int, 2)
	claimErrors := make(chan error, 2)
	for range 2 {
		claimWG.Add(1)
		go func() {
			defer claimWG.Done()
			_, records, claimErr := database.SmartLibraryReindexRecords(user.ID, job.ID, []SmartLibraryPreviewRef{{AssetID: candidate.AssetID, Fingerprint: candidate.Fingerprint}})
			claimCounts <- len(records)
			claimErrors <- claimErr
		}()
	}
	claimWG.Wait()
	close(claimCounts)
	close(claimErrors)
	totalClaims := 0
	for count := range claimCounts {
		totalClaims += count
	}
	for claimErr := range claimErrors {
		if claimErr != nil {
			t.Fatal(claimErr)
		}
	}
	if totalClaims != 1 {
		t.Fatalf("concurrent reindex claims=%d, want 1", totalClaims)
	}
	vector := make([]float64, 768)
	vector[0] = 1
	reindexKey := folder.ID + "\x00" + candidate.AssetID
	refreshed := SmartLibraryCompletion{
		Embedding: vector, EmbeddingInputHash: "hash", Description: "A file manager shown over prominent Pikachu artwork.",
		Tags: []string{"Pikachu", "Pokemon", "file manager", "desktop", "wallpaper"}, Collections: []string{"Character desktops"},
		Confidence: .96, Model: "test-refresh-model", Metadata: SmartLibraryRichMetadata{
			ContentType: "application screenshot", PrimarySubject: "Pikachu file manager",
			SearchTerms: []string{"Pikachu file manager", "Pokemon desktop"}, Characters: []string{"Pikachu"}, Applications: []string{"file manager"},
		},
	}
	job, err = database.CompleteSmartLibraryReindexJob(user.ID, job.ID, map[string]SmartLibraryCompletion{reindexKey: refreshed}, map[string]string{})
	if err != nil || job.Completed != 1 || job.Status != "completed" {
		t.Fatalf("completed reindex=%+v err=%v", job, err)
	}
	if _, records, replayErr := database.SmartLibraryReindexRecords(user.ID, job.ID, []SmartLibraryPreviewRef{{AssetID: candidate.AssetID, Fingerprint: candidate.Fingerprint}}); replayErr != nil || len(records) != 0 {
		t.Fatalf("completed replay records=%v err=%v", records, replayErr)
	}
	refreshedHits, refreshErr := database.SearchSmartLibrary(user.ID, folder.ID, "pikachu", 10)
	if refreshErr != nil || len(refreshedHits) != 1 || len(refreshedHits[0].Metadata.Characters) != 1 || refreshedHits[0].Metadata.Characters[0] != "Pikachu" {
		t.Fatalf("refreshed metadata hits=%+v err=%v", refreshedHits, refreshErr)
	}
	tagged, tagErr := database.SetSmartLibraryAssetTags(user.ID, folder.ID, candidate.AssetID, []string{"favorite", "Pokemon"})
	if tagErr != nil || len(tagged.Tags) != 2 || tagged.Tags[0] != "favorite" {
		t.Fatalf("updated tags=%+v err=%v", tagged, tagErr)
	}
	tagHits, tagSearchErr := database.SearchSmartLibrary(user.ID, folder.ID, "favorite", 10)
	if tagSearchErr != nil || len(tagHits) != 1 || tagHits[0].AssetID != candidate.AssetID {
		t.Fatalf("tag search hits=%+v err=%v", tagHits, tagSearchErr)
	}
	withoutFavorite, removeTagErr := database.SetSmartLibraryAssetTags(user.ID, folder.ID, candidate.AssetID, []string{"Pokemon"})
	if removeTagErr != nil || len(withoutFavorite.Tags) != 1 || withoutFavorite.Tags[0] != "Pokemon" {
		t.Fatalf("removed tag result=%+v err=%v", withoutFavorite, removeTagErr)
	}
	removedTagHits, removedTagSearchErr := database.SearchSmartLibrary(user.ID, folder.ID, "favorite", 10)
	if removedTagSearchErr != nil || len(removedTagHits) != 0 {
		t.Fatalf("removed tag remained searchable hits=%+v err=%v", removedTagHits, removedTagSearchErr)
	}
	job, err = database.CompleteSmartLibraryReindexJob(user.ID, job.ID, map[string]SmartLibraryCompletion{reindexKey: {Embedding: vector, EmbeddingInputHash: "hash"}}, map[string]string{})
	if err != nil || job.Completed != 1 {
		t.Fatalf("idempotent reindex=%+v err=%v", job, err)
	}
	hits, err = database.SearchSmartLibraryHybrid(user.ID, "", "unrelated semantic wording", vector, 10)
	if err != nil || len(hits) != 1 || hits[0].AssetID != candidate.AssetID || hits[0].SemanticScore < .99 {
		t.Fatalf("hybrid hits=%+v err=%v", hits, err)
	}
	if err = database.DeleteSmartLibraryFolder(user.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
}

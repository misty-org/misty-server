package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMediaSearchTimestampLifecycleAndTenantIsolation(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Media User", "media-search@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other User", "other-media@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	deviceID := "device_0123456789abcdef0123456789abcdef"
	otherDeviceID := "device_fedcba9876543210fedcba9876543210"
	asset := MediaSearchAsset{DeviceID: deviceID, AssetID: "media_0123456789abcdef0123456789abcdef", Fingerprint: strings.Repeat("a", 64), MediaType: "video", MimeType: "video/mp4", DurationMS: 120_186}
	claimed, err := database.ClaimMediaSearchChunk(user.ID, asset, 0, 0, 30_000)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if _, err = database.ClaimMediaSearchChunk(user.ID, asset, 0, 0, 30_000); !errors.Is(err, ErrMediaChunkBusy) {
		t.Fatalf("duplicate claim err=%v", err)
	}
	vector := make([]float64, 768)
	vector[0] = 1
	if err = database.CompleteMediaSearchChunk(user.ID, deviceID, asset.AssetID, 0, 30_000, []MediaSearchSegment{{Kind: "spoken", ChunkIndex: 0, StartMS: 1250, EndMS: 2500, Content: "hello electric world", Transcript: "hello electric world", Embedding: vector, EmbeddingModel: "test"}, {Kind: "visual", ChunkIndex: 0, StartMS: 5_000, EndMS: 15_000, Content: "red sports car on a city street", VisualDescription: "A red sports car passes buildings.", VisibleText: []string{"DOWNTOWN"}, Embedding: vector, EmbeddingModel: "test"}}); err != nil {
		t.Fatal(err)
	}
	hits, err := database.SearchMedia(user.ID, deviceID, "electric", nil, 10)
	if err != nil || len(hits) != 1 || hits[0].StartMS != 1250 || hits[0].Kind != "spoken" {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	visual, err := database.SearchMedia(user.ID, deviceID, "DOWNTOWN", nil, 10)
	if err != nil || len(visual) != 1 || visual[0].Kind != "visual" {
		t.Fatalf("visual=%+v err=%v", visual, err)
	}
	isolated, err := database.SearchMedia(other.ID, deviceID, "electric", nil, 10)
	if err != nil || len(isolated) != 0 {
		t.Fatalf("tenant leak=%+v err=%v", isolated, err)
	}
	deviceIsolated, err := database.SearchMedia(user.ID, otherDeviceID, "electric", nil, 10)
	if err != nil || len(deviceIsolated) != 0 {
		t.Fatalf("device leak=%+v err=%v", deviceIsolated, err)
	}
	claimed, err = database.ClaimMediaSearchChunk(user.ID, asset, 0, 0, 30_000)
	if err != nil || claimed {
		t.Fatalf("idempotent claim=%v err=%v", claimed, err)
	}
	deleted, err := database.DeleteMediaSearchAsset(user.ID, deviceID, asset.AssetID)
	if err != nil || !deleted {
		t.Fatalf("delete=%v err=%v", deleted, err)
	}
	hits, err = database.SearchMedia(user.ID, deviceID, "electric", nil, 10)
	if err != nil || len(hits) != 0 {
		t.Fatalf("deleted search=%+v err=%v", hits, err)
	}
}

func TestMediaSearchRequiresContiguousChunksBeforeIndexed(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Contiguous Media", "contiguous-media@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	asset := MediaSearchAsset{DeviceID: "device_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AssetID: "media_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Fingerprint: strings.Repeat("c", 64), MediaType: "audio", MimeType: "audio/mpeg", DurationMS: 60_000}
	claimed, err := database.ClaimMediaSearchChunk(user.ID, asset, 1, 30_000, 60_000)
	if err != nil || !claimed {
		t.Fatalf("claim final=%v err=%v", claimed, err)
	}
	if err = database.CompleteMediaSearchChunk(user.ID, asset.DeviceID, asset.AssetID, 1, 60_000, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := database.MediaSearchAsset(user.ID, asset.DeviceID, asset.AssetID)
	if err != nil || stored == nil || stored.Status == "indexed" || stored.IndexedThroughMS != 0 {
		t.Fatalf("out-of-order asset=%+v err=%v", stored, err)
	}
	claimed, err = database.ClaimMediaSearchChunk(user.ID, asset, 0, 0, 30_000)
	if err != nil || !claimed {
		t.Fatalf("claim first=%v err=%v", claimed, err)
	}
	if err = database.CompleteMediaSearchChunk(user.ID, asset.DeviceID, asset.AssetID, 0, 30_000, nil); err != nil {
		t.Fatal(err)
	}
	stored, err = database.MediaSearchAsset(user.ID, asset.DeviceID, asset.AssetID)
	if err != nil || stored == nil || stored.Status != "indexed" || stored.IndexedThroughMS != 60_000 {
		t.Fatalf("contiguous asset=%+v err=%v", stored, err)
	}
}

func TestMediaSearchPrunesStaleIncompleteAssets(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Media Retention", "media-retention@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	asset := MediaSearchAsset{DeviceID: "device_cccccccccccccccccccccccccccccccc", AssetID: "media_dddddddddddddddddddddddddddddddd", Fingerprint: strings.Repeat("e", 64), MediaType: "audio", MimeType: "audio/mpeg", DurationMS: 30_000}
	if claimed, claimErr := database.ClaimMediaSearchChunk(user.ID, asset, 0, 0, 30_000); claimErr != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, claimErr)
	}
	if err = database.FailMediaSearchChunk(user.ID, asset.DeviceID, asset.AssetID, 0, "test_failure"); err != nil {
		t.Fatal(err)
	}
	if err = database.TestingWithRLSContext(context.Background(), TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		_, updateErr := tx.Exec(`UPDATE media_search_assets SET updated_at=NOW()-INTERVAL '31 days' WHERE user_id=$1 AND device_id=$2 AND asset_id=$3`, user.ID, asset.DeviceID, asset.AssetID)
		return updateErr
	}); err != nil {
		t.Fatal(err)
	}
	if err = database.PruneIncompleteMediaSearchAssets(user.ID, asset.DeviceID); err != nil {
		t.Fatal(err)
	}
	stored, err := database.MediaSearchAsset(user.ID, asset.DeviceID, asset.AssetID)
	if err != nil || stored != nil {
		t.Fatalf("stale incomplete asset=%+v err=%v", stored, err)
	}
}

func TestMediaSearchLegacyCatalogCanOnlyBeAdoptedOnce(t *testing.T) {
	database := openTestDatabase(t)
	user, err := database.CreateUser("Legacy Media", "legacy-media@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	asset := MediaSearchAsset{DeviceID: LegacyMediaSearchDeviceID, AssetID: "media_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Fingerprint: strings.Repeat("f", 64), MediaType: "audio", MimeType: "audio/mpeg", DurationMS: 30_000}
	if claimed, claimErr := database.ClaimMediaSearchChunk(user.ID, asset, 0, 0, 30_000); claimErr != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, claimErr)
	}
	if err = database.CompleteMediaSearchChunk(user.ID, asset.DeviceID, asset.AssetID, 0, 30_000, []MediaSearchSegment{{Kind: "spoken", ChunkIndex: 0, StartMS: 100, EndMS: 900, Content: "legacy recording", Transcript: "legacy recording"}}); err != nil {
		t.Fatal(err)
	}
	deviceID := "device_11111111111111111111111111111111"
	ready, adopted, err := database.AdoptLegacyMediaSearchDevice(user.ID, deviceID)
	if err != nil || !ready || !adopted {
		t.Fatalf("ready=%v adopted=%v err=%v", ready, adopted, err)
	}
	hits, err := database.SearchMedia(user.ID, deviceID, "legacy", nil, 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("adopted hits=%+v err=%v", hits, err)
	}
	legacy, err := database.MediaSearchAssets(user.ID, LegacyMediaSearchDeviceID)
	if err != nil || len(legacy) != 0 {
		t.Fatalf("legacy catalog remains=%+v err=%v", legacy, err)
	}
	ready, adopted, err = database.AdoptLegacyMediaSearchDevice(user.ID, deviceID)
	if err != nil || !ready || adopted {
		t.Fatalf("idempotent ready=%v adopted=%v err=%v", ready, adopted, err)
	}
	ready, adopted, err = database.AdoptLegacyMediaSearchDevice(user.ID, "device_22222222222222222222222222222222")
	if err != nil || ready || adopted {
		t.Fatalf("second device ready=%v adopted=%v err=%v", ready, adopted, err)
	}
}

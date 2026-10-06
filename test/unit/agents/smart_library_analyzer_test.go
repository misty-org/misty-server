package agent

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestSmartLibraryAnalyzerRoutesLowConfidenceToSingleFallback(t *testing.T) {
	var mu sync.Mutex
	models := []string{}
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		model, _ := call.Body["model"].(string)
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		confidence := .2
		if model == SmartLibraryFallbackModel {
			confidence = .9
		}
		assets := []map[string]any{{
			"assetId": "asset_1", "contentType": "product photograph", "primarySubject": "blue ceramic cup",
			"description": "A blue ceramic cup on a wooden table beside a bright window.",
			"tags":        []string{"blue", "ceramic", "cup", "table", "product"}, "searchTerms": []string{"blue cup", "ceramic mug", "tabletop product", "kitchenware", "window light"},
			"entities": []string{}, "characters": []string{}, "brands": []string{}, "applications": []string{},
			"objects": []string{"cup", "table", "window"}, "scenes": []string{"tabletop"}, "activities": []string{},
			"colors": []string{"blue", "brown"}, "visibleText": []string{}, "topics": []string{"kitchenware"},
			"suggestedCollections": []string{"Product photos"}, "confidence": confidence,
		}}
		return http.StatusOK, map[string]any{"object": map[string]any{"assets": assets}, "usage": map[string]int{"inputTokens": 10, "outputTokens": 5}}
	})
	analyzer := SmartLibraryAnalyzer{Models: runtime.Client, ConfidenceThreshold: .55}
	result, err := analyzer.Analyze(context.Background(), []SmartLibraryImage{{AssetID: "asset_1", AssetKind: "image", MimeType: "image/jpeg", Bytes: []byte("preview")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].Model != SmartLibraryFallbackModel || result.Results[0].FallbackReason != "confidence_below_evaluated_threshold" {
		t.Fatalf("result=%#v", result)
	}
	if len(models) != 2 || models[0] != SmartLibraryPrimaryModel || models[1] != SmartLibraryFallbackModel {
		t.Fatalf("models=%v", models)
	}
}

func TestSmartLibraryAnalyzerNeverRoutesToPremiumModels(t *testing.T) {
	for _, model := range []string{SmartLibraryPrimaryModel, SmartLibraryFallbackModel, SmartLibraryEmbeddingModel} {
		if model == "openai/gpt-5.6-luna" || model == "openai/gpt-5.6-terra" {
			t.Fatalf("premium model used automatically: %s", model)
		}
	}
}

func TestSmartLibraryMultimodalEmbeddingSendsTheImageAnd768Dimensions(t *testing.T) {
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		images, _ := call.Body["images"].([]any)
		values, _ := call.Body["values"].([]any)
		if call.Path != "/v1/models/embed" || call.Body["model"] != SmartLibraryEmbeddingModel || call.Body["dimensions"] != float64(SmartLibraryEmbeddingDims) || len(values) != 1 || len(images) != 1 {
			t.Fatalf("call=%s %v", call.Path, call.Body)
		}
		image, _ := images[0].(map[string]any)
		if values[0] == "" || image["mediaType"] != "image/jpeg" || image["data"] == "" {
			t.Fatalf("missing multimodal content: %v", call.Body)
		}
		return http.StatusOK, map[string]any{"embeddings": [][]float64{make([]float64, SmartLibraryEmbeddingDims)}, "usage": map[string]int{"inputTokens": 266}}
	})
	analyzer := SmartLibraryAnalyzer{Models: runtime.Client}
	asset := SmartLibraryAsset{AssetID: "asset_1", AssetKind: "image", MimeType: "image/jpeg", Bytes: []byte("preview")}
	metadata := SmartLibraryMetadata{AssetID: "asset_1", PrimarySubject: "desktop", Description: "A desktop interface with mascot artwork"}
	embeddings, usage, err := analyzer.EmbedAssets(context.Background(), []SmartLibraryAsset{asset}, map[string]SmartLibraryMetadata{"asset_1": metadata})
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddings) != 1 || len(embeddings[0].Vector) != SmartLibraryEmbeddingDims || usage.InputTokens != 266 {
		t.Fatalf("embeddings=%d dims=%d usage=%+v", len(embeddings), len(embeddings[0].Vector), usage)
	}
}

func TestEmbeddingDocumentIncludesBoundedExtractedTextAndMetadata(t *testing.T) {
	asset := SmartLibraryAsset{AssetID: "asset_doc", AssetKind: "document", MimeType: "application/pdf", ExtractedText: "quarterly revenue increased due to enterprise renewals", Metadata: map[string]string{"extension": "pdf"}}
	document := TestingEmbeddingDocument(asset, SmartLibraryMetadata{AssetID: "asset_doc", Description: "A quarterly business report"})
	for _, term := range []string{"quarterly revenue", "enterprise renewals", "extension", "pdf"} {
		if !strings.Contains(document, term) {
			t.Fatalf("embedding document missing %q: %s", term, document)
		}
	}
}

func TestLegacySparseMetadataRequiresRefresh(t *testing.T) {
	legacy := SmartLibraryMetadata{
		AssetID: "asset_legacy", Description: "A dark file manager interface with a blurred background.",
		Tags: []string{"file", "manager", "dark", "interface", "desktop"}, Confidence: .98,
	}
	if !SmartLibraryMetadataNeedsRefresh(legacy) {
		t.Fatal("legacy metadata without search terms or entity categories was treated as current")
	}
	current := SmartLibraryMetadata{
		AssetID: "asset_current", ContentType: "application screenshot", PrimarySubject: "Pikachu file manager",
		Description: "A dark file manager interface displayed over prominent Pikachu artwork.",
		Tags:        []string{"Pikachu", "Pokemon", "file manager", "desktop", "wallpaper"},
		SearchTerms: []string{"Pikachu file manager", "Pokemon desktop", "yellow character", "file browser", "wallpaper"},
		Characters:  []string{"Pikachu"}, Applications: []string{"file manager"}, Objects: []string{"folders"}, Confidence: .95,
	}
	if SmartLibraryMetadataNeedsRefresh(current) {
		t.Fatal("complete current metadata was incorrectly marked stale")
	}
}

func TestBackgroundInterfaceMetadataRequiresVisualEntityAudit(t *testing.T) {
	metadata := SmartLibraryMetadata{
		AssetID: "asset_background", ContentType: "image/png", PrimarySubject: "Dark Theme File Manager Interface",
		Description:  "A file manager interface over a blurred background wallpaper.",
		Tags:         []string{"file manager", "interface", "desktop", "software", "wallpaper"},
		SearchTerms:  []string{"file manager", "dark interface", "desktop", "folders", "wallpaper"},
		Applications: []string{"file manager"}, Objects: []string{"folders"}, Scenes: []string{"desktop environment"}, Confidence: .97,
	}
	if !SmartLibraryMetadataNeedsRefresh(metadata) {
		t.Fatal("background artwork without visual entities did not request an audit")
	}
	metadata.Characters = []string{"Pikachu"}
	if SmartLibraryMetadataNeedsRefresh(metadata) {
		t.Fatal("recognized background character was still treated as stale")
	}
}

func TestSmartLibraryAssetRejectsRawPDFAndVideo(t *testing.T) {
	for _, asset := range []SmartLibraryAsset{{AssetID: "asset_pdf", AssetKind: "document", MimeType: "application/pdf", Bytes: []byte("raw")}, {AssetID: "asset_video", AssetKind: "video", MimeType: "video/mp4", Metadata: map[string]string{"extension": "mp4"}}} {
		if ValidateSmartLibraryAsset(asset) == nil {
			t.Fatalf("accepted unsafe asset: %+v", asset)
		}
	}
}

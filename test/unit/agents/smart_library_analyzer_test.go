package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	. "github.com/kannachi323/misty/server/internal/agents"
)

func TestSmartLibraryAnalyzerRoutesLowConfidenceToSingleFallback(t *testing.T) {
	var mu sync.Mutex
	models := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		model, _ := request["model"].(string)
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
		confidence := .2
		if model == SmartLibraryFallbackModel {
			confidence = .9
		}
		content, _ := json.Marshal(map[string]any{"assets": []map[string]any{{
			"assetId": "asset_1", "contentType": "product photograph", "primarySubject": "blue ceramic cup",
			"description": "A blue ceramic cup on a wooden table beside a bright window.",
			"tags":        []string{"blue", "ceramic", "cup", "table", "product"}, "searchTerms": []string{"blue cup", "ceramic mug", "tabletop product", "kitchenware", "window light"},
			"entities": []string{}, "characters": []string{}, "brands": []string{}, "applications": []string{},
			"objects": []string{"cup", "table", "window"}, "scenes": []string{"tabletop"}, "activities": []string{},
			"colors": []string{"blue", "brown"}, "visibleText": []string{}, "topics": []string{"kitchenware"},
			"suggestedCollections": []string{"Product photos"}, "confidence": confidence,
		}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": string(content)}}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5}})
	}))
	defer server.Close()
	analyzer := SmartLibraryAnalyzer{APIKey: "key", BaseURL: server.URL, ConfidenceThreshold: .55}
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

func TestSmartLibraryMultimodalEmbeddingUsesGatewayPartsAnd768Dimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var request struct {
			Model      string `json:"model"`
			Dimensions int    `json:"dimensions"`
			Input      []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != SmartLibraryEmbeddingModel || request.Dimensions != SmartLibraryEmbeddingDims || len(request.Input) != 2 || request.Input[0].Type != "text" || request.Input[1].Type != "image_url" {
			t.Fatalf("request=%+v", request)
		}
		if request.Input[0].Text == "" || request.Input[1].ImageURL.URL == "" {
			t.Fatalf("missing multimodal content: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": make([]float64, SmartLibraryEmbeddingDims)}}, "usage": map[string]any{"prompt_tokens": 266}})
	}))
	defer server.Close()
	analyzer := SmartLibraryAnalyzer{APIKey: "key", BaseURL: server.URL}
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

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestMediaSearchHandlersRequireAuthentication(t *testing.T) {
	service := NewMediaSearchService(&db.Database{}, nil)
	for name, handler := range map[string]http.HandlerFunc{"index": service.IndexChunk(), "search": service.Search(), "status": service.Status()} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/ai/media-search/"+name, strings.NewReader(`{}`))
			response := httptest.NewRecorder()
			handler(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d", response.Code)
			}
		})
	}
}
func TestMediaIndexValidationEnforcesScopeAndDuration(t *testing.T) {
	valid := TestingMediaIndexRequest{DeviceID: "device_0123456789abcdef0123456789abcdef", AssetID: "media_0123456789abcdef0123456789abcdef", Fingerprint: strings.Repeat("a", 64), MediaType: "video", MimeType: "video/mp4", DurationMS: 7_200_000, ChunkIndex: 239, StartMS: 7_170_000, EndMS: 7_200_000}
	if !TestingValidMediaIndexRequest(valid) {
		t.Fatal("valid 120-minute final chunk rejected")
	}
	tests := []TestingMediaIndexRequest{func() TestingMediaIndexRequest { v := valid; v.DurationMS++; return v }(), func() TestingMediaIndexRequest { v := valid; v.AssetID = "media_/Users/me/Movies/a.mp4"; return v }(), func() TestingMediaIndexRequest { v := valid; v.StartMS--; return v }(), func() TestingMediaIndexRequest { v := valid; v.MimeType = "audio/mpeg"; return v }()}
	for _, value := range tests {
		if TestingValidMediaIndexRequest(value) {
			t.Fatalf("accepted invalid request: %+v", value)
		}
	}
}

func TestMediaIndexValidationFoldsTinyFinalTail(t *testing.T) {
	mime, audio := "audio/mpeg", "YQ=="
	valid := TestingMediaIndexRequest{DeviceID: "device_0123456789abcdef0123456789abcdef", AssetID: "media_0123456789abcdef0123456789abcdef", Fingerprint: strings.Repeat("a", 64), MediaType: "video", MimeType: "video/mp4", DurationMS: 120_186, ChunkIndex: 3, StartMS: 90_000, EndMS: 120_186, AudioMimeType: &mime, AudioBase64: &audio}
	if !TestingValidMediaIndexRequest(valid) {
		t.Fatal("folded final chunk rejected")
	}
	invalid := valid
	invalid.ChunkIndex = 4
	invalid.StartMS = 120_000
	invalid.EndMS = 120_186
	if TestingValidMediaIndexRequest(invalid) {
		t.Fatal("tiny standalone tail accepted")
	}
}

func TestEmbedMediaTextsRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary upstream failure", http.StatusBadGateway)
			return
		}
		vector := make([]float64, serveragent.SmartLibraryEmbeddingDims)
		vector[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []any{map[string]any{"embedding": vector}},
			"usage": map[string]any{"prompt_tokens": 3},
		})
	}))
	defer gateway.Close()
	analyzer := &serveragent.SmartLibraryAnalyzer{APIKey: "test", BaseURL: gateway.URL}
	vectors, _, err := TestingEmbedMediaTexts(context.Background(), analyzer, []string{"grocery store aisle"})
	if err != nil || len(vectors) != 1 || len(vectors[0]) != serveragent.SmartLibraryEmbeddingDims {
		t.Fatalf("vectors=%d err=%v", len(vectors), err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want one absorbed retry", calls.Load())
	}
}

func TestVisualSegmentStartsAtSampledFrame(t *testing.T) {
	start, end := TestingVisualSegmentBounds(115_000, 120_186)
	if start != 115_000 || end != 120_000 {
		t.Fatalf("visual segment=%d..%d, want exact sampled-frame jump", start, end)
	}
}

func TestMediaPreviewSignatures(t *testing.T) {
	if !TestingValidJPEGPreview([]byte{0xff, 0xd8, 0x01, 0xff, 0xd9}) || TestingValidJPEGPreview([]byte("not jpeg")) {
		t.Fatal("JPEG signature validation is incorrect")
	}
	if !TestingValidMP3Preview([]byte("ID3payload")) || !TestingValidMP3Preview([]byte{0xff, 0xfb, 0x00}) || TestingValidMP3Preview([]byte("not mp3")) {
		t.Fatal("MP3 signature validation is incorrect")
	}
}

func TestMediaSemanticSearchDegradesWithoutAnalyzer(t *testing.T) {
	service := NewMediaSearchService(&db.Database{}, nil)
	if _, _, err := service.TestingCachedEmbedding(context.Background(), "user", "device_0123456789abcdef0123456789abcdef", "grocery store"); err == nil {
		t.Fatal("missing analyzer should return a controlled error")
	}
}

func TestMediaProviderGuardBoundsPerUserAndGlobalConcurrency(t *testing.T) {
	service := NewMediaSearchService(&db.Database{}, nil)
	release, allowed := service.TestingAcquireProviderSlot("same-user")
	if !allowed {
		t.Fatal("first request should be admitted")
	}
	if _, allowed = service.TestingAcquireProviderSlot("same-user"); allowed {
		t.Fatal("a second concurrent request for one user must be rejected")
	}
	releases := []func(){release}
	for index := 1; index < TestingAiGlobalMaxConcurrent; index++ {
		next, admitted := service.TestingAcquireProviderSlot("user-" + strconv.Itoa(index))
		if !admitted {
			t.Fatalf("global slot %d was unexpectedly rejected", index)
		}
		releases = append(releases, next)
	}
	if _, allowed = service.TestingAcquireProviderSlot("overflow"); allowed {
		t.Fatal("global provider concurrency limit was not enforced")
	}
	for _, done := range releases {
		done()
	}
	if done, admitted := service.TestingAcquireProviderSlot("same-user"); !admitted {
		t.Fatal("released provider slot was not reusable")
	} else {
		done()
	}
}

func TestMediaJSONLimitAccountsForBase64Expansion(t *testing.T) {
	type payload struct {
		Data string `json:"data"`
	}
	raw := `{"data":"` + strings.Repeat("a", TestingMaxAIJSONBodyBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/media", strings.NewReader(raw))
	response := httptest.NewRecorder()
	var decoded payload
	if err := TestingDecodeAIJSONWithLimit(response, request, &decoded, TestingMediaMaxJSONBytes); err != nil {
		t.Fatalf("media-sized payload was rejected: %v", err)
	}
	if len(decoded.Data) != TestingMaxAIJSONBodyBytes {
		t.Fatalf("decoded %d bytes", len(decoded.Data))
	}
}

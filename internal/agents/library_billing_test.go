package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

func TestLibraryBillsFallbackAndEmbeddingSeparately(t *testing.T) {
	for _, deny := range []int{0, 1, 2} {
		t.Run([]string{"admitted", "primary denied", "fallback denied"}[deny], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					http.Error(w, "unavailable", 503)
					return
				}
				if r.URL.Path == "/embeddings" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": make([]float64, SmartLibraryEmbeddingDims)}}, "usage": map[string]int{"prompt_tokens": 13}})
					return
				}
				metadata := SmartLibraryMetadata{AssetID: "one", ContentType: "text", PrimarySubject: "Garden plan", Description: "A detailed garden plan describing flowers and vegetable beds.", Tags: []string{"garden", "flowers", "plants", "soil", "beds"}, SearchTerms: []string{"garden plan", "flowers", "plants", "soil", "beds"}, Objects: []string{"flowers", "plants", "soil"}, Confidence: 0.9}
				body, _ := json.Marshal(map[string]any{"assets": []SmartLibraryMetadata{metadata}})
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}, "usage": map[string]any{"prompt_tokens": 123, "completion_tokens": 17, "prompt_tokens_details": map[string]int{"cached_tokens": 20}}})
			}))
			defer server.Close()
			store, adapter := &voiceBillingStore{}, &voiceBillingAdapter{deny: deny}
			base := &SmartLibraryAnalyzer{APIKey: "test", BaseURL: server.URL, Client: server.Client()}
			analyzer := base.WithBilling(&billingadapter.Service{Adapter: adapter, Store: store}, "account", "job")
			result, err := analyzer.Analyze(t.Context(), []SmartLibraryAsset{{AssetID: "one", AssetKind: "text", MimeType: "text/plain", ExtractedText: "Garden plan"}})
			if deny > 0 {
				if !errors.Is(err, billingadapter.ErrDenied) || calls != deny-1 {
					t.Fatalf("denial bypassed: calls=%d err=%v", calls, err)
				}
				_, _, _ = analyzer.Embed(t.Context(), []string{"must not run"})
				if calls != deny-1 || len(adapter.admissions) != deny {
					t.Fatal("billing failure did not stop subsequent work")
				}
				return
			}
			if err != nil || len(result.Results) != 1 {
				t.Fatalf("fallback: %v %+v", err, result)
			}
			if _, _, err = analyzer.Embed(t.Context(), []string{"Garden plan"}); err != nil {
				t.Fatal(err)
			}
			if calls != 3 || len(store.entries) != 3 || len(adapter.admissions) != 3 {
				t.Fatal("incorrect attempt count")
			}
			for i, model := range []string{SmartLibraryPrimaryModel, SmartLibraryFallbackModel, SmartLibraryEmbeddingModel} {
				r, e := adapter.admissions[i], store.entries[i]
				if r.Usage.Model != model || e.Request.Usage.Model != model && e.Action != "release" || r.AccountID != "account" || r.OperationID != "job" {
					t.Fatalf("incorrect identity: %+v %+v", r, e)
				}
				if i > 0 && r.Key == adapter.admissions[i-1].Key {
					t.Fatal("attempts share a key")
				}
			}
			if store.entries[0].Action != "release" || store.entries[1].Request.Usage.Units["input_tokens"] != 123 || store.entries[1].Request.Usage.Units["cached_input_tokens"] != 20 || store.entries[2].Request.Operation != "library.embedding" || store.entries[2].Request.Usage.Units["input_tokens"] != 13 {
				t.Fatal("mixed model usage")
			}
			if base.billing != nil {
				t.Fatal("shared analyzer acquired account state")
			}
		})
	}
}

type libraryOutageAdapter struct{ voiceBillingAdapter }

func (a *libraryOutageAdapter) Do(ctx context.Context, action string, r billingadapter.Request) (billingadapter.Decision, error) {
	if action == "settle" {
		return billingadapter.Decision{}, billingadapter.ErrUnavailable
	}
	return a.voiceBillingAdapter.Do(ctx, action, r)
}

type libraryCompletionStore struct {
	voiceBillingStore
	fail               bool
	completionCanceled bool
}

func (s *libraryCompletionStore) Enqueue(ctx context.Context, e billingadapter.Entry) error {
	s.completionCanceled = ctx.Err() != nil
	if s.fail {
		return errors.New("outbox unavailable")
	}
	return s.voiceBillingStore.Enqueue(ctx, e)
}
func TestLibraryCompletionSurvivesInterruptionAndOutage(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "adapter outage", true: "outbox failure"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store, adapter := &libraryCompletionStore{fail: fail}, &libraryOutageAdapter{}
			client := &http.Client{Transport: voiceTransport(func(*http.Request) (*http.Response, error) {
				cancel()
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":9,"completion_tokens":2}}`)), Header: make(http.Header)}, nil
			})}
			a := (&SmartLibraryAnalyzer{APIKey: "test", Client: client}).WithBilling(&billingadapter.Service{Adapter: adapter, Store: store}, "account", "job")
			var result any
			err := a.requestAt(ctx, "https://provider.test/chat/completions", map[string]any{"model": "model", "max_tokens": 10, "messages": "hello"}, nil, &result)
			if store.completionCanceled {
				t.Fatal("cancellation discarded completion")
			}
			if fail {
				if err == nil || a.BillingError() == nil {
					t.Fatal("outbox failure ignored")
				}
				return
			}
			if err != nil || len(store.entries) != 1 || store.entries[0].Request.Usage.Units["input_tokens"] != 9 {
				t.Fatalf("lost durable settlement: %v", err)
			}
		})
	}
}

func TestLibraryDisabledBillingNeedsNoStore(t *testing.T) {
	a := (&SmartLibraryAnalyzer{APIKey: "test", Client: &http.Client{Transport: voiceTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}}).WithBilling(&billingadapter.Service{Adapter: billingadapter.Disabled{}}, "account", "job")
	var result any
	if err := a.requestAt(t.Context(), "https://provider.test/chat/completions", map[string]any{"model": "model"}, nil, &result); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryTranscriptionBillsEachModelAndStopsOnDenial(t *testing.T) {
	for _, deny := range []int{0, 1, 2} {
		t.Run([]string{"admitted", "primary denied", "fallback denied"}[deny], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					http.Error(w, "unavailable", 503)
					return
				}
				_, _ = w.Write([]byte(`{"text":"Hello","durationInSeconds":2.5}`))
			}))
			defer server.Close()
			t.Setenv("AI_GATEWAY_EMBEDDING_BASE_URL", server.URL)
			t.Setenv("MEDIA_SEARCH_TRANSCRIPTION_MODEL", MediaSearchTranscriptionModel)
			t.Setenv("MEDIA_SEARCH_TRANSCRIPTION_FALLBACK_MODEL", MediaSearchTranscriptionFallbackModel)
			store, adapter := &voiceBillingStore{}, &voiceBillingAdapter{deny: deny}
			analyzer := (&SmartLibraryAnalyzer{APIKey: "test", Client: server.Client()}).WithBilling(&billingadapter.Service{Store: store, Adapter: adapter}, "account", "media-job")
			segments, _, err := analyzer.TranscribeMedia(t.Context(), []byte("audio"), "audio/mpeg", 3000)
			if deny > 0 {
				if !errors.Is(err, billingadapter.ErrDenied) || calls != deny-1 {
					t.Fatalf("denial bypassed: %d %v", calls, err)
				}
				return
			}
			if err != nil || len(segments) != 1 || calls != 2 || len(store.entries) != 2 {
				t.Fatalf("fallback: %d %v", calls, err)
			}
			if adapter.admissions[0].Usage.Model != MediaSearchTranscriptionModel || adapter.admissions[1].Usage.Model != MediaSearchTranscriptionFallbackModel {
				t.Fatal("incorrect transcription model")
			}
			if store.entries[0].Action != "release" || store.entries[1].Action != "settle" || store.entries[1].Request.Usage.Units["audio_ms"] != 2500 || store.entries[1].Request.Usage.Estimated {
				t.Fatal("incorrect transcription completion")
			}
		})
	}
}

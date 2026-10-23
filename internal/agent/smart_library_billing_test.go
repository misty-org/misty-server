package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

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
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(embeddingFixture(9))), Header: make(http.Header)}, nil
			})}
			a := (&SmartLibraryAnalyzer{Models: modelruntime.New("https://runtime.test", []byte(strings.Repeat("s", 32)), client)}).WithBilling(&billingadapter.Service{Adapter: adapter, Store: store}, "account", "job")
			_, _, err := a.Embed(ctx, []string{"hello"})
			if store.completionCanceled {
				t.Fatal("cancellation discarded completion")
			}
			if fail {
				if err == nil || a.billing.Err() == nil {
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
	client := &http.Client{Transport: voiceTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(embeddingFixture(1))), Header: make(http.Header)}, nil
	})}
	a := (&SmartLibraryAnalyzer{Models: modelruntime.New("https://runtime.test", []byte(strings.Repeat("s", 32)), client)}).WithBilling(&billingadapter.Service{Adapter: billingadapter.Disabled{}}, "account", "job")
	if _, _, err := a.Embed(t.Context(), []string{"hello"}); err != nil {
		t.Fatal(err)
	}
}

func embeddingFixture(tokens int) string {
	raw, _ := json.Marshal(map[string]any{"embeddings": [][]float64{make([]float64, SmartLibraryEmbeddingDims)}, "usage": map[string]int{"inputTokens": tokens}})
	return string(raw)
}

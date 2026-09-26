package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type voiceBillingStore struct {
	billingadapter.DurableStore
	entries []billingadapter.Entry
}

func (s *voiceBillingStore) Reservation(context.Context, billingadapter.Request) (*billingadapter.Reservation, error) {
	return nil, nil
}
func (s *voiceBillingStore) BeginAdmission(context.Context, billingadapter.Request) error { return nil }
func (s *voiceBillingStore) SaveReservation(context.Context, billingadapter.Reservation) error {
	return nil
}
func (s *voiceBillingStore) Enqueue(_ context.Context, e billingadapter.Entry) error {
	s.entries = append(s.entries, e)
	return nil
}
func (s *voiceBillingStore) Delivered(context.Context, string) error { return nil }

type voiceBillingAdapter struct {
	deny       int
	admissions []billingadapter.Request
}

func (a *voiceBillingAdapter) Enabled() bool { return true }
func (a *voiceBillingAdapter) Do(_ context.Context, action string, r billingadapter.Request) (billingadapter.Decision, error) {
	if action == "reserve" {
		a.admissions = append(a.admissions, r)
		if len(a.admissions) == a.deny {
			return billingadapter.Decision{}, billingadapter.ErrDenied
		}
	}
	return billingadapter.Decision{Allowed: true, ReservationID: r.Key}, nil
}
func TestVoiceBillingSeparatesFallbackAndDoesNotBypassDenial(t *testing.T) {
	for _, scenario := range []struct {
		name                         string
		deny, wantCalls, wantEntries int
	}{
		{"primary denied", 1, 0, 0}, {"fallback denied", 2, 1, 1}, {"fallback admitted", 0, 2, 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
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
			t.Setenv("AGENT_TRANSCRIPTION_MODEL", "openai/gpt-4o-mini-transcribe")
			store, adapter := &voiceBillingStore{}, &voiceBillingAdapter{deny: scenario.deny}
			analyzer := &SmartLibraryAnalyzer{APIKey: "test", Client: server.Client()}
			text, _, _, err := analyzer.TranscribeAgentVoiceWithBilling(t.Context(), []byte("audio"), "audio/webm", 1000, &billingadapter.Service{Store: store, Adapter: adapter}, "account")
			if scenario.deny > 0 && !errors.Is(err, billingadapter.ErrDenied) {
				t.Fatalf("lost denial: %v", err)
			}
			if scenario.deny == 0 && (err != nil || text != "Hello") {
				t.Fatalf("fallback failed: %v", err)
			}
			if calls != scenario.wantCalls || len(store.entries) != scenario.wantEntries {
				t.Fatalf("provider calls=%d completions=%d", calls, len(store.entries))
			}
			if adapter.admissions[0].Usage.Model != "openai/gpt-4o-mini-transcribe" {
				t.Fatal("primary model not reserved")
			}
			if len(adapter.admissions) == 2 && adapter.admissions[1].Usage.Model != MediaSearchTranscriptionFallbackModel {
				t.Fatal("fallback model not reserved")
			}
			if len(store.entries) > 0 && store.entries[0].Action != "release" {
				t.Fatal("failed primary not released")
			}
			if len(store.entries) == 2 {
				e := store.entries[1]
				if e.Action != "settle" || e.Request.Usage.Model != MediaSearchTranscriptionFallbackModel || e.Request.Usage.Units["audio_ms"] != 2500 {
					t.Fatal("incorrect fallback usage")
				}
			}
		})
	}
}

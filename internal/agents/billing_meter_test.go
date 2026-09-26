package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/kannachi323/misty/server/internal/billingadapter"
)

type completionStore struct {
	billingadapter.DurableStore
	reservations       []billingadapter.Reservation
	entries            []billingadapter.Entry
	account, operation string
}

func (s *completionStore) Reservations(_ context.Context, account, operation string) ([]billingadapter.Reservation, error) {
	s.account, s.operation = account, operation
	return s.reservations, nil
}
func (s *completionStore) Enqueue(_ context.Context, entry billingadapter.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

type offlineCompletionAdapter struct{ calls int }

func (a *offlineCompletionAdapter) Enabled() bool { return true }
func (a *offlineCompletionAdapter) Do(context.Context, string, billingadapter.Request) (billingadapter.Decision, error) {
	a.calls++
	return billingadapter.Decision{}, billingadapter.ErrUnavailable
}
func TestRuntimeCompletionPinsMeterAndPersistsDuringOutage(t *testing.T) {
	for _, scenario := range []struct {
		name, provider, model string
		conflict              bool
	}{
		{"same meter", "provider", "model", false},
		{"changed provider", "other", "model", true},
		{"changed model", "provider", "other", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := billingadapter.Request{Version: 1, AccountID: "account", Operation: "agent.model", OperationID: "agent-runtime:run", Key: "turn:1", Usage: billingadapter.Usage{Provider: "provider", Model: "model"}}
			second := request
			second.Key = "turn:2"
			second.Usage = billingadapter.Usage{Provider: scenario.provider, Model: scenario.model}
			store := &completionStore{reservations: []billingadapter.Reservation{{ID: "one", Admission: request}, {ID: "two", Admission: second}}}
			adapter := &offlineCompletionAdapter{}
			meter := BillingMeter{Service: &billingadapter.Service{Adapter: adapter, Store: store}}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			err := meter.CompleteRuntime(canceled, "account", "run", ModelUsage{InputTokens: 123, OutputTokens: 45})
			if scenario.conflict {
				if !errors.Is(err, billingadapter.ErrConflict) || len(store.entries) != 0 || adapter.calls != 0 {
					t.Fatalf("mixed meter escaped: %v %+v", err, store.entries)
				}
				return
			}
			if err != nil || len(store.entries) != 1 || adapter.calls != 1 {
				t.Fatalf("outage lost settlement: %v %+v", err, store.entries)
			}
			entry := store.entries[0]
			if entry.Action != "settle_group" || entry.Request.Usage.Units["input_tokens"] != 123 || len(entry.Request.ReservationIDs) != 2 || store.account != "account" || store.operation != "agent-runtime:run" {
				t.Fatalf("incorrect settlement: %+v", entry)
			}
		})
	}
}

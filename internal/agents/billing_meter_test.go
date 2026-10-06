package agent

import (
	"context"
	"errors"
	"reflect"
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
	for _, prior := range s.entries {
		if prior.ID == entry.ID {
			if !reflect.DeepEqual(prior, entry) {
				return billingadapter.ErrConflict
			}
			return nil
		}
	}
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

func (s *completionStore) RuntimeTransitions(context.Context, string, string) ([]billingadapter.Entry, error) {
	return s.entries, nil
}
func (s *completionStore) WithRuntimeBillingLock(ctx context.Context, _, _ string, f func(context.Context) error) error {
	return f(ctx)
}

func TestRuntimeTurnSettlementAndAggregateReconciliation(t *testing.T) {
	request := billingadapter.Request{Version: 1, AccountID: "account", Operation: "agent.model", OperationID: "agent-runtime:run", Key: "agent-runtime:run:model:model:1", Usage: billingadapter.Usage{Provider: "provider", Model: "model"}}
	second := request
	second.Key = "agent-runtime:run:model:model:2"
	store := &completionStore{reservations: []billingadapter.Reservation{{ID: "one", Admission: request}, {ID: "two", Admission: second}}}
	adapter := &offlineCompletionAdapter{}
	meter := BillingMeter{Service: &billingadapter.Service{Adapter: adapter, Store: store}}
	firstUsage := ModelUsage{InputTokens: 100, OutputTokens: 20, CachedInputTokens: 30, ReasoningTokens: 5}
	for range 2 {
		if err := meter.CompleteRuntimeTurn(t.Context(), "account", "run", "model:1", firstUsage); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.entries) != 1 || store.entries[0].Action != "settle" || store.entries[0].Request.ReservationID != "one" {
		t.Fatalf("missing idempotent turn receipt: %+v", store.entries)
	}
	changed := firstUsage
	changed.OutputTokens++
	if err := meter.CompleteRuntimeTurn(t.Context(), "account", "run", "model:1", changed); !errors.Is(err, billingadapter.ErrConflict) {
		t.Fatal("changed duplicate accepted", err)
	}
	if err := meter.CompleteRuntimeTurn(t.Context(), "account", "run", "model:2", ModelUsage{Estimated: true}); err != nil || len(store.entries) != 1 {
		t.Fatal("unknown usage released hold", err)
	}
	total := ModelUsage{InputTokens: 250, OutputTokens: 50, CachedInputTokens: 60, ReasoningTokens: 12}
	for range 2 {
		if err := meter.CompleteRuntime(t.Context(), "account", "run", total); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.entries) != 2 {
		t.Fatal("duplicate final receipt", len(store.entries))
	}
	final := store.entries[1].Request
	if len(final.ReservationIDs) != 1 || final.ReservationIDs[0] != "two" || final.Usage.Units["input_tokens"] != 150 || final.Usage.Units["output_tokens"] != 30 || final.Usage.Units["cached_input_tokens"] != 30 || final.Usage.Units["reasoning_tokens"] != 7 {
		t.Fatalf("aggregate double-charged turn: %+v", final)
	}
	if err := meter.CompleteRuntimeTurn(t.Context(), "account", "run", "model:2", ModelUsage{InputTokens: 150, OutputTokens: 30}); err != nil || len(store.entries) != 2 {
		t.Fatal("late checkpoint duplicated final settlement", err)
	}
}

func TestRuntimeFullyMeasuredTurnsNeedNoAggregateCharge(t *testing.T) {
	request := billingadapter.Request{Version: 1, AccountID: "account", Operation: "agent.model", OperationID: "agent-runtime:run", Key: "agent-runtime:run:model:model:1", Usage: billingadapter.Usage{Provider: "provider", Model: "model"}}
	store := &completionStore{reservations: []billingadapter.Reservation{{ID: "one", Admission: request}}}
	meter := BillingMeter{Service: &billingadapter.Service{Adapter: &offlineCompletionAdapter{}, Store: store}}
	usage := ModelUsage{InputTokens: 120, OutputTokens: 15}
	if err := meter.CompleteRuntimeTurn(t.Context(), "account", "run", "model:1", usage); err != nil {
		t.Fatal(err)
	}
	if err := meter.CompleteRuntime(t.Context(), "account", "run", usage); err != nil || len(store.entries) != 1 {
		t.Fatal("final charged measured turn again", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := meter.CompleteRuntime(canceled, "account", "run", ModelUsage{Estimated: true}); err != nil || len(store.entries) != 1 {
		t.Fatal("cancellation lost known usage", err)
	}
	if err := meter.CompleteRuntime(t.Context(), "account", "run", ModelUsage{InputTokens: 121, OutputTokens: 15}); !errors.Is(err, billingadapter.ErrConflict) {
		t.Fatal("unreserved excess aggregate accepted", err)
	}
}

func TestRuntimeAggregateOmittedDetailsDoNotContradictMeasuredTurns(t *testing.T) {
	request := billingadapter.Request{Version: 1, AccountID: "account", Operation: "agent.model", OperationID: "agent-runtime:run", Key: "agent-runtime:run:model:model:1", Usage: billingadapter.Usage{Provider: "provider", Model: "model"}}
	store := &completionStore{reservations: []billingadapter.Reservation{{ID: "one", Admission: request}}}
	meter := BillingMeter{Service: &billingadapter.Service{Adapter: &offlineCompletionAdapter{}, Store: store}}
	measured := ModelUsage{InputTokens: 120, OutputTokens: 45, CachedInputTokens: 100, ReasoningTokens: 20}
	if err := meter.CompleteRuntimeTurn(t.Context(), "account", "run", "model:1", measured); err != nil {
		t.Fatal(err)
	}
	aggregate := ModelUsage{InputTokens: 120, OutputTokens: 45, CachedInputTokensMissing: true, ReasoningTokensMissing: true}
	if err := meter.CompleteRuntime(t.Context(), "account", "run", aggregate); err != nil || len(store.entries) != 1 {
		t.Fatal("omitted aggregate details rejected or double charged", err)
	}
	aggregate.InputTokens++
	if err := meter.CompleteRuntime(t.Context(), "account", "run", aggregate); !errors.Is(err, billingadapter.ErrConflict) {
		t.Fatal("aggregate total mismatch accepted", err)
	}
	aggregate.InputTokens--
	aggregate.CachedInputTokensMissing = false
	if err := meter.CompleteRuntime(t.Context(), "account", "run", aggregate); !errors.Is(err, billingadapter.ErrConflict) {
		t.Fatal("explicit zero detail mismatch accepted", err)
	}
}

package agent

import (
	"context"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"time"
)

type runtimeBillingStore interface {
	Reservations(context.Context, string, string) ([]billingadapter.Reservation, error)
	RuntimeTransitions(context.Context, string, string) ([]billingadapter.Entry, error)
	WithRuntimeBillingLock(context.Context, string, string, func(context.Context) error) error
}

func modelUsageUnits(u ModelUsage) map[string]int64 {
	return map[string]int64{"input_tokens": u.InputTokens, "cached_input_tokens": u.CachedInputTokens, "output_tokens": u.OutputTokens, "reasoning_tokens": u.ReasoningTokens}
}

// CompleteRuntimeTurn consumes signed provider-measured counters before public
// lifecycle redaction. Unknown usage retains the hold for final reconciliation.
func (m BillingMeter) CompleteRuntimeTurn(ctx context.Context, account, run, node string, u ModelUsage) error {
	if !m.Service.Adapter.Enabled() || u.Estimated {
		return nil
	}
	return m.runtimeBilling(ctx, account, run, func(ctx context.Context, store runtimeBillingStore, group string) error {
		transitions, err := store.RuntimeTransitions(ctx, account, group)
		if err != nil {
			return err
		}
		for _, e := range transitions {
			if e.Request.Key == group+":settle" {
				return nil
			}
		}
		reservations, err := store.Reservations(ctx, account, group)
		if err != nil {
			return err
		}
		for _, r := range reservations {
			if r.Admission.Key != group+":model:"+node {
				continue
			}
			return m.Service.Complete(ctx, "settle", &r, r.Admission.Key+":settle", billingadapter.Usage{Provider: r.Admission.Usage.Provider, Model: r.Admission.Usage.Model, Units: modelUsageUnits(u)}, "")
		}
		return billingadapter.ErrConflict
	})
}

// CompleteRuntime settles only holds not already covered by durable per-turn
// receipts. Subtract their measured counters from the authoritative aggregate;
// retries use exactly the same group key and payload, even across an outage.
func (m BillingMeter) CompleteRuntime(ctx context.Context, account, run string, u ModelUsage) error {
	if !m.Service.Adapter.Enabled() {
		return nil
	}
	return m.runtimeBilling(ctx, account, run, func(ctx context.Context, store runtimeBillingStore, group string) error {
		reservations, err := store.Reservations(ctx, account, group)
		if err != nil {
			return err
		}
		if len(reservations) == 0 {
			return nil
		}
		transitions, err := store.RuntimeTransitions(ctx, account, group)
		if err != nil {
			return err
		}
		units := modelUsageUnits(u)
		settled := map[string]bool{}
		for _, e := range transitions {
			if e.Action != "settle" {
				continue
			}
			if settled[e.Request.ReservationID] {
				return billingadapter.ErrConflict
			}
			settled[e.Request.ReservationID] = true
			if !u.Estimated {
				for k, n := range e.Request.Usage.Units {
					// Some SDK aggregate results omit detail counters. Absence does not
					// mean zero and cannot contradict authoritative per-turn measurements.
					if (k == "cached_input_tokens" && u.CachedInputTokensMissing) || (k == "reasoning_tokens" && u.ReasoningTokensMissing) {
						continue
					}
					units[k] -= n
					if units[k] < 0 {
						return billingadapter.ErrConflict
					}
				}
			}
		}
		request := reservations[0].Admission
		request.Key = group + ":settle"
		request.ReservationID = ""
		request.ReservationIDs = nil
		for _, r := range reservations {
			if r.Admission.Usage.Model != request.Usage.Model || r.Admission.Usage.Provider != request.Usage.Provider {
				return billingadapter.ErrConflict
			}
			if !settled[r.ID] {
				request.ReservationIDs = append(request.ReservationIDs, r.ID)
			}
		}
		if len(request.ReservationIDs) == 0 {
			if !u.Estimated {
				for _, n := range units {
					if n != 0 {
						return billingadapter.ErrConflict
					}
				}
			}
			return nil
		}
		request.Usage.Units = units
		request.Usage.Estimated = u.Estimated
		return (billingadapter.Reliable{Adapter: m.Service.Adapter, Store: m.Service.Store}).Submit(ctx, "settle_group", request)
	})
}

func (m BillingMeter) runtimeBilling(ctx context.Context, account, run string, f func(context.Context, runtimeBillingStore, string) error) error {
	store, ok := m.Service.Store.(runtimeBillingStore)
	if !ok {
		return errors.New("billing runtime reservation store is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	group := "agent-runtime:" + run
	return store.WithRuntimeBillingLock(ctx, account, group, func(locked context.Context) error { return f(locked, store, group) })
}

package agent

import (
	"context"
	"errors"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"strings"
	"time"
)

// BillingMeter sends authenticated identity and native usage only. Private
// billing calculates prices; disabled self-hosting never calls a billing server.
type BillingMeter struct{ Service *billingadapter.Service }

func (m BillingMeter) Reserve(userID, key, meter, provider, model string, input, output int64) (*UsageReservation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	operationID := key
	if strings.HasPrefix(key, "agent-runtime:") {
		if i := strings.Index(key, ":model:"); i >= 0 {
			operationID = key[:i]
		}
	}
	r, err := m.Service.Reserve(ctx, billingadapter.Request{Version: 1, AccountID: userID, Operation: "agent.model", OperationID: operationID, Key: key, Usage: billingadapter.Usage{Provider: provider, Model: model, Units: map[string]int64{"input_tokens": input, "output_tokens": output}, Estimated: true}})
	if err != nil {
		return nil, err
	}
	return &UsageReservation{ID: r.ID, Billing: r}, nil
}

// CompleteRuntime uses the durable set of admitted model turns. Completion never
// re-admits work or depends on the availability of the billing HTTP service.
func (m BillingMeter) CompleteRuntime(ctx context.Context, accountID, runID string, u ModelUsage) error {
	if !m.Service.Adapter.Enabled() {
		return nil
	}
	store, ok := m.Service.Store.(interface {
		Reservations(context.Context, string, string) ([]billingadapter.Reservation, error)
	})
	if !ok {
		return errors.New("billing runtime reservation store is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	group := "agent-runtime:" + runID
	reservations, err := store.Reservations(ctx, accountID, group)
	if err != nil {
		return err
	}
	if len(reservations) == 0 {
		return nil
	}
	request := reservations[0].Admission
	request.Key = group + ":settle"
	request.ReservationID = ""
	for _, r := range reservations {
		if r.Admission.Usage.Model != request.Usage.Model || r.Admission.Usage.Provider != request.Usage.Provider {
			return billingadapter.ErrConflict
		}
		request.ReservationIDs = append(request.ReservationIDs, r.ID)
	}
	request.Usage.Units = map[string]int64{"input_tokens": u.InputTokens, "cached_input_tokens": u.CachedInputTokens, "output_tokens": u.OutputTokens, "reasoning_tokens": u.ReasoningTokens}
	request.Usage.Estimated = u.Estimated
	return (billingadapter.Reliable{Adapter: m.Service.Adapter, Store: m.Service.Store}).Submit(ctx, "settle_group", request)
}
func (m BillingMeter) Settle(r *UsageReservation, key, meter, provider, model string, u ModelUsage) (UsageSettlement, error) {
	if r == nil || r.Billing == nil {
		return UsageSettlement{}, billingadapter.ErrInvalid
	}
	err := m.Service.Complete(context.Background(), "settle", r.Billing, key, billingadapter.Usage{Provider: provider, Model: model, Units: map[string]int64{"input_tokens": u.InputTokens, "cached_input_tokens": u.CachedInputTokens, "output_tokens": u.OutputTokens, "reasoning_tokens": u.ReasoningTokens}, Estimated: u.Estimated || u.InputTokens == 0 && u.OutputTokens == 0}, "")
	return UsageSettlement{Settled: err == nil}, err
}
func (m BillingMeter) Release(r *UsageReservation) error {
	if r == nil || r.Billing == nil {
		return billingadapter.ErrInvalid
	}
	return m.Service.Complete(context.Background(), "release", r.Billing, r.Billing.Admission.Key+":release", billingadapter.Usage{}, "")
}
func (m BillingMeter) Refund(r *UsageReservation, key, reason string) (UsageSettlement, error) {
	if r == nil || r.Billing == nil {
		return UsageSettlement{}, billingadapter.ErrInvalid
	}
	err := m.Service.Complete(context.Background(), "refund", r.Billing, key, billingadapter.Usage{}, reason)
	return UsageSettlement{}, err
}

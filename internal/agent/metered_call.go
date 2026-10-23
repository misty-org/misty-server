package agent

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// FinishMeteredCall settles a model call's measured usage. Only a call that
// never reached a provider is released; a failure after the provider may have
// worked (a timeout, a provider error) keeps its hold for reconciliation rather
// than being given away.
func FinishMeteredCall(meter UsageMeter, r *UsageReservation, key, meterName, provider, model string, usage modelruntime.Usage, callErr error) error {
	if meter == nil || r == nil {
		return nil
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		_, err := meter.Settle(r, key, meterName, provider, model, modelUsage(usage))
		return err
	}
	if callErr != nil && NeverReachedProvider(callErr) {
		return meter.Release(r)
	}
	return nil
}

// NeverReachedProvider reports failures the runtime returned before calling a
// provider: no runtime, or a request it refused as invalid.
func NeverReachedProvider(err error) bool {
	if errors.Is(err, modelruntime.ErrUnavailable) {
		return true
	}
	var failure *modelruntime.Error
	if errors.As(err, &failure) && failure.UpstreamStatus == 0 {
		switch failure.Status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return true
		}
	}
	return false
}

// ReserveWebSearch holds one web search: the engine's requests (Serper credits) at its per-request fee.
func (m BillingMeter) ReserveWebSearch(userID, key, engine string, requests int64) (*UsageReservation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	r, err := m.Service.Reserve(ctx, billingadapter.Request{CommandID: key, Version: 1, AccountID: userID, Operation: "web.search", OperationID: key, Key: key,
		Usage: billingadapter.Usage{Provider: engine, Units: map[string]int64{"requests": max(requests, 1)}}})
	if err != nil {
		return nil, err
	}
	return &UsageReservation{ID: r.ID, Billing: r, OverLimit: r.OverLimit}, nil
}

// SettleWebSearch charges a search that returned results and releases one
// that failed; the engine bills only answered requests.
func (m BillingMeter) SettleWebSearch(r *UsageReservation, key string, requests int64, callErr error) error {
	if r == nil || r.Billing == nil {
		return billingadapter.ErrInvalid
	}
	if callErr != nil {
		return m.Release(r)
	}
	a := r.Billing.Admission
	return m.Service.Complete(context.Background(), "settle", r.Billing, key, billingadapter.Usage{Provider: a.Usage.Provider, Units: map[string]int64{"requests": min(max(requests, 1), 100)}}, "")
}

// releasableProviderFailure is a failure before any provider work: the
// process-wide ceiling refused the call, or the runtime never sent it.
func releasableProviderFailure(err error) bool {
	return errors.Is(err, ErrProviderBudgetExceeded) || errors.Is(err, ErrProviderTokenBudgetExceeded) || NeverReachedProvider(err)
}

package agent

import (
	"context"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"strings"
	"time"
)

// BillingMeter sends authenticated identity and native usage only. Private
// billing calculates prices; a disabled adapter (development) never calls it.
type BillingMeter struct{ Service *billingadapter.Service }

func (m BillingMeter) Reserve(userID, key, meter, provider, model string, input, output int64) (*UsageReservation, error) {
	return m.reserveNative(userID, key, provider, model, map[string]int64{"input_tokens": input, "output_tokens": output}, "")
}

func (m BillingMeter) ReserveMeasured(userID, key, provider, model string, units map[string]int64, commandID string) (*UsageReservation, error) {
	return m.reserveNative(userID, key, provider, model, units, commandID)
}
func (m BillingMeter) reserveNative(userID, key, provider, model string, units map[string]int64, commandID string) (*UsageReservation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	operationID := key
	if strings.HasPrefix(key, "agent-runtime:") {
		if i := strings.Index(key, ":model:"); i >= 0 {
			operationID = key[:i]
		}
	}
	if commandID == "" {
		commandID = operationID
		if i := strings.LastIndex(key, ":step:"); i >= 0 {
			commandID = key[:i]
		}
	}
	r, err := m.Service.Reserve(ctx, billingadapter.Request{CommandID: commandID, Background: strings.HasPrefix(commandID, "agent-job:"), Version: 1, AccountID: userID, Operation: "agent.model", OperationID: operationID, Key: key, Usage: billingadapter.Usage{Provider: provider, Model: model, Units: units, Estimated: true}})
	if err != nil {
		return nil, err
	}
	return &UsageReservation{ID: r.ID, Billing: r}, nil
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

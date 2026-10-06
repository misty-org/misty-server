package agent

import (
	"context"

	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/library"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func (a *SmartLibraryAnalyzer) WithBilling(service *billingadapter.Service, account, operation string) *SmartLibraryAnalyzer {
	clone := *a
	clone.account = account
	clone.billing = &library.Meter{Service: service, Account: account, OperationID: operation}
	return &clone
}

func (a *SmartLibraryAnalyzer) BillingError() error { return a.billing.Err() }

// meteredCall reserves Library billing for one model call before it starts and
// settles it with the usage the model reported. A call that reports no input
// tokens keeps its estimate; a failed call releases its hold.
func (a *SmartLibraryAnalyzer) meteredCall(ctx context.Context, operation, model string, estimate map[string]int64, call func() (modelruntime.Usage, error)) error {
	attempt, err := a.billing.Begin(ctx, operation, model, estimate)
	if err != nil {
		return err
	}
	usage, callErr := call()
	units := map[string]int64{"input_tokens": usage.InputTokens, "cached_input_tokens": usage.CachedInputTokens}
	if operation != "library.embedding" {
		units["output_tokens"] = usage.OutputTokens
	}
	if err := attempt.Finish(ctx, callErr == nil, units, usage.InputTokens <= 0); err != nil {
		return err
	}
	return callErr
}

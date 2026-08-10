package agent

import "context"

func (p *BudgetedProvider) Next(request ModelRequest) (ModelResponse, error) {
	release, err := p.acquire()
	if err != nil {
		return ModelResponse{}, err
	}
	defer release()
	response, err := p.inner.Next(request)
	p.TestingRecordSpend(response)
	return response, err
}

// recordSpend charges the tokens a completed call actually consumed.
func (p *BudgetedProvider) TestingRecordSpend(response ModelResponse) {
	tokens := response.Usage.InputTokens + response.Usage.OutputTokens + response.Usage.ReasoningTokens
	if tokens <= 0 {
		return
	}
	p.TestingMu.Lock()
	defer p.TestingMu.Unlock()
	now := p.TestingNow()
	p.tokenHour.record(now, tokens)
	p.TestingTokenDay.record(now, tokens)
	p.spentTokens += tokens
}

// SpentTokens reports billable tokens observed since start, for monitoring.
func (p *BudgetedProvider) SpentTokens() int64 {
	p.TestingMu.Lock()
	defer p.TestingMu.Unlock()
	return p.spentTokens
}

// NextContext preserves cancellation for providers that support it.
func (p *BudgetedProvider) NextContext(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	release, err := p.acquire()
	if err != nil {
		return ModelResponse{}, err
	}
	defer release()
	if contextual, ok := p.inner.(ContextModelProvider); ok {
		response, err := contextual.NextContext(ctx, request)
		p.TestingRecordSpend(response)
		return response, err
	}
	response, err := p.inner.Next(request)
	p.TestingRecordSpend(response)
	return response, err
}

// ProviderName and ModelName pass through so status reporting is unchanged.
func (p *BudgetedProvider) ProviderName() string {
	if named, ok := p.inner.(interface{ ProviderName() string }); ok {
		return named.ProviderName()
	}
	return ""
}

func (p *BudgetedProvider) ModelName() string {
	if named, ok := p.inner.(interface{ ModelName() string }); ok {
		return named.ModelName()
	}
	return ""
}

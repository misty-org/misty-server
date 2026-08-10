package agent

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

type AgentTier string

const (
	TierLow  AgentTier = "tier-low"
	TierMed  AgentTier = "tier-med"
	TierHigh AgentTier = "tier-high"
)

func NormalizeAgentTier(value AgentTier) AgentTier {
	switch AgentTier(strings.ToLower(strings.TrimSpace(string(value)))) {
	case TierMed:
		return TierMed
	case TierHigh:
		return TierHigh
	default:
		return TierLow
	}
}

func (tier AgentTier) DisplayName() string {
	return InitialSelectedModelName
}

type AgentProviderRouter struct {
	providers map[AgentTier]ModelProvider
}

func NewAgentProviderRouter(low, med, high ModelProvider) *AgentProviderRouter {
	if low == nil {
		low = MockProvider{}
	}
	if med == nil {
		med = low
	}
	if high == nil {
		high = med
	}
	return &AgentProviderRouter{providers: map[AgentTier]ModelProvider{
		TierLow: low, TierMed: med, TierHigh: high,
	}}
}

func (router *AgentProviderRouter) ProviderForTier(tier AgentTier) ModelProvider {
	if router == nil {
		return MockProvider{}
	}
	provider := router.providers[NormalizeAgentTier(tier)]
	if provider == nil {
		return MockProvider{}
	}
	return provider
}

func (router *AgentProviderRouter) Next(request ModelRequest) (ModelResponse, error) {
	return router.ProviderForTier(request.AgentTier).Next(request)
}

func (router *AgentProviderRouter) NextContext(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	return nextProvider(ctx, router.ProviderForTier(request.AgentTier), request)
}

// ProviderInfo on the router is deliberately provider-neutral. Internal callers
// that need billing metadata resolve the concrete provider with ProviderForTier.
func (*AgentProviderRouter) ProviderName() string { return "misty" }
func (*AgentProviderRouter) ModelName() string    { return InitialSelectedModelID }

type agentProviderResolver interface {
	ProviderForTier(AgentTier) ModelProvider
}

func TestingResolveAgentProvider(provider ModelProvider, tier AgentTier) ModelProvider {
	if resolver, ok := provider.(agentProviderResolver); ok {
		return resolver.ProviderForTier(tier)
	}
	return provider
}

func nextProvider(ctx context.Context, provider ModelProvider, request ModelRequest) (ModelResponse, error) {
	if resolver, ok := provider.(agentProviderResolver); ok {
		provider = resolver.ProviderForTier(request.AgentTier)
	}
	var response ModelResponse
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		response, err = callProvider(ctx, provider, request)
		if err == nil || ctx.Err() != nil || !transientProviderError(err) || attempt == 1 {
			return response, err
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ModelResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	return response, err
}

func callProvider(ctx context.Context, provider ModelProvider, request ModelRequest) (ModelResponse, error) {
	if contextual, ok := provider.(ContextModelProvider); ok {
		return contextual.NextContext(ctx, request)
	}
	return provider.Next(request)
}

func transientProviderError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

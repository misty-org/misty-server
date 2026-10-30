package agent

import (
	"context"
	"strings"

	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func (s *Service) SetModelResolver(resolve aimodels.Resolver) { s.modelResolver = resolve }

func (a *SmartLibraryAnalyzer) WithAIAccount(account string) *SmartLibraryAnalyzer {
	clone := *a
	clone.account = account
	return &clone
}

func (a *SmartLibraryAnalyzer) roleConfig(ctx context.Context, role string) (*aimodels.Resolved, error) {
	if a.ModelResolver == nil || a.account == "" {
		return nil, nil
	}
	if c, ok := a.roleConfigs[role]; ok {
		return c, nil
	}
	return a.ModelResolver(ctx, a.account, role)
}

// embeddingRoute is where this account's embeddings go and the model they use.
func (a *SmartLibraryAnalyzer) embeddingRoute(ctx context.Context) (modelruntime.Route, string, error) {
	c, err := a.roleConfig(ctx, "embedding")
	if err != nil {
		return modelruntime.Route{}, "", err
	}
	if c != nil {
		return modelruntime.For(c), c.Model, nil
	}
	return modelruntime.Instance(), a.embeddingModel(), nil
}

func (a *SmartLibraryAnalyzer) AccountEmbeddingModel(ctx context.Context) (string, error) {
	c, err := a.roleConfig(ctx, "embedding")
	if err != nil {
		return "", err
	}
	if c != nil {
		return c.Model, nil
	}
	return a.embeddingModel(), nil
}

func accountCompletionProvider(models *modelruntime.Client, c *aimodels.Resolved) (ModelProvider, error) {
	if !models.Enabled() {
		return nil, modelruntime.ErrUnavailable
	}
	return NewBudgetedProvider(NewRuntimeProvider(models, modelruntime.For(c), c.Model, modelProviderName(c.Model), c.Reasoning), ProviderBudgetFromEnv()), nil
}

// modelProviderName labels a model by its provider prefix.
func modelProviderName(model string) string {
	if provider, _, ok := strings.Cut(model, "/"); ok && provider != "" {
		return provider
	}
	return ProviderVercelAI
}

// oneShotRoute is how one of the service's own small calls for role runs for
// user: on the account's model and key, else fallback on the operator's keys.
func (s *Service) oneShotRoute(ctx context.Context, user, role, fallback string) (modelruntime.Route, string, error) {
	if s.modelResolver != nil {
		c, err := s.modelResolver(ctx, user, role)
		if err != nil {
			return modelruntime.Route{}, "", err
		}
		if c != nil {
			return modelruntime.For(c), c.Model, nil
		}
	}
	return modelruntime.Instance(), fallback, nil
}

// ConfiguredRealtime applies the account's Speaking choice to the voice socket.
func (a *SmartLibraryAnalyzer) ConfiguredRealtime(ctx context.Context, account string) (*SmartLibraryAnalyzer, bool, error) {
	scoped := a.WithAIAccount(account)
	config, err := scoped.roleConfig(ctx, "realtime")
	if err != nil {
		return nil, false, err
	}
	clone := *scoped
	clone.realtimeConfig = config
	return &clone, config != nil, nil
}
func (a *SmartLibraryAnalyzer) selectedRealtimeModel() string {
	if a.realtimeConfig != nil {
		return a.realtimeConfig.Model
	}
	return AgentRealtimeModel
}

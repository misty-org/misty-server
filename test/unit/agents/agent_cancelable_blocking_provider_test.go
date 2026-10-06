package agent

import (
	"context"
	"sync"

	. "github.com/kannachi323/misty/server/internal/agents"
)

type cancelableBlockingProvider struct {
	once    sync.Once
	entered chan struct{}
}

func (provider *cancelableBlockingProvider) ProviderName() string { return ProviderVercelAI }

func (provider *cancelableBlockingProvider) ModelName() string { return "private-model" }

func (provider *cancelableBlockingProvider) Next(request ModelRequest) (ModelResponse, error) {
	return provider.NextContext(context.Background(), request)
}

func (provider *cancelableBlockingProvider) NextContext(ctx context.Context, _ ModelRequest) (ModelResponse, error) {
	provider.once.Do(func() { close(provider.entered) })
	<-ctx.Done()
	return ModelResponse{}, ctx.Err()
}

type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (provider *blockingProvider) Next(ModelRequest) (ModelResponse, error) {
	provider.entered <- struct{}{}
	<-provider.release
	return ModelResponse{Text: "done"}, nil
}

package agent

import (
	"context"
	"errors"
	"net"
	"time"
)

func nextProvider(ctx context.Context, provider ModelProvider, request ModelRequest) (ModelResponse, error) {
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

package api

import (
	"context"
	"net/http"
	"time"
)

func serveInvocationStream(ctx context.Context, w http.ResponseWriter, stream *invocationStream, cursor int64, keepalive time.Duration) {
	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()
	if err := writeAIInvocationSSE(w, ""); err != nil {
		return
	}
	for ctx.Err() == nil {
		page, err := stream.read(ctx, cursor)
		if err != nil {
			return
		}
		if page.ready {
			cursor = page.cursor
		}
		for _, event := range page.events {
			if err := writeAIInvocationSSE(w, string(event.frame)); err != nil {
				return
			}
			cursor = event.sequence
		}
		if page.ready && cursor >= page.head && aiInvocationTerminal(page.state) {
			return
		}
		if len(page.events) > 0 {
			continue
		}
	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case <-page.notify:
				break wait
			case <-ticker.C:
				// Transport maintenance never reads invocation state or event history.
				if err := writeAIInvocationSSE(w, ": keep-alive\n\n"); err != nil {
					return
				}
			}
		}
	}
}

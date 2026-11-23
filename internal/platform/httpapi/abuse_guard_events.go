package api

import (
	"context"
	"log"
	"time"

	"github.com/misty-org/misty-server/internal/platform/workqueue"
)

type abuseBlockEvents interface {
	SubscribeAbuseBlockEvents(context.Context) (<-chan struct{}, func(), error)
}

func (g *AbuseGuard) Run(ctx context.Context, observe func(string)) {
	defer g.Close()
	source, ok := g.store.(abuseBlockEvents)
	if !ok {
		return
	}
	var nextRead time.Time
	queue := workqueue.Queue{
		Subscribe: source.SubscribeAbuseBlockEvents,
		Observe:   observe,
		Next: func(ctx context.Context) (time.Duration, bool, error) {
			if delay := time.Until(nextRead); delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return 0, false, ctx.Err()
				case <-timer.C:
				}
			}
			nextRead = time.Now().Add(100 * time.Millisecond)
			read, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return 0, false, g.refresh(read)
		},
		OnError: func(err error) { log.Printf("abuse block refresh: %v", err) },
	}
	queue.Run(ctx)
}

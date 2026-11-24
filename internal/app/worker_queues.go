package app

import (
	"context"
	"log"
	"time"

	"github.com/misty-org/misty-server/internal/platform/workqueue"
)

func runDatabaseQueue(ctx context.Context, server *Server, kind string, process func(context.Context) (int, error)) {
	queue := workqueue.Queue{
		Subscribe: func(ctx context.Context) (<-chan struct{}, func(), error) {
			return server.Database.SubscribeWorkerEvents(ctx, kind)
		},
		Next: func(ctx context.Context) (timeDelay time.Duration, pending bool, err error) {
			return server.Database.NextWorkerDelay(ctx, kind)
		},
		Process: process,
		Observe: func(reason string) {
			if server.Metrics != nil {
				server.Metrics.RecordWorkerWake(kind, reason)
			}
		},
		OnError: func(err error) { log.Printf("%s worker: %v", kind, err) },
	}
	queue.Run(ctx)
}

package app

import (
	"context"
	"log"
	"sync"
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

func runNoteControlProcessing(ctx context.Context, server *Server) {
	if server.Spaces == nil {
		return
	}
	var workers sync.WaitGroup
	queues := map[string]func(context.Context) (int, error){
		"note-control": func(ctx context.Context) (int, error) { return server.Spaces.ProcessNoteControlCommands(ctx, 50) },
		"code-session-control": func(ctx context.Context) (int, error) {
			return server.Spaces.ProcessCodeSessionControlCommands(ctx, 50)
		},
		"room-control": func(ctx context.Context) (int, error) {
			return server.Spaces.ProcessCollabRoomControlCommands(ctx, 50)
		},
	}
	for kind, process := range queues {
		workers.Add(1)
		go func() { defer workers.Done(); runDatabaseQueue(ctx, server, kind, process) }()
	}
	workers.Wait()
}

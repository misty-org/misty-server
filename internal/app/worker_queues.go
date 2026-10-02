package app

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/platform/workqueue"
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
		"note-control":    func(ctx context.Context) (int, error) { return server.Spaces.ProcessNoteControlCommands(ctx, 50) },
		"drawing-control": func(ctx context.Context) (int, error) { return server.Spaces.ProcessDrawingControlCommands(ctx, 50) },
		"drawing-purge": func(ctx context.Context) (int, error) {
			n, err := server.Database.PurgeDeletedDrawings(ctx, 100)
			return int(n), err
		},
	}
	for kind, process := range queues {
		workers.Add(1)
		go func() { defer workers.Done(); runDatabaseQueue(ctx, server, kind, process) }()
	}
	workers.Wait()
}

func runLibraryIntelligenceProcessing(ctx context.Context, server *Server) {
	if server.Library == nil || !server.Library.WorkerEnabled("ai") {
		return
	}
	worker := "intelligence-worker-" + uuid.NewString()
	runDatabaseQueue(ctx, server, "library-ai", func(ctx context.Context) (int, error) { return server.Library.ProcessIntelligenceJobs(ctx, worker, 2) })
}
func runLibraryRenditionProcessing(ctx context.Context, server *Server) {
	if server.Library == nil || !server.Library.WorkerEnabled("edit") {
		return
	}
	worker := "rendition-worker-" + uuid.NewString()
	runDatabaseQueue(ctx, server, "library-edit", func(ctx context.Context) (int, error) { return server.Library.ProcessRenditionJobs(ctx, worker, 2) })
}
func runLibraryPeopleProcessing(ctx context.Context, server *Server) {
	if server.Library == nil || !server.Library.WorkerEnabled("faces") {
		return
	}
	worker := "people-worker-" + uuid.NewString()
	runDatabaseQueue(ctx, server, "library-faces", func(ctx context.Context) (int, error) { return server.Library.ProcessPeopleJobs(ctx, worker, 4) })
}

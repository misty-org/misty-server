package app

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/email"

	"github.com/google/uuid"
)

func Run() {
	runtimeConfig := config.LoadRuntime()

	server, err := CreateServer()
	if err != nil {
		panic(err)
	}

	if err := server.Database.Start(); err != nil {
		panic(err)
	}
	defer server.Database.Stop()
	if err := server.MountHandlers(); err != nil {
		panic(err)
	}
	if err := server.StartRealtime(); err != nil {
		panic(err)
	}
	defer server.Realtime.Close()
	workerContext, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	startWorkers(
		workerContext,
		WorkerFunc(func(ctx context.Context) {
			if server.AbuseGuard != nil {
				server.AbuseGuard.Run(ctx, func(reason string) { server.Metrics.RecordWorkerWake("abuse-blocks", reason) })
			}
		}),
		WorkerFunc(func(ctx context.Context) {
			runDatabaseQueue(ctx, server, "abuse-retention", func(ctx context.Context) (int, error) { return server.Database.PurgeExpiredAbuseBlocks(ctx, 250) })
		}),
		WorkerFunc(func(ctx context.Context) { runLifecycleQueues(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runRetention(ctx, server) }),
		WorkerFunc(func(ctx context.Context) {
			if server.BrowserSync != nil {
				// Hijacked sockets outlive the HTTP server's own shutdown.
				<-ctx.Done()
				server.BrowserSync.Shutdown()
			}
		}),
		WorkerFunc(func(ctx context.Context) { runPersonalAgentTaskProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryRenditionProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runNoteControlProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { email.Run(ctx, server.Database) }),
	)
	// Domain gauges refresh on their own schedule so a scrape never holds a
	// database connection.
	if server.Metrics != nil {
		server.Metrics.StartSampling(workerContext, 15*time.Second)
	}

	log.Printf("Misty server running on :%s", runtimeConfig.Port)
	if err := runHTTPServer(NewHTTPServer(":"+runtimeConfig.Port, server.Router), stopWorkers); err != nil {
		panic(err)
	}
	log.Println("Misty server stopped")
}

// The agent dispatcher runs as two durable queues woken by committed hints and
// their earliest real deadline: invocation runtime deliveries and Space runs.
func runPersonalAgentTaskProcessing(ctx context.Context, server *Server) {
	if server.Spaces == nil {
		return
	}
	workerID := "personal-agent-task-worker-" + uuid.NewString()
	var workers sync.WaitGroup
	if server.Spaces.AgentRuntimeDeliveriesEnabled() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runDatabaseQueue(ctx, server, "agent-runtime", func(ctx context.Context) (int, error) {
				return server.Spaces.ProcessAgentRuntimeDeliveries(ctx, 2)
			})
		}()
	}
	runDatabaseQueue(ctx, server, "agent-tasks", func(ctx context.Context) (int, error) {
		return server.Spaces.ProcessPersonalAgentTasks(ctx, workerID, 2)
	})
	workers.Wait()
}

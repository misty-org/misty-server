package app

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/google/uuid"
)

func Run() {
	runtimeConfig := envconfig.LoadRuntime()

	server, err := CreateServer()
	if err != nil {
		panic(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		server.Telemetry.Close(ctx)
	}()
	aiProvider, aiModel := server.AIAgent.ProviderStatus()
	log.Printf("MistyAI provider: %s (%s)", aiProvider, aiModel)

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
		WorkerFunc(func(ctx context.Context) { runBillingCompletions(ctx, server) }),
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
				server.BrowserSync.RunLiveness(ctx)
			}
		}),
		WorkerFunc(func(ctx context.Context) { runPersonalAgentTaskProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryPeopleProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryRenditionProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryIntelligenceProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runNoteControlProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runAIEmbeddingProcessing(ctx, server) }),
	)
	// Domain gauges refresh on their own schedule so a scrape never holds a
	// database connection.
	if server.Metrics != nil {
		server.Metrics.StartSampling(workerContext, 15*time.Second)
	}

	log.Printf("Misty server running on :%s", runtimeConfig.Port)
	if err := runHTTPServer(TestingNewHTTPServer(":"+runtimeConfig.Port, server.Router), stopWorkers); err != nil {
		panic(err)
	}
	log.Println("Misty server stopped")
}

func runAIEmbeddingProcessing(ctx context.Context, server *Server) {
	if !server.AIAnalyzer.Available() {
		return
	}
	runDatabaseQueue(ctx, server, "embedding", func(ctx context.Context) (int, error) {
		processed := 0
		// Claim immediately before each provider call; queued chunks cannot outlive
		// their leases while an earlier chunk is processed.
		for processed < 32 && ctx.Err() == nil {
			chunks, err := server.Database.PendingAIEmbeddingChunks(ctx, 1)
			if err != nil {
				return processed, err
			}
			if len(chunks) == 0 {
				return processed, nil
			}
			chunk := chunks[0]
			processed++
			if err := embedRetrievalChunk(ctx, server, chunk); err != nil {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				retryErr := server.Database.RetryAIEmbeddingChunk(cleanup, chunk)
				cancel()
				return processed, errors.Join(err, retryErr)
			}
		}
		return processed, ctx.Err()
	})
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

package app

import (
	"context"
	"log"
	"time"

	"github.com/misty-org/misty-server/internal/platform/config"
	"github.com/misty-org/misty-server/internal/platform/email"
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

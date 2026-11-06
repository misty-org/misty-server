package app

import (
	"context"
	"errors"
	"sync"
	"time"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// Scheduled runs and lifecycle steps wake on committed hints and their earliest
// database deadline. Each queue starts only when its owning service exists, so
// a disabled service can never leave due work that its queue cannot clear.
func runLifecycleQueues(ctx context.Context, server *Server) {
	queues := map[string]func(context.Context) (int, error){
		"ai-cleanup": func(ctx context.Context) (int, error) { return server.Database.ProcessAICleanupJobs(ctx, 25) },
	}
	if server.AI != nil {
		queues["scheduled"] = func(ctx context.Context) (int, error) {
			now := time.Now().UTC()
			recaps, err := server.AI.ProcessDueAIRecaps(ctx, now, 25)
			if err != nil {
				return recaps, err
			}
			workflows, err := server.AI.ProcessDueWorkflowSchedules(ctx, now, 25)
			return recaps + workflows, err
		}
	}
	if server.AI != nil {
		queues["inbound-tasks"] = func(ctx context.Context) (int, error) {
			return server.AI.ProcessInboundTasks(ctx, time.Now().UTC(), 25)
		}
	}
	if server.Spaces != nil {
		closings := api.NewAccountClosings(server.Database)
		queues["account-deletion"] = func(ctx context.Context) (int, error) {
			// A failing closed-account step must not hold up a person's own
			// deletion, so every step runs.
			closed, closeErr := closings.Process(ctx, 25)
			processed, err := server.Spaces.ProcessAccountDeletions(ctx, 10)
			if err != nil {
				return closed + processed, errors.Join(closeErr, err)
			}
			purged, err := server.Spaces.PurgeDueAccountDeletions(ctx, 25)
			return closed + processed + purged, errors.Join(closeErr, err)
		}
	}
	if server.Library != nil {
		queues["rendition-reservations"] = func(ctx context.Context) (int, error) {
			return server.Database.ReleaseExpiredLibraryRenditionReservations(ctx, 100)
		}
	}
	var workers sync.WaitGroup
	for kind, process := range queues {
		workers.Add(1)
		go func() { defer workers.Done(); runDatabaseQueue(ctx, server, kind, process) }()
	}
	workers.Wait()
}

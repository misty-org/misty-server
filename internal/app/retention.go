package app

import (
	"context"
	"errors"
	"log"
	"math/rand/v2"
	"time"
)

// Retention purges are maintenance, not user-visible state changes: expired
// rows are already excluded by their readers. They run on an explicit budget
// rather than per-row deadlines, because inserts into these high-churn tables
// (messages, tickets, events) must not publish notifications. One replica owns
// each pass and drains bounded batches; the others skip it.
const (
	retentionInterval = 10 * time.Minute
	retentionBatches  = 20
)

type retentionTask struct {
	name  string
	limit int
	run   func(context.Context) (int, error)
}

func runRetention(ctx context.Context, server *Server) {
	for {
		// Full jitter spreads replicas and restarts across the interval.
		timer := time.NewTimer(retentionInterval/2 + time.Duration(rand.Int64N(int64(retentionInterval))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		owned, err := server.Database.WithSessionOwnership(ctx, "retention", func(ctx context.Context) error {
			return runRetentionPass(ctx, retentionTasks(server))
		})
		if ctx.Err() != nil {
			return
		}
		reason := "deadline"
		if !owned {
			reason = "contention"
		}
		if err != nil {
			reason = "error"
			log.Printf("retention pass: %v", err)
		}
		if server.Metrics != nil {
			server.Metrics.RecordWorkerWake("retention", reason)
		}
	}
}

// runRetentionPass drains each task while it returns full batches, up to a
// fixed number of batches so a large backlog cannot monopolize the pass.
func runRetentionPass(ctx context.Context, tasks []retentionTask) error {
	var failures []error
	for _, task := range tasks {
		for batch := 0; batch < retentionBatches && ctx.Err() == nil; batch++ {
			n, err := task.run(ctx)
			if err != nil {
				failures = append(failures, errors.New(task.name+": "+err.Error()))
				break
			}
			if task.limit <= 0 || n < task.limit {
				break
			}
		}
	}
	return errors.Join(failures...)
}

func retentionTasks(server *Server) []retentionTask {
	tasks := []retentionTask{
		{"ai-transients", 250, func(ctx context.Context) (int, error) {
			n, err := server.Database.PurgeExpiredAITransients(ctx, 250)
			return int(n), err
		}},
		{"library-expired", 100, func(ctx context.Context) (int, error) { return server.CleanupExpiredLibraryData(ctx, 100) }},
		{"journal-assets", 100, func(ctx context.Context) (int, error) {
			return server.CleanupExpiredJournalAssets(ctx, 24*time.Hour, 100)
		}},
		{"notes", 100, func(ctx context.Context) (int, error) {
			n, err := server.Database.PurgeExpiredNotes(ctx, 100)
			return int(n), err
		}},
		// Five tables share one call; a full batch from any of them drains again.
		{"space-data", 1000, func(ctx context.Context) (int, error) {
			n, err := server.Database.PurgeExpiredSpaceData(ctx, 1000)
			return int(n), err
		}},
	}
	if server.Library != nil {
		tasks = append(tasks,
			retentionTask{"renditions", 20, func(ctx context.Context) (int, error) { return server.Library.PurgeExpiredRenditions(ctx, 20) }},
			// Object storage offers no change stream; the inventory comparison is
			// the budgeted exception. It runs once per pass, not drained.
			retentionTask{"library-objects", 0, func(ctx context.Context) (int, error) {
				report, err := server.Library.ReconcileLibraryObjects(ctx, 24*time.Hour, 250)
				if err == nil && (report.OrphanObjectsDeleted > 0 || report.MissingPermanentObjects > 0 ||
					report.MismatchedObjects > 0 || report.InterruptedFinalizations > 0) {
					log.Printf("Library R2 reconciliation: checked=%d expired=%d orphan_deleted=%d missing=%d mismatched=%d retryable_finalizations=%d",
						report.InventoryObjectsChecked, report.ExpiredUploads, report.OrphanObjectsDeleted,
						report.MissingPermanentObjects, report.MismatchedObjects, report.InterruptedFinalizations)
				}
				return 0, err
			}},
		)
	}
	return tasks
}

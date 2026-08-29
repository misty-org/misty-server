package app

import (
	"context"
	"log"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
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
	if !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_DEPLOYMENT_MODE")), "self_hosted") {
		if err := server.Database.ConfigureCanonicalMistySpace(
			context.Background(),
			strings.TrimSpace(envconfig.Getenv("MISTY_OPERATOR_USER_ID")),
		); err != nil {
			panic(err)
		}
	}
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
		WorkerFunc(func(ctx context.Context) { runAgentRetention(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runPersonalAgentTaskProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryPeopleProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryRenditionProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runLibraryIntelligenceProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runNoteControlProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runActionSuggestionProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runSocialDeliveryProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { server.Spaces.RunDiscordSocialGateway(ctx) }),
		WorkerFunc(func(ctx context.Context) { runAIEmbeddingProcessing(ctx, server) }),
		WorkerFunc(func(ctx context.Context) { runSubscriptionReconciliation(ctx, server) }),
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

func runSocialDeliveryProcessing(ctx context.Context, server *Server) {
	if server.Spaces == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Spaces.ProcessSocialAutomations(ctx, 10); err != nil {
				log.Printf("Social automation processing failed: %v", err)
			}
			if _, err := server.Spaces.ProcessSocialDelivery(ctx, 20); err != nil {
				log.Printf("Social delivery processing failed: %v", err)
			}
		}
	}
}

func runAIEmbeddingProcessing(ctx context.Context, server *Server) {
	if server.AIAnalyzer == nil || strings.TrimSpace(server.AIAnalyzer.APIKey) == "" {
		return
	}
	process := func() {
		chunks, err := server.Database.PendingAIEmbeddingChunks(ctx, 32)
		if err != nil || len(chunks) == 0 {
			if err != nil {
				log.Printf("AI retrieval embedding scan failed: %v", err)
			}
			return
		}
		inputs := make([]string, len(chunks))
		for index, chunk := range chunks {
			inputs[index] = chunk.Content
		}
		vectors, _, err := server.AIAnalyzer.Embed(ctx, inputs)
		if err != nil || len(vectors) != len(chunks) {
			if err != nil {
				log.Printf("AI retrieval embedding failed: %v", err)
			}
			return
		}
		for index, chunk := range chunks {
			if err := server.Database.CompleteAIEmbeddingChunk(ctx, chunk, vectors[index], serveragent.SmartLibraryEmbeddingModel); err != nil {
				log.Printf("AI retrieval embedding write failed: %v", err)
			}
		}
	}
	process()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			process()
		}
	}
}

func runActionSuggestionProcessing(ctx context.Context, server *Server) {
	if server.Spaces == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Spaces.ProcessActionSuggestionJobs(ctx, 4); err != nil {
				log.Printf("Action suggestion processing failed: %v", err)
			}
			if _, err := server.Spaces.ProcessConversationFollowUps(ctx, 10); err != nil {
				log.Printf("Conversation follow-up processing failed: %v", err)
			}
		}
	}
}

func runPersonalAgentTaskProcessing(ctx context.Context, server *Server) {
	if server.Spaces == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	workerID := "personal-agent-task-worker-" + uuid.NewString()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Spaces.ProcessAssignedPersonalAgentRuns(ctx, workerID, 2); err != nil {
				log.Printf("Personal Agent Task processing failed: %v", err)
			}
		}
	}
}

func runSubscriptionReconciliation(ctx context.Context, server *Server) {
	if strings.TrimSpace(envconfig.Getenv("STRIPE_SECRET_KEY")) == "" {
		return
	}
	service := appbilling.NewStripeService(
		server.Database,
		appbilling.WithTelemetry(server.Telemetry),
	)
	reconcile := func() {
		report, err := service.ReconcileSubscriptions(ctx, time.Now().UTC(), 100)
		if err != nil {
			log.Printf("Stripe subscription reconciliation failed: %v", err)
			return
		}
		if report.Failed > 0 || report.EntitlementsExpired > 0 {
			log.Printf(
				"Stripe subscription reconciliation: checked=%d updated=%d failed=%d stale_entitlements_expired=%d",
				report.Checked,
				report.Updated,
				report.Failed,
				report.EntitlementsExpired,
			)
		}
	}
	reconcile()
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func runNoteControlProcessing(ctx context.Context, server *Server) {
	if server.Spaces == nil {
		return
	}
	log.Printf("Journal collaboration command processing enabled for %s", server.Spaces.JournalCollab().Host)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Spaces.ProcessNoteControlCommands(ctx, 50); err != nil {
				log.Printf("Journal note collaboration command processing failed: %v", err)
			}
			if _, err := server.Spaces.ProcessDrawingControlCommands(ctx, 50); err != nil {
				log.Printf("Drawing collaboration command processing failed: %v", err)
			}
			if _, err := server.Database.PurgeDeletedDrawings(ctx, 100); err != nil {
				log.Printf("Drawing retention purge failed: %v", err)
			}
		}
	}
}

func runLibraryIntelligenceProcessing(ctx context.Context, server *Server) {
	if server.Library == nil {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	workerID := "intelligence-worker-" + uuid.NewString()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Library.ProcessIntelligenceJobs(ctx, workerID, 2); err != nil {
				log.Printf("Library intelligence processing failed: %v", err)
			}
		}
	}
}

func runLibraryRenditionProcessing(ctx context.Context, server *Server) {
	if server.Library == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	workerID := "rendition-worker-" + uuid.NewString()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Library.ProcessRenditionJobs(ctx, workerID, 2); err != nil {
				log.Printf("Library rendition processing failed: %v", err)
			}
		}
	}
}

func runLibraryPeopleProcessing(ctx context.Context, server *Server) {
	if server.Library == nil {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	workerID := "people-worker-" + uuid.NewString()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Library.ProcessPeopleJobs(ctx, workerID, 4); err != nil {
				log.Printf("Library People processing failed: %v", err)
			}
		}
	}
}

func runAgentRetention(ctx context.Context, server *Server) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	retentionCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := server.Database.ProcessAICleanupJobs(ctx, 25); err != nil {
				log.Printf("AI privacy cleanup failed: %v", err)
			}
			if _, err := server.Database.PurgeExpiredAITransients(ctx, 250); err != nil {
				log.Printf("AI transient retention cleanup failed: %v", err)
			}
			if server.Spaces != nil {
				if _, err := server.Spaces.ProcessAccountDeletions(ctx, 10); err != nil {
					log.Printf("Account deletion cleanup failed: %v", err)
				}
			}
			if server.AI != nil {
				if _, err := server.AI.ProcessDueAIRecaps(ctx, time.Now().UTC(), 25); err != nil {
					log.Printf("AI recurring briefing processing failed: %v", err)
				}
			}
			if _, err := server.CleanupExpiredLibraryData(ctx, 100); err != nil {
				log.Printf("Library reservation cleanup failed: %v", err)
			}
			if _, err := server.CleanupExpiredJournalAssets(
				ctx, 24*time.Hour, 100,
			); err != nil {
				log.Printf("Journal asset cleanup failed: %v", err)
			}
			if server.Library != nil {
				if _, err := server.Database.ReleaseExpiredLibraryRenditionReservations(ctx, 100); err != nil {
					log.Printf("Library rendition reservation cleanup failed: %v", err)
				}
				if _, err := server.Library.PurgeExpiredRenditions(ctx, 20); err != nil {
					log.Printf("Library rendition purge failed: %v", err)
				}
			}
			if server.Spaces != nil {
				if _, err := server.Spaces.ProcessDueAgentWorkflows(ctx, time.Now().UTC(), 100); err != nil {
					log.Printf("Agent workflow schedule processing failed: %v", err)
				}
			}
			if _, err := server.Database.PurgeExpiredNotes(ctx, 100); err != nil {
				log.Printf("Note retention purge failed: %v", err)
			}
			retentionCounter++
			if retentionCounter%10 == 0 {
				if server.Spaces != nil {
					if _, err := server.Spaces.PurgeDueAccountDeletions(ctx, 25); err != nil {
						log.Printf("Account deletion retention purge failed: %v", err)
					}
				}
				if server.Library != nil {
					report, reconcileErr := server.Library.ReconcileLibraryObjects(
						ctx, 24*time.Hour, 250,
					)
					if reconcileErr != nil {
						log.Printf("Library R2 reconciliation failed: %v", reconcileErr)
					} else if report.OrphanObjectsDeleted > 0 ||
						report.MissingPermanentObjects > 0 ||
						report.MismatchedObjects > 0 ||
						report.InterruptedFinalizations > 0 {
						log.Printf(
							"Library R2 reconciliation: checked=%d expired=%d orphan_deleted=%d missing=%d mismatched=%d retryable_finalizations=%d",
							report.InventoryObjectsChecked,
							report.ExpiredUploads,
							report.OrphanObjectsDeleted,
							report.MissingPermanentObjects,
							report.MismatchedObjects,
							report.InterruptedFinalizations,
						)
					}
				}
				if _, err := server.Database.PurgeExpiredSpaceData(ctx); err != nil {
					log.Printf("Space retention purge failed: %v", err)
				}
			}
		}
	}
}

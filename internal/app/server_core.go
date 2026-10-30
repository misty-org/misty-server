package app

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/kannachi323/misty/server/internal/platform/db"
	api "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/browsersync"
	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/email"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
)

type Server struct {
	AbuseGuard               *api.AbuseGuard
	Router                   *chi.Mux
	Database                 *db.Database
	EmailSender              email.Sender
	AIAgent                  *agent.Service
	LibraryStore             api.LibraryObjectStore
	Library                  *api.SpaceLibraryService
	Spaces                   *api.SpacesService
	Realtime                 *api.RealtimeService
	PasswordResetStartURL    string
	PasswordResetRedirectURL string
	AuthHandoffStartURL      string
	WebsiteURL               string
	Telemetry                telemetry.Client
	HealthMonitor            *healthMonitor
	Metrics                  *metrics.Registry
	AIAnalyzer               *agent.SmartLibraryAnalyzer
	Models                   *modelruntime.Client
	AI                       *api.AIService
	AgentRuntime             api.AgentRuntimeConfig
	BrowserSync              *browsersync.BrowserSyncService
}

func CreateServer() (*Server, error) {
	if _, err := config.AgentModel(); err != nil {
		return nil, fmt.Errorf("validate agent model configuration: %w", err)
	}
	if err := ValidateProductionEnvironment(); err != nil {
		return nil, fmt.Errorf("validate production environment: %w", err)
	}
	warnOnProxyConfiguration()
	passwordResetRedirectURL, err := PasswordResetRedirectURLFromEnv()
	if err != nil {
		return nil, err
	}
	passwordResetStartURL, err := PasswordResetStartURLFromEnv()
	if err != nil {
		return nil, err
	}
	authHandoffStartURL, err := AuthHandoffStartURLFromEnv()
	if err != nil {
		return nil, err
	}
	websiteURL, err := WebsiteURLFromEnv()
	if err != nil {
		return nil, err
	}

	s := &Server{
		Router:                   chi.NewRouter(),
		Database:                 &db.Database{},
		PasswordResetStartURL:    passwordResetStartURL,
		PasswordResetRedirectURL: passwordResetRedirectURL,
		AuthHandoffStartURL:      authHandoffStartURL,
		WebsiteURL:               websiteURL,
		Telemetry:                telemetry.NewFromEnv(),
	}
	// Every model call goes through the agent runtime and the AI SDK.
	models, err := modelruntime.FromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure AI model runtime: %w", err)
	}
	s.Models = models
	s.AIAgent = agent.NewService(
		// The embodied companion and Agents both use misty_ask_conversations as their
		// durable task-thread store. Keeping the runtime wired to the database is
		// also required before an invocation can bind its surface metadata.
		agent.NewSessionStoreWithPersistence(0, s.Database),
		// Every model call in the process passes through this ceiling, so no
		// path can flood the providers.
		agent.NewBudgetedProvider(
			agent.NewAgentProvider(models),
			agent.ProviderBudgetFromEnv(),
		),
		agent.WithModelRuntime(models),
	)
	s.AIAnalyzer = &agent.SmartLibraryAnalyzer{
		Models: models,
		// The Gateway key here only opens realtime voice sockets.
		APIKey:  strings.TrimSpace(config.Getenv("AI_GATEWAY_API_KEY")),
		BaseURL: strings.TrimSpace(config.Getenv("AI_GATEWAY_BASE_URL")),
	}
	s.LibraryStore, err = LibraryStoreFromEnv()
	if err != nil {
		return nil, err
	}
	s.Library, err = api.NewSpaceLibraryService(s.Database, s.LibraryStore, true, api.UploadLimitsFromEnv())
	if err != nil {
		return nil, fmt.Errorf("configure Space Library: %w", err)
	}
	mediaProcessingEnabled := false
	if mediaProcessorBin, lookupErr := exec.LookPath("ffmpeg"); lookupErr == nil {
		processor, processorErr := api.NewFFmpegLibraryMediaProcessor(mediaProcessorBin)
		if processorErr != nil {
			return nil, fmt.Errorf("configure Library media processor: %w", processorErr)
		}
		s.Library.SetMediaProcessor(processor)
		extractor, extractorErr := api.NewFFprobeLibraryMetadataExtractor(mediaProcessorBin)
		if extractorErr != nil {
			return nil, fmt.Errorf("configure Library metadata extractor: %w", extractorErr)
		}
		s.Library.SetMetadataExtractor(extractor)
		mediaProcessingEnabled = true
	}
	s.Library.SetSubsystems(true, mediaProcessingEnabled, mediaProcessingEnabled, true)
	s.Library.SetNoteAssetsEnabled(true)
	s.Library.SetDrawingAssetsEnabled(true)
	spaceKey, err := spaceLinkEncryptionKeyFromEnv()
	if err != nil {
		return nil, err
	}
	s.Spaces, err = api.NewSpacesService(s.Database, s.AIAgent, spaceKey)
	if err != nil {
		return nil, fmt.Errorf("configure Space link encryption: %w", err)
	}
	journalCollab, err := api.JournalCollabConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure journal collaboration: %w", err)
	}
	s.Spaces.SetJournalCollab(journalCollab)
	agentRuntime, err := api.AgentRuntimeConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("configure Agent runtime: %w", err)
	}
	s.Spaces.SetAgentRuntime(agentRuntime)
	s.Spaces.SetModels(models)
	s.AIAnalyzer.ModelResolver = s.Spaces.ResolveAIModel
	s.AIAgent.SetModelResolver(s.Spaces.ResolveAIModel)
	s.AgentRuntime = agentRuntime
	s.Spaces.SetLibraryProvider(s.Library)
	s.Spaces.SetAvatarStore(s.LibraryStore)
	s.Realtime = api.NewRealtimeService(s.Database, s.Database.GetDSN())

	emailSender, err := email.NewSenderFromEnv()
	if err != nil {
		return nil, err
	}
	s.EmailSender = emailSender
	// Invitation emails link to the operator's own invite page. Without
	// MISTY_INVITATION_URL_BASE or MISTY_WEBSITE_URL, invitations still reach
	// the invitee inside Misty but no email is sent.
	invitationBaseURL := strings.TrimSpace(config.Getenv("MISTY_INVITATION_URL_BASE"))
	if website := strings.TrimRight(strings.TrimSpace(config.Getenv("MISTY_WEBSITE_URL")), "/"); invitationBaseURL == "" && website != "" {
		invitationBaseURL = website + "/invite"
	}
	s.Spaces.SetInvitationSender(emailSender, invitationBaseURL)
	s.HealthMonitor = NewHealthMonitor(s)

	return s, nil
}

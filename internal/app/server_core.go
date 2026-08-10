package app

import (
	"fmt"
	"os/exec"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
	"github.com/kannachi323/misty/server/internal/platform/email"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
)

type Server struct {
	Router                    *chi.Mux
	Database                  *db.Database
	EmailSender               email.Sender
	AIAgent                   *serveragent.Service
	LibraryStore              api.LibraryObjectStore
	Library                   *api.SpaceLibraryService
	Spaces                    *api.SpacesService
	Realtime                  *api.RealtimeService
	PasswordResetStartURL     string
	PasswordResetRedirectURL  string
	AuthHandoffStartURL       string
	WebsiteURL                string
	StripeWebhookPath         string
	WaitlistNotificationEmail string
	Telemetry                 telemetry.Client
	HealthMonitor             *healthMonitor
	Metrics                   *metrics.Registry
}

func CreateServer() (*Server, error) {
	if err := TestingValidateProductionEnvironment(); err != nil {
		return nil, fmt.Errorf("validate production environment: %w", err)
	}
	warnOnInsecureBillingConfiguration()
	passwordResetRedirectURL, err := TestingPasswordResetRedirectURLFromEnv()
	if err != nil {
		return nil, err
	}
	passwordResetStartURL, err := TestingPasswordResetStartURLFromEnv()
	if err != nil {
		return nil, err
	}
	stripeWebhookPath, err := TestingStripeWebhookPathFromEnv()
	if err != nil {
		return nil, err
	}
	authHandoffStartURL, err := TestingAuthHandoffStartURLFromEnv()
	if err != nil {
		return nil, err
	}
	websiteURL, err := TestingWebsiteURLFromEnv()
	if err != nil {
		return nil, err
	}

	s := &Server{
		Router:                    chi.NewRouter(),
		Database:                  &db.Database{},
		PasswordResetStartURL:     passwordResetStartURL,
		PasswordResetRedirectURL:  passwordResetRedirectURL,
		AuthHandoffStartURL:       authHandoffStartURL,
		WebsiteURL:                websiteURL,
		StripeWebhookPath:         stripeWebhookPath,
		WaitlistNotificationEmail: strings.TrimSpace(envconfig.Getenv("WAITLIST_NOTIFY_EMAIL")),
		Telemetry:                 telemetry.NewFromEnv(),
	}
	s.AIAgent = serveragent.NewService(
		// Agent execution state is attached to durable Space runs. The generic
		// completion service must never recreate the retired private chat store.
		serveragent.NewSessionStoreWithPersistence(0, nil),
		// Every paid model call in the process passes through this ceiling, so
		// no path can run up an unbounded provider bill.
		serveragent.NewBudgetedProvider(
			serveragent.NewAgentProviderFromEnv(),
			serveragent.ProviderBudgetFromEnv(),
		),
		serveragent.WithUsageMeter(appbilling.NewCreditMeter(s.Database)),
	)
	s.LibraryStore, err = TestingLibraryStoreFromEnv()
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
	peopleProcessingEnabled := false
	if endpoint := strings.TrimSpace(envconfig.Getenv("VISION_PROCESSOR_URL")); endpoint != "" {
		processor, processorErr := api.NewHTTPLibraryPeopleProcessor(endpoint, envconfig.Getenv("VISION_PROCESSOR_TOKEN"))
		if processorErr != nil {
			return nil, fmt.Errorf("configure Library People processor: %w", processorErr)
		}
		s.Library.SetPeopleProcessor(processor)
		peopleProcessingEnabled = true
	}
	s.Library.SetSubsystems(true, true, mediaProcessingEnabled, peopleProcessingEnabled, mediaProcessingEnabled, true, true, true, true)
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
	s.Spaces.SetLibraryProvider(s.Library)
	s.Spaces.SetAvatarStore(s.LibraryStore)
	s.Realtime = api.NewRealtimeService(s.Database, s.Database.GetDSN())

	emailSender, err := email.NewSenderFromEnv()
	if err != nil {
		return nil, err
	}
	s.EmailSender = emailSender
	invitationBaseURL := strings.TrimSpace(envconfig.Getenv("MISTY_INVITATION_URL_BASE"))
	if invitationBaseURL == "" {
		invitationBaseURL = "https://mistysys.com/invite"
	}
	s.Spaces.SetInvitationSender(emailSender, invitationBaseURL)
	s.HealthMonitor = TestingNewHealthMonitor(s)

	return s, nil
}

package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/cors"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	appbilling "github.com/kannachi323/misty/server/internal/billing"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
)

func (s *Server) MountHandlers() error {
	s.Router.Use(cors.Handler(cors.Options{
		AllowOriginFunc:  func(_ *http.Request, origin string) bool { return TestingIsAllowedCORSOrigin(origin) },
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   allowedCORSRequestHeaders,
		AllowCredentials: true,
		// The client must be able to read the marker that distinguishes a signed
		// download descriptor from a proxied file body.
		ExposedHeaders: []string{"X-Misty-Signed-Download", "X-Request-ID"},
		MaxAge:         300,
	}))
	s.Router.Use(TestingRequestObservabilityMiddleware)
	// The abuse guard runs first: a blocked caller is rejected before any
	// routing or handler work, and repeated per-route rejections escalate
	// into a block through the shared guard.
	// Blocks are persisted so a restart or a second instance still honours
	// them; the per-request counters stay in memory for speed.
	abuseGuard := api.NewAbuseGuard(api.DefaultAbusePolicy()).
		WithStore(context.Background(), s.Database)
	abuseGuard.StartRefreshLoop(context.Background(), 30*time.Second)
	s.Router.Use(abuseGuard.Middleware)
	s.Router.Use(api.NewAPIRateLimiter().WithAbuseGuard(abuseGuard).Middleware)
	s.Metrics = metrics.New()
	s.registerDomainGauges(s.Metrics)
	s.Router.Use(s.Metrics.Middleware)
	s.Router.Use(api.SelfHostedFeatureGate)
	s.Router.Use(api.SelfHostedEntitlementMiddleware(s.Database))

	passwordResetService, err := api.NewPasswordResetService(s.Database, s.EmailSender, s.PasswordResetStartURL, s.PasswordResetRedirectURL)
	if err != nil {
		return err
	}
	waitlistService, err := api.NewWaitlistService(s.Database, s.EmailSender, s.WaitlistNotificationEmail)
	if err != nil {
		return err
	}
	authHandoffService, err := api.NewAuthHandoffService(s.Database, s.AuthHandoffStartURL, s.WebsiteURL)
	if err != nil {
		return err
	}
	aiService := api.NewAIService(s.Database, s.AIAgent)
	libraryAnalyzer := &serveragent.SmartLibraryAnalyzer{
		APIKey:  strings.TrimSpace(envconfig.Getenv("AI_GATEWAY_API_KEY")),
		BaseURL: strings.TrimSpace(envconfig.Getenv("AI_GATEWAY_BASE_URL")),
	}
	intelligenceEnabled := libraryAnalyzer.APIKey != ""
	s.Library.SetIntelligence(libraryAnalyzer, intelligenceEnabled)
	smartLibraryService := api.NewSmartLibraryService(s.Database, libraryAnalyzer)
	mediaSearchService := api.NewMediaSearchService(s.Database, libraryAnalyzer)
	agentsService := api.NewAgentsService(s.Database)
	agentsService.SetAvatarStore(s.LibraryStore)
	if serverFeatureEnabled("MISTY_CONNECTED_DEVICES_ENABLED") {
		connectedDevicesConfig, configErr := api.ConnectedDevicesConfigFromEnv()
		if configErr != nil {
			return configErr
		}
		agentsService.SetConnectedDevices(connectedDevicesConfig)
	}
	registerHandler := api.RegisterWithTelemetry(s.Database, s.Telemetry)
	if api.InstanceConfigFromEnv().Deployment == "self_hosted" {
		registerHandler = api.ClosedSelfHostRegistration()
	}
	loginHandler := api.Login(s.Database)
	logoutHandler := api.Logout(s.Database)
	forgotPasswordHandler := passwordResetService.Forgot()
	startResetHandler := passwordResetService.Start()
	validateResetTokenHandler := passwordResetService.Validate()
	resetPasswordHandler := passwordResetService.Reset()
	mintHandoffHandler := authHandoffService.Mint()
	startHandoffHandler := authHandoffService.Start()
	waitlistJoinHandler := waitlistService.Join()
	healthHandler := s.HealthMonitor.Handler()
	instanceHandler := api.Instance(s.Database)
	entitlementHandler := api.MintSelfHostedEntitlement(s.Database)
	s.Router.Get("/health", healthHandler)
	s.Router.Get("/instance", instanceHandler)
	// Unmounted entirely when no token is configured: the output names every
	// route, its traffic volume, and its error rate.
	if token := metricsToken(); token != "" {
		s.Router.Get("/metrics", s.Metrics.Handler(token))
	}
	s.Router.Get("/api/health", healthHandler)
	s.Router.Get("/api/instance", instanceHandler)
	s.Router.MethodFunc(http.MethodGet, "/internal/self-host/collaboration/{resourceType}/{resourceID}", api.SelfHostCollaborationState(s.Database))
	s.Router.MethodFunc(http.MethodPut, "/internal/self-host/collaboration/{resourceType}/{resourceID}", api.SelfHostCollaborationState(s.Database))
	s.Router.MethodFunc(http.MethodDelete, "/internal/self-host/collaboration/{resourceType}/{resourceID}", api.SelfHostCollaborationState(s.Database))

	// Account management
	s.Router.Post("/register", registerHandler)
	s.Router.Post("/self-host/bootstrap", api.SelfHostBootstrap(s.Database))
	s.Router.Post("/self-host/enroll", api.SelfHostEnroll(s.Database))
	s.Router.Post("/self-host/invitations", api.SelfHostInvitation(s.Database))
	s.Router.Delete("/self-host/invitations/{invitationID}", api.SelfHostInvitation(s.Database))
	s.Router.Post("/self-host/entitlement", api.RenewSelfHostEntitlement(s.Database))
	s.Router.Post("/login", loginHandler)
	s.Router.Post("/logout", logoutHandler)
	s.Router.Post("/auth/forgot", forgotPasswordHandler)
	s.Router.Get("/auth/reset/start", startResetHandler)
	s.Router.Get("/auth/reset/validate", validateResetTokenHandler)
	s.Router.Post("/auth/reset", resetPasswordHandler)
	s.Router.Post("/auth/handoff", mintHandoffHandler)
	s.Router.Get("/auth/handoff/start", startHandoffHandler)
	s.Router.Post("/waitlist", waitlistJoinHandler)

	// Dashboard — authenticated endpoints
	s.Router.Get("/me", api.GetMe(s.Database))
	s.Router.Put("/me/profile", api.UpdateProfile(s.Database))
	s.Router.Post("/me/export", s.Spaces.AccountExportManifest())
	s.Router.Post("/me/deletion", s.Spaces.BeginAccountDeletion())
	s.Router.Post("/account/deletion/status", s.Spaces.AccountDeletionStatus())
	s.Router.MethodFunc(http.MethodGet, "/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.MethodFunc(http.MethodPut, "/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.Put("/me/device", api.UpdateDevice(s.Database))
	s.Router.Get("/me/settings", api.GetSettings(s.Database))
	s.Router.Put("/me/settings", api.UpdateSettings(s.Database))
	s.Router.Put("/me/telemetry", api.UpdateTelemetryPreferences(s.Database))
	s.Router.Post("/billing/trial/start", api.StartPersonalTrial(s.Database))
	s.Router.Post("/billing/self-host-entitlement", entitlementHandler)
	s.Router.Post("/billing/checkout-session", api.CreateCheckoutSession(s.Database))
	s.Router.Post("/billing/credit-checkout-session", api.CreateCreditCheckoutSession(s.Database))
	s.Router.Post("/billing/portal-session", api.CreatePortalSession(s.Database))
	s.Router.Get("/billing/usage", api.GetBillingUsage(s.Database))
	s.mountAIRoutes("/ai", aiService)
	s.mountMistyRoutes("", aiService)
	s.mountSmartLibraryRoutes("/ai/smart-library", smartLibraryService)
	s.mountMediaSearchRoutes("/ai/media-search", mediaSearchService)
	s.mountAgentsRoutes("", agentsService)
	s.mountSpacesRoutes("", s.Spaces, s.Realtime)
	if s.Library != nil {
		s.mountLibraryRoutes("", s.Library)
	}

	// Compatibility routes for clients configured with the /api prefix.
	s.Router.Post("/api/register", registerHandler)
	s.Router.Post("/api/self-host/bootstrap", api.SelfHostBootstrap(s.Database))
	s.Router.Post("/api/self-host/enroll", api.SelfHostEnroll(s.Database))
	s.Router.Post("/api/self-host/invitations", api.SelfHostInvitation(s.Database))
	s.Router.Delete("/api/self-host/invitations/{invitationID}", api.SelfHostInvitation(s.Database))
	s.Router.Post("/api/self-host/entitlement", api.RenewSelfHostEntitlement(s.Database))
	s.Router.Post("/api/login", loginHandler)
	s.Router.Post("/api/logout", logoutHandler)
	s.Router.Post("/api/auth/forgot", forgotPasswordHandler)
	s.Router.Get("/api/auth/reset/start", startResetHandler)
	s.Router.Get("/api/auth/reset/validate", validateResetTokenHandler)
	s.Router.Post("/api/auth/reset", resetPasswordHandler)
	s.Router.Post("/api/auth/handoff", mintHandoffHandler)
	s.Router.Get("/api/auth/handoff/start", startHandoffHandler)
	s.Router.Post("/api/waitlist", waitlistJoinHandler)
	s.Router.Get("/api/me", api.GetMe(s.Database))
	s.Router.Put("/api/me/profile", api.UpdateProfile(s.Database))
	s.Router.Post("/api/me/export", s.Spaces.AccountExportManifest())
	s.Router.Post("/api/me/deletion", s.Spaces.BeginAccountDeletion())
	s.Router.Post("/api/account/deletion/status", s.Spaces.AccountDeletionStatus())
	s.Router.MethodFunc(http.MethodGet, "/api/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.MethodFunc(http.MethodPut, "/api/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.Put("/api/me/device", api.UpdateDevice(s.Database))
	s.Router.Get("/api/me/settings", api.GetSettings(s.Database))
	s.Router.Put("/api/me/settings", api.UpdateSettings(s.Database))
	s.Router.Put("/api/me/telemetry", api.UpdateTelemetryPreferences(s.Database))
	s.Router.Post("/api/billing/trial/start", api.StartPersonalTrial(s.Database))
	s.Router.Post("/api/billing/self-host-entitlement", entitlementHandler)
	s.Router.Post("/api/billing/checkout-session", api.CreateCheckoutSession(s.Database))
	s.Router.Post("/api/billing/credit-checkout-session", api.CreateCreditCheckoutSession(s.Database))
	s.Router.Post("/api/billing/portal-session", api.CreatePortalSession(s.Database))
	s.Router.Get("/api/billing/usage", api.GetBillingUsage(s.Database))
	s.mountAIRoutes("/api/ai", aiService)
	s.mountMistyRoutes("/api", aiService)
	s.mountSmartLibraryRoutes("/api/ai/smart-library", smartLibraryService)
	s.mountMediaSearchRoutes("/api/ai/media-search", mediaSearchService)
	s.mountAgentsRoutes("/api", agentsService)
	s.mountSpacesRoutes("/api", s.Spaces, s.Realtime)
	if s.Library != nil {
		s.mountLibraryRoutes("/api", s.Library)
	}

	// Stripe webhook — called by Stripe on payment events
	if api.InstanceConfigFromEnv().Deployment == "hosted" {
		s.Router.Post(s.StripeWebhookPath, api.StripeWebhookWithService(envconfig.Getenv("STRIPE_WEBHOOK_SECRET"), appbilling.NewStripeService(s.Database, appbilling.WithTelemetry(s.Telemetry))))
		s.Spaces.StartProviderWorkers(context.Background())
	}

	return nil
}

func (s *Server) mountLibraryRoutes(prefix string, library *api.SpaceLibraryService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/search/spaces", library.GlobalSemanticSearch())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library", library.Items())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/facets", library.Facets())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/search/semantic", library.SemanticSearch())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/discovery", library.Discovery())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/discovery/{kind}/{groupID}/items", library.DiscoveryItems())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/discovery/memory/{memoryID}", library.MemoryPreference())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/pins", library.PinnedCollections())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/library/pins", library.PinnedCollections())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/duplicates/merge", library.MergeDuplicates())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/exports/download", library.ExportItems())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/imports", library.ImportItems())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/imports/history", library.ImportHistory())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/shared", library.SharedReferences())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/shared", library.SharedReferences())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/shared/{referenceID}/download", library.SharedReferenceDownload())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/grants/{grantID}", library.RevokeGrant())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/usage", library.Usage())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/reauthenticate", library.Reauthenticate())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/asset-stacks", library.AssetStacks())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/asset-stacks", library.AssetStacks())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/asset-stacks/{stackID}", library.AssetStack())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/asset-stacks/{stackID}", library.AssetStack())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads", library.InitiateUpload())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/content", library.UploadContent())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/finalize", library.FinalizeUpload())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/bulk", library.BulkItems())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/duplicate", library.DuplicateItems())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/items/{itemID}", library.Item())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/items/{itemID}", library.Item())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/items/{itemID}/download", library.DownloadItem())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/items/{itemID}/preview", library.PreviewItem())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/trash", library.TrashItem())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/restore", library.RestoreItem())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/attachments/{attachmentID}/download", library.DownloadAttachment())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/attachments/{attachmentID}/promote", library.PromoteAttachment())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/albums", library.Albums())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/albums", library.Albums())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/albums/{albumID}", library.Album())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/albums/{albumID}", library.Album())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/albums/{albumID}", library.Album())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/library/albums/{albumID}/organization", library.OrganizeAlbum())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/albums/{albumID}/order", library.ReorderAlbumItems())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/albums/{albumID}/items", library.AlbumItems())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/albums/{albumID}/items", library.AlbumItems())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/albums/{albumID}/items/{itemID}", library.AlbumItems())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/album-folders", library.AlbumFolders())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/album-folders", library.AlbumFolders())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/album-folders/{folderID}", library.AlbumFolder())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/album-folders/{folderID}", library.AlbumFolder())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/groups", library.Groups())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/groups", library.Groups())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/groups/{groupID}/items", library.GroupItems())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/people/policy", library.PeoplePolicy())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/people/policy", library.PeoplePolicy())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/people", library.People())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/people", library.People())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/people/merge", library.MergePeople())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/people/{personID}", library.Person())
	s.Router.MethodFunc(http.MethodPatch, prefix+"/spaces/{spaceID}/library/people/{personID}", library.Person())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/people/{personID}", library.Person())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/people/{personID}/items", library.PersonItems())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/people/{personID}/items", library.PersonItems())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/people/{personID}/items", library.PersonItems())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions", library.EditVersions())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions", library.EditVersions())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions/current", library.SelectEditVersion())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions/{editID}/render", library.RenderEditVersion())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions/{editID}", library.DeleteEditVersion())
}

package app

import (
	"context"
	"github.com/kannachi323/misty/server/internal/platform/security"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	browsersync "github.com/kannachi323/misty/server/internal/sync"
	"net/http"
	"time"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
)

func (s *Server) MountHandlers() error {
	if _, err := security.SessionSignerFromEnv(); err != nil {
		return err
	}
	s.Router.Use(cors.Handler(cors.Options{
		AllowOriginFunc:  func(_ *http.Request, origin string) bool { return TestingIsAllowedCORSOrigin(origin) },
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   allowedCORSRequestHeaders,
		AllowCredentials: true,
		// The client must be able to read the marker that distinguishes a signed
		// download descriptor from a proxied file body, and the retry delay on
		// rate-limit responses emitted before routing.
		ExposedHeaders: []string{"X-Misty-Signed-Download", "X-Request-ID", "Retry-After", "X-Misty-RateLimit-Scope"},
		MaxAge:         300,
	}))
	s.Router.Use(api.CookieCSRFProtection(TestingIsAllowedCORSOrigin))
	s.Router.Use(TestingRequestObservabilityMiddleware)
	s.Router.Use(middleware.Recoverer)
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
	s.Router.Use(api.SelfHostedAccountMiddleware(s.Database))

	passwordResetService, err := api.NewPasswordResetService(s.Database, s.EmailSender, s.PasswordResetStartURL, s.PasswordResetRedirectURL)
	if err != nil {
		return err
	}
	authHandoffService, err := api.NewAuthHandoffService(s.Database, s.AuthHandoffStartURL, s.WebsiteURL)
	if err != nil {
		return err
	}
	aiService := api.NewAIService(s.Database, s.AIAgent)
	s.AI = aiService
	aiService.SetAgentRuntime(s.AgentRuntime)
	aiService.AttachSpacesRuntime(s.Spaces)
	aiService.SetMetrics(s.Metrics)
	libraryAnalyzer := s.AIAnalyzer
	aiService.SetEmbeddingAnalyzer(libraryAnalyzer)
	aiService.SetAttachmentStore(s.LibraryStore)
	s.Spaces.SetSearchAnalyzer(libraryAnalyzer)
	intelligenceEnabled := libraryAnalyzer.APIKey != ""
	s.Library.SetIntelligence(libraryAnalyzer, intelligenceEnabled)
	smartLibraryService := api.NewSmartLibraryService(s.Database, libraryAnalyzer)
	mediaSearchService := api.NewMediaSearchService(s.Database, libraryAnalyzer)
	agentsService := api.NewAgentsService(s.Database)
	agentsService.SetVoiceAnalyzer(libraryAnalyzer)
	agentsService.SetVoiceMetrics(s.Metrics)
	if serverConnectedDevicesConfigured() {
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
	healthHandler := s.HealthMonitor.Handler()
	instanceHandler := api.Instance(s.Database)
	browserSync := browsersync.NewBrowserSyncService(s.Database)
	if s.AIAgent != nil {
		browserSync.SetRestoreCompleter(func(ctx context.Context, userID, prompt string) (string, error) {
			text, _, err := s.AIAgent.CompleteWithTierContext(ctx, userID, prompt, "assistant_ai", serveragent.TierLow)
			return text, err
		})
	}
	mountPublicRoutes := func(prefix string) {
		s.Router.Get(prefix+"/health", healthHandler)
		s.Router.Get(prefix+"/instance", instanceHandler)
		s.Router.Post(prefix+"/register", registerHandler)
		s.Router.Post(prefix+"/self-host/bootstrap", api.SelfHostBootstrap(s.Database))
		s.Router.Post(prefix+"/self-host/enroll", api.SelfHostEnroll(s.Database))
		s.Router.Post(prefix+"/self-host/invitations", api.SelfHostInvitation(s.Database))
		s.Router.Delete(prefix+"/self-host/invitations/{invitationID}", api.SelfHostInvitation(s.Database))
		s.Router.Post(prefix+"/login", loginHandler)
		s.Router.Post(prefix+"/logout", logoutHandler)
		s.Router.Post(prefix+"/auth/refresh", api.RefreshSession(s.Database))
		s.Router.Post(prefix+"/auth/forgot", forgotPasswordHandler)
		s.Router.Get(prefix+"/auth/reset/start", startResetHandler)
		s.Router.Get(prefix+"/auth/reset/validate", validateResetTokenHandler)
		s.Router.Post(prefix+"/auth/reset", resetPasswordHandler)
		s.Router.Post(prefix+"/auth/handoff", mintHandoffHandler)
		s.Router.Get(prefix+"/auth/handoff/start", startHandoffHandler)
		s.Router.Get(prefix+"/me", api.GetMe(s.Database))
		s.Router.Get(prefix+"/sync/workspace", browserSync.Workspace())
		s.Router.Post(prefix+"/sync/workspace", browserSync.Workspace())
		s.Router.Get(prefix+"/sync/devices", browserSync.Devices())
		s.Router.Post(prefix+"/sync/devices", browserSync.Devices())
		s.Router.Post(prefix+"/sync/control", browserSync.ControlDevice())
		s.Router.Post(prefix+"/sync/ticket", browserSync.Ticket())
		s.Router.Get(prefix+"/sync/ws", browserSync.Connect())
		s.Router.Post(prefix+"/sync/restore/step", browserSync.RestoreStep())
		s.Router.Put(prefix+"/me/profile", api.UpdateProfile(s.Database))
		s.Router.Post(prefix+"/me/export", s.Spaces.AccountExportManifest())
		s.Router.Post(prefix+"/me/deletion", s.Spaces.BeginAccountDeletion())
		s.Router.Post(prefix+"/account/deletion/status", s.Spaces.AccountDeletionStatus())
		s.Router.MethodFunc(http.MethodGet, prefix+"/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
		s.Router.MethodFunc(http.MethodPut, prefix+"/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
		s.Router.Put(prefix+"/me/device", api.UpdateDevice(s.Database))
		s.Router.Get(prefix+"/me/settings", api.GetSettings(s.Database))
		s.Router.Put(prefix+"/me/settings", api.UpdateSettings(s.Database))
		s.Router.Get(prefix+"/me/space-templates", api.PersonalSpaceTemplates(s.Database))
		s.Router.Post(prefix+"/me/space-templates", api.PersonalSpaceTemplates(s.Database))
		s.Router.Put(prefix+"/me/space-templates/{templateID}", api.PersonalSpaceTemplates(s.Database))
		s.Router.Delete(prefix+"/me/space-templates/{templateID}", api.PersonalSpaceTemplates(s.Database))
		s.Router.Get(prefix+"/me/agent-approvals", s.Spaces.AgentBrowserApprovals())
		s.Router.Get(prefix+"/me/agent-approvals/{approvalID}", s.Spaces.AgentBrowserApprovalReview())
		s.Router.Post(prefix+"/me/agent-invocations/{runID}/approvals/{approvalID}", s.Spaces.AgentBrowserApprovalDecision())
		s.Router.Get(prefix+"/me/agent-interventions", s.Spaces.AIUserInterventionControl())
		s.Router.Post(prefix+"/me/agent-interventions/{waitID}", s.Spaces.AIUserInterventionControl())

		s.Router.Get(prefix+"/me/home", api.HomeDashboard(s.Database))
		s.Router.Post(prefix+"/me/home/visits", api.RecordHomeVisit(s.Database))
		s.Router.Post(prefix+"/me/home/apps", api.RecordHomeAppActivity(s.Database))
		s.Router.Put(prefix+"/me/telemetry", api.UpdateTelemetryPreferences(s.Database))
		s.Router.Post(prefix+"/billing/checkout-session", api.CreateCheckoutSession(s.Database))
		s.Router.Post(prefix+"/billing/portal-session", api.CreatePortalSession(s.Database))
		s.Router.Get(prefix+"/billing/usage", api.GetBillingUsage(s.Database))
		s.mountAIRoutes(prefix+"/ai", aiService)
		s.mountMistyRoutes(prefix, aiService)
		s.mountSmartLibraryRoutes(prefix+"/ai/smart-library", smartLibraryService)
		s.mountMediaSearchRoutes(prefix+"/ai/media-search", mediaSearchService)
		s.mountAgentsRoutes(prefix, agentsService)
		s.mountSpacesRoutes(prefix, s.Spaces, s.Realtime)
		if s.Library != nil {
			s.mountLibraryRoutes(prefix, s.Library)
		}
	}

	// /v1 is the canonical hosted contract. The bare and /api trees remain
	// available for self-hosted installations and existing desktop releases.
	for _, prefix := range []string{"", "/api", "/v1"} {
		mountPublicRoutes(prefix)
	}

	// Unmounted entirely when no token is configured: the output names every
	// route, its traffic volume, and its error rate.
	if token := metricsToken(); token != "" {
		s.Router.Get("/metrics", s.Metrics.Handler(token))
	}
	s.Router.MethodFunc(http.MethodGet, "/internal/self-host/collaboration/{resourceType}/{resourceID}", api.SelfHostCollaborationState(s.Database))
	s.Router.MethodFunc(http.MethodPut, "/internal/self-host/collaboration/{resourceType}/{resourceID}", api.SelfHostCollaborationState(s.Database))
	s.Router.MethodFunc(http.MethodDelete, "/internal/self-host/collaboration/{resourceType}/{resourceID}", api.SelfHostCollaborationState(s.Database))
	s.Router.Post("/internal/agent-runtime/runs/{runID}/activate", s.Spaces.AgentRuntimeActivate())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/start-receipt", s.Spaces.AgentRuntimeStartReceipt())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/budget", s.Spaces.AgentRuntimeExecutionBudget())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/context", s.Spaces.AgentRuntimeContext())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/mcp-token", s.Spaces.AgentRuntimeMCPAccess())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/tools", s.Spaces.AgentRuntimeTool())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/events", s.Spaces.AgentRuntimeEvent())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/complete", s.Spaces.AgentRuntimeComplete())
	s.Router.Post("/mcp", s.Spaces.MistyMCP().ServeHTTP)

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
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/provider-import", library.ProviderImportProvenance())
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

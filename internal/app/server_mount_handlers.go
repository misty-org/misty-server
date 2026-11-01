package app

import (
	"context"
	"github.com/kannachi323/misty/server/internal/browsersync"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/kannachi323/misty/server/internal/platform/metrics"
)

// mountDeps holds the services and handlers that MountHandlers builds once and
// then wires into every public route prefix.
type mountDeps struct {
	health         http.HandlerFunc
	instance       http.HandlerFunc
	register       http.HandlerFunc
	login          http.HandlerFunc
	redeemSetupKey http.HandlerFunc
	logout         http.HandlerFunc
	mintHandoff    http.HandlerFunc
	startHandoff   http.HandlerFunc

	googleSignIn *api.GoogleSignInService
	browserSync  *browsersync.BrowserSyncService
	ai           *api.AIService
	agents       *api.AgentsService
}

func (s *Server) MountHandlers() error {
	if _, err := security.SessionSignerFromEnv(); err != nil {
		return err
	}
	s.mountRequestMiddleware()

	deps, err := s.buildMountDeps()
	if err != nil {
		return err
	}

	// /v1 is the canonical contract. The bare and /api trees remain available
	// for existing desktop releases.
	for _, prefix := range []string{"", "/api", "/v1"} {
		s.mountPublicRoutes(prefix, deps)
	}
	s.mountOperationalRoutes(deps)

	return nil
}

// mountRequestMiddleware installs the router-wide middleware stack.
func (s *Server) mountRequestMiddleware() {
	s.Router.Use(cors.Handler(cors.Options{
		AllowOriginFunc:  func(_ *http.Request, origin string) bool { return IsAllowedCORSOrigin(origin) },
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   allowedCORSRequestHeaders,
		AllowCredentials: true,
		// The client must be able to read the marker that distinguishes a signed
		// download descriptor from a proxied file body, and the retry delay on
		// rate-limit responses emitted before routing.
		ExposedHeaders: []string{"X-Misty-Signed-Download", "X-Request-ID", "Retry-After", "X-Misty-RateLimit-Scope"},
		MaxAge:         300,
	}))
	s.Router.Use(api.CookieCSRFProtection(IsAllowedCORSOrigin))
	s.Router.Use(RequestObservabilityMiddleware)
	s.Router.Use(middleware.Recoverer)
	// The abuse guard runs first: a blocked caller is rejected before any
	// routing or handler work, and repeated per-route rejections escalate
	// into a block through the shared guard.
	// Blocks are persisted so a restart or a second instance still honours
	// them; the per-request counters stay in memory for speed.
	abuseGuard := api.NewAbuseGuard(api.DefaultAbusePolicy()).
		WithStore(context.Background(), s.Database)
	s.AbuseGuard = abuseGuard
	s.Router.Use(abuseGuard.Middleware)
	s.Router.Use(api.NewAPIRateLimiter().WithAbuseGuard(abuseGuard).Middleware)
	// A suspended account is refused before any handler runs; its data stays.
	s.Suspensions = api.NewSuspensionGuard(s.Database)
	s.Router.Use(s.Suspensions.Middleware)
	s.Metrics = metrics.New()
	s.registerDomainGauges(s.Metrics)
	s.Router.Use(s.Metrics.Middleware)
	// Model keys stay on each device (docs/agents/runtime.md). An older
	// app's X-Misty-Models header is dropped before any handler or log sees it.
	s.Router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del("X-Misty-Models")
			next.ServeHTTP(w, r)
		})
	})
}

// buildMountDeps constructs the services and handlers in the order the route
// table has always depended on.
func (s *Server) buildMountDeps() (*mountDeps, error) {
	authHandoffService, err := api.NewAuthHandoffService(s.Database, s.AuthHandoffStartURL, s.WebsiteURL)
	if err != nil {
		return nil, err
	}
	deps := &mountDeps{}
	if err := s.wireAIServices(deps); err != nil {
		return nil, err
	}
	if err := s.wireAccountHandlers(deps, authHandoffService); err != nil {
		return nil, err
	}
	s.wireBrowserSync(deps)
	return deps, nil
}

// wireAIServices builds the AI, library search, and agents services.
func (s *Server) wireAIServices(deps *mountDeps) error {
	aiService := api.NewAIService(s.Database, s.AIAgent)
	s.AI = aiService
	aiService.SetAgentRuntime(s.AgentRuntime)
	aiService.AttachSpacesRuntime(s.Spaces)
	aiService.SetMetrics(s.Metrics)
	aiService.SetAttachmentStore(s.LibraryStore)
	agentsService := api.NewAgentsService(s.Database)
	if serverConnectedDevicesConfigured() {
		connectedDevicesConfig, configErr := api.ConnectedDevicesConfigFromEnv()
		if configErr != nil {
			return configErr
		}
		agentsService.SetConnectedDevices(connectedDevicesConfig)
	}
	deps.ai = aiService
	deps.agents = agentsService
	return nil
}

// wireAccountHandlers builds the sign-in and handoff handlers, plus the health
// and instance handlers. The server sends no email, so there is no email
// verification or reset by email: a setup key resets a password.
func (s *Server) wireAccountHandlers(deps *mountDeps, authHandoffService *api.AuthHandoffService) error {
	registerHandler := api.Register(s.Database)
	loginHandler := api.Login(s.Database)
	googleSignIn, err := api.NewGoogleSignInService(s.Database)
	if err != nil {
		return err
	}
	logoutHandler := api.Logout(s.Database)
	mintHandoffHandler := authHandoffService.Mint()
	startHandoffHandler := authHandoffService.Start()
	healthHandler := s.HealthMonitor.Handler()
	instanceHandler := api.Instance(s.Database, libraryStorageName(s.LibraryStore))

	deps.googleSignIn = googleSignIn
	deps.register = registerHandler
	deps.login = loginHandler
	deps.redeemSetupKey = api.RedeemSetupKey(s.Database)
	deps.logout = logoutHandler
	deps.mintHandoff = mintHandoffHandler
	deps.startHandoff = startHandoffHandler
	deps.health = healthHandler
	deps.instance = instanceHandler
	return nil
}

// wireBrowserSync builds the browser sync service and points its restore step
// at the AI agent when one is configured.
func (s *Server) wireBrowserSync(deps *mountDeps) {
	browserSync := browsersync.NewBrowserSyncService(s.Database)
	s.BrowserSync = browserSync
	deps.browserSync = browserSync
}

func (s *Server) mountLibraryRoutes(prefix string, library *api.SpaceLibraryService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library", library.Items())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/facets", library.Facets())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/usage", library.Usage())
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
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions", library.EditVersions())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions", library.EditVersions())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions/current", library.SelectEditVersion())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions/{editID}/render", library.RenderEditVersion())
	s.Router.MethodFunc(http.MethodDelete, prefix+"/spaces/{spaceID}/library/items/{itemID}/versions/{editID}", library.DeleteEditVersion())
}

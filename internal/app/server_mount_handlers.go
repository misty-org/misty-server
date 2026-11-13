package app

import (
	"context"
	"github.com/misty-org/misty-server/internal/platform/security"
	"net/http"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/misty-org/misty-server/internal/platform/metrics"
)

// mountDeps holds the services and handlers that MountHandlers builds once and
// then wires into the /v1 routes.
type mountDeps struct {
	health         http.HandlerFunc
	register       http.HandlerFunc
	login          http.HandlerFunc
	redeemSetupKey http.HandlerFunc
	logout         http.HandlerFunc

	googleSignIn *api.GoogleSignInService
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

	// Every client uses /v1. Docker, compose and the setup tools probe the
	// bare /health, so it stays outside /v1 too.
	s.mountPublicRoutes("/v1", deps)
	s.Router.Get("/health", deps.health)
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
}

// buildMountDeps constructs the sign-in handlers and the health handler.
func (s *Server) buildMountDeps() (*mountDeps, error) {
	deps := &mountDeps{}
	if err := s.wireAccountHandlers(deps); err != nil {
		return nil, err
	}
	return deps, nil
}

// wireAccountHandlers builds the sign-in handlers and the health handler.
// There is no reset by email: a setup key's sign-in link resets a password.
func (s *Server) wireAccountHandlers(deps *mountDeps) error {
	googleSignIn, err := api.NewGoogleSignInService(s.Database)
	if err != nil {
		return err
	}
	deps.googleSignIn = googleSignIn
	deps.register = api.Register(s.Database)
	deps.login = api.Login(s.Database)
	deps.redeemSetupKey = api.RedeemSetupKey(s.Database)
	deps.logout = api.Logout(s.Database)
	deps.health = s.HealthMonitor.Handler()
	return nil
}

func (s *Server) mountLibraryRoutes(prefix string, library *api.SpaceLibraryService) {
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library", library.Items())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/facets", library.Facets())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/usage", library.Usage())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads", library.InitiateUpload())
	s.Router.MethodFunc(http.MethodPut, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/content", library.UploadContent())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/finalize", library.FinalizeUpload())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/abort", library.AbortUpload())
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

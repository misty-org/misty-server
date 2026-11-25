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
	if err := s.mountRequestMiddleware(); err != nil {
		return err
	}

	deps, err := s.buildMountDeps()
	if err != nil {
		return err
	}

	s.mountPublicRoutes("/v1", deps)
	s.Router.Get("/health", deps.health)
	s.mountOperationalRoutes(deps)

	return nil
}

func (s *Server) mountRequestMiddleware() error {
	minimumAppVersion, err := minimumAppVersionMiddleware()
	if err != nil {
		return err
	}
	s.Router.Use(cors.Handler(cors.Options{
		AllowOriginFunc:  func(_ *http.Request, origin string) bool { return IsAllowedCORSOrigin(origin) },
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   allowedCORSRequestHeaders,
		AllowCredentials: true,
		ExposedHeaders:   []string{"X-Misty-Signed-Download", "X-Request-ID", "Retry-After", "X-Misty-RateLimit-Scope"},
		MaxAge:           300,
	}))
	s.Router.Use(minimumAppVersion)
	s.Router.Use(api.CookieCSRFProtection(IsAllowedCORSOrigin))
	s.Router.Use(RequestObservabilityMiddleware)
	s.Router.Use(middleware.Recoverer)
	abuseGuard := api.NewAbuseGuard(api.DefaultAbusePolicy()).
		WithStore(context.Background(), s.Database)
	s.AbuseGuard = abuseGuard
	s.Router.Use(abuseGuard.Middleware)
	s.Router.Use(api.NewAPIRateLimiter().WithAbuseGuard(abuseGuard).Middleware)
	s.Suspensions = api.NewSuspensionGuard(s.Database)
	s.Router.Use(s.Suspensions.Middleware)
	s.Metrics = metrics.New()
	s.registerDomainGauges(s.Metrics)
	s.Router.Use(s.Metrics.Middleware)
	return nil
}

func (s *Server) buildMountDeps() (*mountDeps, error) {
	deps := &mountDeps{}
	if err := s.wireAccountHandlers(deps); err != nil {
		return nil, err
	}
	return deps, nil
}

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
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/library/usage", library.Usage())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads", library.InitiateUpload())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/finalize", library.FinalizeUpload())
	s.Router.MethodFunc(http.MethodPost, prefix+"/spaces/{spaceID}/library/uploads/{uploadID}/abort", library.AbortUpload())
	s.Router.MethodFunc(http.MethodGet, prefix+"/spaces/{spaceID}/attachments/{attachmentID}/download", library.DownloadAttachment())
	s.Router.MethodFunc(http.MethodPost, prefix+"/dms/{dmID}/attachments/uploads", library.DMInitiateUpload())
	s.Router.MethodFunc(http.MethodPost, prefix+"/dms/{dmID}/attachments/uploads/{uploadID}/finalize", library.DMFinalizeUpload())
	s.Router.MethodFunc(http.MethodPost, prefix+"/dms/{dmID}/attachments/uploads/{uploadID}/abort", library.DMAbortUpload())
	s.Router.MethodFunc(http.MethodGet, prefix+"/dms/{dmID}/attachments/{attachmentID}/download", library.DMDownloadAttachment())
}

package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	api "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// mountPublicRoutes registers every public route group under one prefix. The
// groups run in this order so route registration matches the original table.
func (s *Server) mountPublicRoutes(prefix string, deps *mountDeps) {
	s.mountPublicAuthRoutes(prefix, deps)
	s.mountPublicAccountRoutes(prefix, deps)
	s.mountPublicProfileRoutes(prefix, deps)
	s.mountPublicFeatureRoutes(prefix, deps)
}

// mountPublicAuthRoutes registers health, sign-in, sign-out and password reset
// routes.
func (s *Server) mountPublicAuthRoutes(prefix string, deps *mountDeps) {
	s.Router.Get(prefix+"/health", deps.health)
	s.Router.Post(prefix+"/register", deps.register)
	s.Router.Post(prefix+"/login", deps.login)
	s.Router.Post(prefix+"/setup-keys/redeem", deps.redeemSetupKey)
	s.Router.Post(prefix+"/auth/confirm-email", api.ConfirmEmail(s.Database))
	s.Router.Post(prefix+"/auth/confirm-email/sign-in", api.ConfirmEmailSignIn(s.Database))
	s.Router.Get(prefix+"/auth/google", deps.googleSignIn.Available())
	s.Router.Post(prefix+"/auth/google", deps.googleSignIn.Begin())
	s.Router.Get(prefix+"/auth/google/start", deps.googleSignIn.Start())
	s.Router.Get(prefix+"/auth/google/callback", deps.googleSignIn.Callback())
	s.Router.Post(prefix+"/auth/google/complete", deps.googleSignIn.Complete())
	s.Router.Post(prefix+"/logout", deps.logout)
	s.Router.Post(prefix+"/auth/refresh", api.RefreshSession(s.Database))
}

// mountPublicAccountRoutes registers the current-user routes and the server
// owner's open alerts.
func (s *Server) mountPublicAccountRoutes(prefix string, deps *mountDeps) {
	s.Router.Get(prefix+"/me", api.GetMe(s.Database))
	s.Router.Put(prefix+"/me/password", api.ChangePassword(s.Database))
	s.Router.Post(prefix+"/me/account-proof", api.AccountProof(s.Database))
	s.Router.Get(prefix+"/me/invite-code", api.MyInviteCode(s.Database))
	s.Router.Get(prefix+"/invites/{code}", api.InviteByCode(s.Database))
	s.Router.Get(prefix+"/server/alerts", api.ServerAlerts(s.Database))
}

// mountPublicProfileRoutes registers profile, avatar, settings and Space people
// routes.
func (s *Server) mountPublicProfileRoutes(prefix string, deps *mountDeps) {
	s.Router.Get(prefix+"/me/events", s.Spaces.AccountEvents())
	s.Router.Post(prefix+"/me/export", s.Spaces.AccountExportManifest())
	s.Router.Post(prefix+"/me/deletion", s.Spaces.BeginAccountDeletion())
	s.Router.MethodFunc(http.MethodGet, prefix+"/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.MethodFunc(http.MethodPut, prefix+"/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.Get(prefix+"/me/settings", api.GetSettings(s.Database))
	s.Router.Put(prefix+"/me/settings", api.UpdateSettings(s.Database))
	s.Router.Put(prefix+"/me/username", api.UpdateUsername(s.Database))
	s.Router.Get(prefix+"/me/space-people", api.SpacePeople(s.Database))
}

// mountPublicFeatureRoutes mounts the Spaces and Library route trees.
func (s *Server) mountPublicFeatureRoutes(prefix string, deps *mountDeps) {
	s.mountSpacesRoutes(prefix, s.Spaces, s.Realtime)
	if s.Library != nil {
		s.mountLibraryRoutes(prefix, s.Library)
	}
}

// mountOperationalRoutes registers the unversioned metrics and operator endpoints.
func (s *Server) mountOperationalRoutes(deps *mountDeps) {
	// Unmounted entirely when no token is configured: the output names every
	// route, its traffic volume, and its error rate.
	if token := metricsToken(); token != "" {
		s.Router.Get("/metrics", s.Metrics.Handler(token))
	}
	s.mountOperatorRoutes()
}

// mountOperatorRoutes registers the operator account API, only when
// MISTY_OPERATOR_TOKEN is set. Setup key emails link to the website's static
// setup page, never to a page on this server, which serves API routes only.
func (s *Server) mountOperatorRoutes() {
	token := api.OperatorToken()
	if token == "" {
		return
	}
	operator := api.NewOperatorAccounts(s.Database, s.Suspensions)
	s.Router.Route("/operator/v1", func(r chi.Router) {
		r.Use(api.RequireOperator(token))
		operator.Mount(r)
		r.Post("/accounts/{id}/export", s.Spaces.OperatorAccountExport())
	})
}

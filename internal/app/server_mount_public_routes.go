package app

import (
	"net/http"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// mountPublicRoutes registers every public route group under one prefix. The
// groups run in this order so route registration matches the original table.
func (s *Server) mountPublicRoutes(prefix string, deps *mountDeps) {
	s.mountPublicAuthRoutes(prefix, deps)
	s.mountPublicAccountRoutes(prefix, deps)
	s.mountPublicSyncRoutes(prefix, deps)
	s.mountPublicProfileRoutes(prefix, deps)
	s.mountPublicBillingRoutes(prefix, deps)
	s.mountPublicFeatureRoutes(prefix, deps)
}

// mountPublicAuthRoutes registers health, instance, sign-in, sign-out, password
// reset, and handoff routes.
func (s *Server) mountPublicAuthRoutes(prefix string, deps *mountDeps) {
	s.Router.Get(prefix+"/health", deps.health)
	s.Router.Get(prefix+"/instance", deps.instance)
	s.Router.Post(prefix+"/register", deps.register)
	s.Router.Post(prefix+"/login", deps.login)
	s.Router.Get(prefix+"/auth/google", deps.googleSignIn.Available())
	s.Router.Post(prefix+"/auth/google", deps.googleSignIn.Begin())
	s.Router.Get(prefix+"/auth/google/start", deps.googleSignIn.Start())
	s.Router.Get(prefix+"/auth/google/callback", deps.googleSignIn.Callback())
	s.Router.Post(prefix+"/auth/google/complete", deps.googleSignIn.Complete())
	s.Router.Post(prefix+"/logout", deps.logout)
	s.Router.Post(prefix+"/auth/refresh", api.RefreshSession(s.Database))
	s.Router.Post(prefix+"/auth/forgot", deps.forgotPassword)
	s.Router.Get(prefix+"/auth/reset/start", deps.startReset)
	s.Router.Get(prefix+"/auth/reset/validate", deps.validateReset)
	s.Router.Post(prefix+"/auth/reset", deps.resetPassword)
	s.Router.Get(prefix+"/auth/verify/start", deps.verification.Verify())
	s.Router.Post(prefix+"/auth/handoff", deps.mintHandoff)
	s.Router.Get(prefix+"/auth/handoff/start", deps.startHandoff)
}

// mountPublicAccountRoutes registers the current-user, email verification, and
// referral routes.
func (s *Server) mountPublicAccountRoutes(prefix string, deps *mountDeps) {
	s.Router.Get(prefix+"/me", api.GetMe(s.Database))
	s.Router.Post(prefix+"/me/email/verify", deps.verification.Resend())
	s.Router.Get(prefix+"/me/referral", deps.verification.Referral())
	s.Router.Post(prefix+"/me/referral/claim", deps.verification.ClaimReferral())
	s.Router.Get(prefix+"/me/library-lock", api.LibraryLockPassword(s.Database))
	s.Router.Post(prefix+"/me/library-lock", api.LibraryLockPassword(s.Database))
}

// mountPublicSyncRoutes registers the browser sync vault, device, and restore routes.
func (s *Server) mountPublicSyncRoutes(prefix string, deps *mountDeps) {
	s.Router.Get(prefix+"/sync/vault", deps.browserSync.Vault())
	s.Router.Post(prefix+"/sync/vault", deps.browserSync.Vault())
	s.Router.Get(prefix+"/sync/devices", deps.browserSync.Devices())
	s.Router.Post(prefix+"/sync/devices", deps.browserSync.Devices())
	s.Router.Post(prefix+"/sync/control", deps.browserSync.ControlDevice())
	s.Router.Post(prefix+"/sync/ticket", deps.browserSync.Ticket())
	s.Router.Get(prefix+"/sync/ws", deps.browserSync.Connect())
	s.Router.Post(prefix+"/sync/restore/step", deps.browserSync.RestoreStep())
}

// mountPublicProfileRoutes registers profile, avatar, device, settings, space,
// agent intervention, and telemetry preference routes.
func (s *Server) mountPublicProfileRoutes(prefix string, deps *mountDeps) {
	s.Router.Put(prefix+"/me/profile", api.UpdateProfile(s.Database))
	s.Router.Post(prefix+"/me/export", s.Spaces.AccountExportManifest())
	s.Router.Post(prefix+"/me/deletion", s.Spaces.BeginAccountDeletion())
	s.Router.MethodFunc(http.MethodGet, prefix+"/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.MethodFunc(http.MethodPut, prefix+"/me/avatar", api.UserAvatar(s.Database, s.LibraryStore))
	s.Router.Put(prefix+"/me/device", api.UpdateDevice(s.Database))
	s.Router.Get(prefix+"/me/settings", api.GetSettings(s.Database))
	s.Router.Put(prefix+"/me/settings", api.UpdateSettings(s.Database))
	s.Router.Get(prefix+"/me/space-templates", api.PersonalSpaceTemplates(s.Database))
	s.Router.Post(prefix+"/me/space-templates", api.PersonalSpaceTemplates(s.Database))
	s.Router.Put(prefix+"/me/space-templates/{templateID}", api.PersonalSpaceTemplates(s.Database))
	s.Router.Delete(prefix+"/me/space-templates/{templateID}", api.PersonalSpaceTemplates(s.Database))
	s.Router.Get(prefix+"/me/space-people", api.SpacePeople(s.Database))
	s.Router.Get(prefix+"/me/agent-interventions", s.Spaces.AIUserInterventionControl())
	s.Router.Post(prefix+"/me/agent-interventions/{waitID}", s.Spaces.AIUserInterventionControl())

	s.Router.Put(prefix+"/me/telemetry", api.UpdateTelemetryPreferences(s.Database))
}

// mountPublicBillingRoutes registers checkout, portal, usage, and estimate routes.
func (s *Server) mountPublicBillingRoutes(prefix string, deps *mountDeps) {
	s.Router.Post(prefix+"/billing/checkout-session", api.CreateCheckoutSession(s.Database))
	s.Router.Post(prefix+"/billing/portal-session", api.CreatePortalSession(s.Database))
	s.Router.Get(prefix+"/billing/usage", api.GetBillingUsage(s.Database))
	s.Router.Post(prefix+"/billing/estimate", api.EstimateAIUsage(s.Database))
}

// mountPublicFeatureRoutes mounts the AI, library, agents, and spaces route trees.
func (s *Server) mountPublicFeatureRoutes(prefix string, deps *mountDeps) {
	s.mountAIRoutes(prefix+"/ai", deps.ai)
	s.mountMistyRoutes(prefix, deps.ai)
	s.mountSmartLibraryRoutes(prefix+"/ai/smart-library", deps.smartLibrary)
	s.mountMediaSearchRoutes(prefix+"/ai/media-search", deps.mediaSearch)
	s.mountAgentsRoutes(prefix, deps.agents)
	s.mountSpacesRoutes(prefix, s.Spaces, s.Realtime)
	if s.Library != nil {
		s.mountLibraryRoutes(prefix, s.Library)
	}
}

// mountOperationalRoutes registers the unversioned metrics, agent-runtime
// callback, and MCP endpoints.
func (s *Server) mountOperationalRoutes() {
	// Unmounted entirely when no token is configured: the output names every
	// route, its traffic volume, and its error rate.
	if token := metricsToken(); token != "" {
		s.Router.Get("/metrics", s.Metrics.Handler(token))
	}
	s.Router.Post("/internal/agent-runtime/runs/{runID}/activate", s.Spaces.AgentRuntimeActivate())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/start-receipt", s.Spaces.AgentRuntimeStartReceipt())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/budget", s.Spaces.AgentRuntimeExecutionBudget())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/steering", s.Spaces.AgentRuntimeSteering())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/context", s.Spaces.AgentRuntimeContext())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/model-provider", s.Spaces.AgentRuntimeModelProvider())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/mcp-token", s.Spaces.AgentRuntimeMCPAccess())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/tools", s.Spaces.AgentRuntimeTool())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/events", s.Spaces.AgentRuntimeEvent())
	s.Router.Post("/internal/agent-runtime/runs/{runID}/complete", s.Spaces.AgentRuntimeComplete())
	s.Router.Post("/mcp", s.Spaces.MistyMCP().ServeHTTP)
}

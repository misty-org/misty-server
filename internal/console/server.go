package console

import (
	"embed"
	"io/fs"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

//go:embed assets
var assetFiles embed.FS

// Server wires the console's routes. db is nil when Postgres was unreachable
// at startup; database-backed pages then say so instead of failing.
type Server struct {
	cfg     Config
	db      *db.Database
	auth    *authenticator
	health  *healthClient
	metrics *metricsSampler
	compose composeRunner
}

func NewServer(cfg Config, database *db.Database, launchToken string) *Server {
	client := &http.Client{Timeout: 5 * time.Second}
	return &Server{
		cfg:     cfg,
		db:      database,
		auth:    newAuthenticator(launchToken),
		health:  newHealthClient(cfg.APIURL, client),
		metrics: newMetricsSampler(cfg.APIURL, cfg.MetricsToken, client),
		compose: dockerCompose{dir: cfg.ServerDir},
	}
}

func (s *Server) Handler() http.Handler {
	assets, _ := fs.Sub(assetFiles, "assets")
	r := chi.NewRouter()
	r.Use(requireHost, securityHeaders)
	// Unauthenticated liveness probe used by `misty server up --gui` to reuse
	// a running console instead of starting a second one.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	r.Group(func(r chi.Router) {
		r.Use(s.auth.requireSession)
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/overview", http.StatusSeeOther)
		})
		r.Get("/overview/panel", s.overviewPanel)
		r.Get("/services/panel", s.requirePage("services", s.servicesPanel))
		r.Get("/services/{service}/logs", s.requirePage("services", s.serviceLogs))
		r.Post("/services/{service}/{action}", s.requirePage("services", s.serviceAction))
		r.Post("/accounts/{id}/revoke-sessions", s.revokeSessions)
		r.Get("/{page}", s.page)
	})
	return r
}

// requirePage hides an endpoint whenever its page is unavailable in this
// deployment mode (for example Services outside development).
func (s *Server) requirePage(slug string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := findPage(s.cfg, slug); !ok {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	page, ok := findPage(s.cfg, chi.URLParam(r, "page"))
	if !ok {
		http.Redirect(w, r, "/overview", http.StatusSeeOther)
		return
	}
	view := layoutView{
		Title:   page.Title,
		Active:  page.Slug,
		Nav:     navigation(s.cfg),
		CSRF:    sessionFrom(r.Context()).csrf,
		Content: page.Render(s, r),
	}
	render(w, r, layout(view))
}

// audit records an operator action; failures are logged into the response
// flow by callers only when the action itself failed.
func (s *Server) audit(r *http.Request, action, target, detail string) {
	if s.db != nil {
		_ = s.db.ConsoleRecordAudit(r.Context(), action, target, detail)
	}
}

func render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
	}
}

func renderUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = unauthorized().Render(r.Context(), w)
}

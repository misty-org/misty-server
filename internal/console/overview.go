package console

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type overviewView struct {
	Config     Config
	Health     HealthSnapshot
	HealthErr  string
	Metrics    MetricsSummary
	MetricsErr string
	DBLatency  time.Duration
	DBErr      string
	Accounts   int
	ActiveRuns int
	CountsErr  bool
	Activity   []db.ConsoleAuditEntry
}

func (s *Server) overviewPage(r *http.Request) templ.Component {
	return overviewPanel(s.loadOverview(r.Context()))
}

func (s *Server) overviewPanel(w http.ResponseWriter, r *http.Request) {
	render(w, r, overviewPanel(s.loadOverview(r.Context())))
}

func (s *Server) loadOverview(ctx context.Context) overviewView {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	view := overviewView{Config: s.cfg}
	if snapshot, err := s.health.Fetch(ctx); err != nil {
		view.HealthErr = err.Error()
	} else {
		view.Health = snapshot
	}
	if summary, err := s.metrics.Sample(ctx); err != nil {
		view.MetricsErr = err.Error()
	} else {
		view.Metrics = summary
	}
	if s.db == nil {
		view.DBErr = "not connected; check DB_HOST and DB_PORT"
		view.CountsErr = true
		return view
	}
	started := time.Now()
	if err := s.db.Conn.PingContext(ctx); err != nil {
		view.DBErr = "ping failed"
		view.CountsErr = true
		return view
	}
	view.DBLatency = time.Since(started)
	var err error
	if view.Accounts, view.ActiveRuns, err = s.db.ConsoleOverviewCounts(ctx); err != nil {
		view.CountsErr = true
	}
	view.Activity, _ = s.db.ConsoleRecentAudit(ctx, 5)
	return view
}

// subtitle is "hosted · development · v0.42.1 · up 3h 12m", dropping the
// version when the server reports its environment name as its version.
func (v overviewView) subtitle() string {
	text := v.Config.EnvironmentLabel()
	if v.Health.Version != "" && v.Health.Version != v.Config.Environment {
		text += " · " + v.Health.Version
	}
	if v.HealthErr == "" {
		text += " · up " + v.Health.Uptime()
	}
	return text
}

func (v overviewView) healthy() bool {
	return v.HealthErr == "" && v.Health.Healthy()
}

func (v overviewView) statusLabel() string {
	switch {
	case v.HealthErr != "":
		return "Unreachable"
	case v.Health.Healthy():
		return "Healthy"
	case v.Health.Status == "degraded":
		return "Degraded"
	default:
		return "Unavailable"
	}
}

func (v overviewView) requestsPerMin() string {
	if v.MetricsErr != "" || !v.Metrics.HasRate {
		return "—"
	}
	return strconv.FormatFloat(v.Metrics.RequestsPerMin, 'f', 0, 64)
}

func (v overviewView) errorRate() string {
	if v.MetricsErr != "" {
		return "—"
	}
	return strconv.FormatFloat(v.Metrics.ErrorRate*100, 'f', 1, 64) + "%"
}

func (v overviewView) count(n int) string {
	if v.CountsErr {
		return "—"
	}
	return strconv.Itoa(n)
}

func (v overviewView) dbDetail() string {
	if v.DBErr != "" {
		return v.DBErr
	}
	return "ok · " + strconv.FormatInt(v.DBLatency.Milliseconds(), 10) + " ms"
}

func checkDetail(check HealthCheck) string {
	text := check.Status
	if check.Message != "" {
		text += " · " + check.Message
	} else if check.DurationMS > 0 {
		text += " · " + strconv.FormatInt(check.DurationMS, 10) + " ms"
	}
	return text
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

const healthCacheTTL = 15 * time.Second

type HealthCheck struct {
	Status     string `json:"status"`
	Mode       string `json:"mode"`
	Critical   bool   `json:"critical"`
	Message    string `json:"message,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type HealthSnapshot struct {
	Status         string                 `json:"status"`
	Version        string                 `json:"version"`
	Environment    string                 `json:"environment"`
	ReleaseChannel string                 `json:"release_channel"`
	GeneratedAt    time.Time              `json:"generated_at"`
	UptimeSeconds  int64                  `json:"uptime_seconds"`
	Checks         map[string]HealthCheck `json:"checks"`
}

type healthMonitor struct {
	server    *Server
	startedAt time.Time
	mu        sync.Mutex
	cached    HealthSnapshot
	status    int
	expiresAt time.Time
}

func NewHealthMonitor(server *Server) *healthMonitor {
	return &healthMonitor{server: server, startedAt: time.Now().UTC()}
}

func (monitor *healthMonitor) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		snapshot, status := monitor.snapshot(r.Context())
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(snapshot)
	}
}

func (monitor *healthMonitor) snapshot(parent context.Context) (HealthSnapshot, int) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if time.Now().UTC().Before(monitor.expiresAt) {
		return monitor.cached, monitor.status
	}
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	monitor.cached, monitor.status = monitor.evaluate(ctx)
	monitor.expiresAt = time.Now().UTC().Add(healthCacheTTL)
	return monitor.cached, monitor.status
}

func (monitor *healthMonitor) evaluate(ctx context.Context) (HealthSnapshot, int) {
	now := time.Now().UTC()
	checks := map[string]HealthCheck{}
	checks["database"] = activeHealthCheck(ctx, true, func(ctx context.Context) error {
		if monitor.server == nil || monitor.server.Database == nil || monitor.server.Database.Conn == nil {
			return errors.New("database is not started")
		}
		if err := monitor.server.Database.Conn.PingContext(ctx); err != nil {
			return err
		}
		return nil
	})
	checks["library_storage"] = activeHealthCheck(ctx, true, func(ctx context.Context) error {
		if monitor.server == nil || monitor.server.LibraryStore == nil {
			return errors.New("Library storage is unavailable")
		}
		return monitor.server.LibraryStore.Health(ctx)
	})
	checks["realtime"] = activeHealthCheck(ctx, true, func(context.Context) error {
		if monitor.server == nil || monitor.server.Realtime == nil {
			return errors.New("realtime is unavailable")
		}
		return monitor.server.Realtime.Health()
	})

	checks["public_api"] = PublicAPIConfigurationCheck()
	// Models run on each account's own keys, so the server only needs the runtime.
	// An operator model (MISTY_AGENT_MODEL) is optional; a malformed one is reported.
	agentRuntimeCheck := environmentConfigurationCheck("MISTY_AGENT_RUNTIME_URL", "MISTY_AGENT_RUNTIME_CONTROL_SECRET")
	if _, err := config.AgentModel(); err != nil {
		agentRuntimeCheck = HealthCheck{Status: "unavailable", Mode: "configuration", Critical: false, Message: "MISTY_AGENT_MODEL is malformed"}
	}
	checks["agent_runtime"] = agentRuntimeCheck
	checks["email"] = environmentConfigurationCheck("MAILJET_API_KEY", "MAILJET_SECRET_KEY", "MAILJET_FROM_EMAIL")

	overall, status := SummarizeHealth(checks)
	version := strings.TrimSpace(config.Getenv("MISTY_SERVER_VERSION"))
	if version == "" {
		version = "development"
	}
	environment := strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT"))
	if environment == "" {
		environment = "development"
	}
	release := strings.TrimSpace(config.Getenv("MISTY_RELEASE_CHANNEL"))
	if release == "" {
		release = environment
	}
	return HealthSnapshot{
		Status: overall, Version: version, Environment: environment,
		ReleaseChannel: release, GeneratedAt: now,
		UptimeSeconds: int64(now.Sub(monitor.startedAt).Seconds()), Checks: checks,
	}, status
}

func activeHealthCheck(ctx context.Context, critical bool, check func(context.Context) error) HealthCheck {
	started := time.Now()
	result := HealthCheck{Status: "ok", Mode: "active", Critical: critical}
	if err := check(ctx); err != nil {
		result.Status = "unavailable"
		result.Message = "dependency check failed"
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result
}

func environmentConfigurationCheck(groups ...string) HealthCheck {
	result := HealthCheck{Status: "ready", Mode: "configuration", Critical: false}
	for _, group := range groups {
		configured := false
		for _, key := range strings.Split(group, "|") {
			if strings.TrimSpace(config.Getenv(key)) != "" {
				configured = true
				break
			}
		}
		if !configured {
			result.Status = "unconfigured"
			result.Message = "configuration incomplete"
			return result
		}
	}
	return result
}

func PublicAPIConfigurationCheck() HealthCheck {
	result := HealthCheck{Status: "ready", Mode: "configuration", Critical: false}
	value := strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL"))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		result.Status, result.Message = "unconfigured", "configuration incomplete"
		return result
	}
	if strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT")), "production") && parsed.Scheme != "https" {
		result.Status, result.Message = "degraded", "production API base must use HTTPS"
	}
	return result
}

func SummarizeHealth(checks map[string]HealthCheck) (string, int) {
	overall := "ok"
	for _, check := range checks {
		if check.Critical && check.Status != "ok" {
			return "unavailable", http.StatusServiceUnavailable
		}
		if check.Status != "ok" && check.Status != "ready" && check.Status != "disabled" {
			overall = "degraded"
		}
	}
	return overall, http.StatusOK
}

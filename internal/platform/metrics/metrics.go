// Package metrics exposes the server's internal counters in Prometheus text
// format so a collector can turn them into graphs and alerts.
//
// It deliberately depends on nothing else in this repository. Domain gauges are
// registered by the caller as sampling functions, which keeps this package free
// of import cycles with api and db.
package metrics

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry owns the metric collectors and the HTTP surfaces that feed them.
type Registry struct {
	registry *prometheus.Registry

	requests   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	inFlight   prometheus.Gauge
	sampleAge  prometheus.Gauge
	sampleFail prometheus.Counter

	mu       sync.Mutex
	samplers []sampler
}

type sampler struct {
	gauge prometheus.Gauge
	read  func(context.Context) (float64, error)
}

// New builds a registry preloaded with Go runtime and process collectors.
//
// A private registry is used rather than the global default so nothing a
// dependency registers can leak into our output.
func New() *Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Registry{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "misty_http_requests_total",
			Help: "HTTP requests by route pattern, method, and status class.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "misty_http_request_duration_seconds",
			Help: "HTTP request latency by route pattern and method.",
			// Tuned for a JSON API: sub-millisecond detail is noise, and
			// anything past ~8s is already a failure from the client's view.
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8},
		}, []string{"route", "method"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "misty_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		sampleAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "misty_metrics_sample_age_seconds",
			Help: "Seconds since domain gauges were last refreshed. A climbing value means sampling stalled and the domain gauges are stale.",
		}),
		sampleFail: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "misty_metrics_sample_failures_total",
			Help: "Domain gauge samples that returned an error.",
		}),
	}
	registry.MustRegister(m.requests, m.duration, m.inFlight, m.sampleAge, m.sampleFail)
	return m
}

// WatchGauge registers a domain gauge refreshed by the background sampler.
//
// read is never called during a scrape. Metrics must not be able to hold a
// database connection, or a slow query would turn every scrape into load on the
// system being measured.
func (m *Registry) WatchGauge(name, help string, read func(context.Context) (float64, error)) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	m.registry.MustRegister(gauge)
	m.mu.Lock()
	m.samplers = append(m.samplers, sampler{gauge: gauge, read: read})
	m.mu.Unlock()
}

// StartSampling refreshes every registered domain gauge on an interval until
// the context is cancelled.
func (m *Registry) StartSampling(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		m.sampleOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sampleOnce(ctx)
			}
		}
	}()
}

func (m *Registry) sampleOnce(ctx context.Context) {
	m.mu.Lock()
	samplers := make([]sampler, len(m.samplers))
	copy(samplers, m.samplers)
	m.mu.Unlock()

	for _, s := range samplers {
		// Each sample is individually bounded so one wedged query cannot stall
		// the whole refresh loop.
		sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		value, err := s.read(sampleCtx)
		cancel()
		if err != nil {
			m.sampleFail.Inc()
			// The gauge keeps its previous value; sample age is what reveals
			// that it has gone stale.
			continue
		}
		s.gauge.Set(value)
	}
	m.sampleAge.Set(0)
	go m.ageSampleAge(ctx, time.Now())
}

// ageSampleAge advances the staleness gauge between refreshes so a stalled
// sampler is visible rather than looking permanently fresh.
func (m *Registry) ageSampleAge(ctx context.Context, since time.Time) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			age := time.Since(since).Seconds()
			if age > 120 {
				return
			}
			m.sampleAge.Set(age)
		}
	}
}

// Middleware records request counts and latency per route.
func (m *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// chi's wrapper preserves http.Hijacker and http.Flusher. A naive
		// ResponseWriter wrapper would break the realtime WebSocket upgrade,
		// which needs to take over the connection.
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()
		m.inFlight.Inc()

		defer func() {
			m.inFlight.Dec()
			route := routePattern(r)
			method := r.Method
			m.requests.WithLabelValues(route, method, statusClass(wrapped.Status())).Inc()
			m.duration.WithLabelValues(route, method).Observe(time.Since(started).Seconds())
		}()

		next.ServeHTTP(wrapped, r)
	})
}

// routePattern returns the matched route template, never the raw URL.
//
// This is the difference between a few hundred time series and an unbounded
// number: labelling by raw path would mint a new series for every Space, note,
// and item id that has ever been requested.
func routePattern(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	// Unmatched requests are bucketed together. Scanners probing random paths
	// must not be able to inflate cardinality at will.
	return "unmatched"
}

// statusClass reduces the status code to its class, which is what alerting
// actually keys on and keeps the label set small.
func statusClass(status int) string {
	if status == 0 {
		// The handler never wrote a header; net/http will send 200.
		status = http.StatusOK
	}
	return strconv.Itoa(status/100) + "xx"
}

// Handler serves the metrics endpoint behind a bearer token.
//
// The output names every route, its traffic volume, and its error rate, so it
// is a meaningful disclosure. An empty token returns a handler that always
// refuses, and the caller is expected not to mount it at all.
func (m *Registry) Handler(token string) http.HandlerFunc {
	if token == "" {
		return func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics are not configured", http.StatusNotFound)
		}
	}
	expected := []byte("Bearer " + token)
	promHandler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	})
	return func(w http.ResponseWriter, r *http.Request) {
		provided := []byte(r.Header.Get("Authorization"))
		// Constant time, and length-checked first so the comparison itself
		// cannot leak the token's length.
		if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
			// 404 rather than 401: an unauthenticated caller learns nothing
			// about whether metrics exist here at all.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		promHandler.ServeHTTP(w, r)
	}
}

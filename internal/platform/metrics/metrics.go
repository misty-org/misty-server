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

type Registry struct {
	registry    *prometheus.Registry
	workerWakes *prometheus.CounterVec

	requests   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	inFlight   prometheus.Gauge
	sampleAge  prometheus.GaugeFunc
	lastSample time.Time
	traffic    *prometheus.CounterVec
	sampleFail prometheus.Counter

	mu       sync.Mutex
	samplers []sampler
}

type sampler struct {
	read func(context.Context) error
}

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
			Name:    "misty_http_request_duration_seconds",
			Help:    "HTTP request latency by route pattern and method.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 4, 8},
		}, []string{"route", "method"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "misty_http_requests_in_flight",
			Help: "HTTP requests currently being served.",
		}),
		sampleFail: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "misty_metrics_sample_failures_total",
			Help: "Domain gauge samples that returned an error.",
		}),
	}
	m.lastSample = time.Now()
	m.sampleAge = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "misty_metrics_sample_age_seconds",
		Help: "Seconds since the last fully successful domain sample (or startup before the first success).",
	}, func() float64 {
		m.mu.Lock()
		defer m.mu.Unlock()
		return time.Since(m.lastSample).Seconds()
	})
	m.initWorkers()
	m.initTraffic()
	registry.MustRegister(m.requests, m.duration, m.inFlight, m.sampleAge, m.sampleFail)
	return m
}

func (m *Registry) WatchGauge(name, help string, read func(context.Context) (float64, error)) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	m.registry.MustRegister(gauge)
	m.mu.Lock()
	m.samplers = append(m.samplers, sampler{read: func(ctx context.Context) error {
		value, err := read(ctx)
		if err == nil {
			gauge.Set(value)
		}
		return err
	}})
	m.mu.Unlock()
}

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

	complete := true
	for _, s := range samplers {
		sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.read(sampleCtx)
		cancel()
		if err != nil {
			complete = false
			m.sampleFail.Inc()
			continue
		}
	}
	if complete {
		m.mu.Lock()
		m.lastSample = time.Now()
		m.mu.Unlock()
	}
}

func (m *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		wrapped.Tee(bodyCounter{record: func(n int) { m.recordHTTP(r, "out", n) }})
		if r.Body != nil {
			r.Body = &countingBody{ReadCloser: r.Body, record: func(n int) { m.recordHTTP(r, "in", n) }}
		}
		started := time.Now()
		m.inFlight.Inc()

		defer func() {
			m.inFlight.Dec()
			route := routePattern(r)
			method := boundedMethod(r.Method)
			m.requests.WithLabelValues(route, method, statusClass(wrapped.Status())).Inc()
			m.duration.WithLabelValues(route, method).Observe(time.Since(started).Seconds())
		}()

		next.ServeHTTP(wrapped, r)
	})
}

func routePattern(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func statusClass(status int) string {
	if status == 0 {
		status = http.StatusOK
	}
	return strconv.Itoa(status/100) + "xx"
}

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
		if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		promHandler.ServeHTTP(w, r)
	}
}
